-- golang-migrate does not wrap a migration in a transaction; a half-applied schema is worse than a dirty version.
BEGIN;

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Roles are cluster-wide and may outlive a dropped database, so creation is conditional.
-- They never log in: deployments create login users and grant membership outside the repository.
DO $$
DECLARE
    role_name text;
BEGIN
    FOREACH role_name IN ARRAY ARRAY['impuls_gateway', 'impuls_syncer', 'impuls_optimizer'] LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = role_name) THEN
            EXECUTE format('CREATE ROLE %I NOLOGIN', role_name);
        END IF;
    END LOOP;
END $$;

CREATE SCHEMA ref;
CREATE SCHEMA catalog;
CREATE SCHEMA integration;
CREATE SCHEMA identity;
CREATE SCHEMA planning;
CREATE SCHEMA gateway_ops;

CREATE TABLE ref.city (
    code             text PRIMARY KEY,
    timezone         text NOT NULL,
    boundary         geometry(MultiPolygon, 4326),
    catalog_revision bigint NOT NULL DEFAULT 0 CHECK (catalog_revision >= 0),
    updated_at       timestamptz NOT NULL,
    CONSTRAINT city_timezone_check CHECK (
        (code, timezone) IN (('moscow', 'Europe/Moscow'), ('perm', 'Asia/Yekaterinburg'))
    ),
    CONSTRAINT city_boundary_valid CHECK (boundary IS NULL OR ST_IsValid(boundary))
);
CREATE INDEX city_boundary_gist ON ref.city USING gist (boundary);

CREATE TABLE ref.category (
    code      text PRIMARY KEY,
    title     text NOT NULL,
    is_active boolean NOT NULL DEFAULT true
);

-- A published bit position is never reused for another meaning: stored masks would silently change.
CREATE TABLE ref.interest_tag (
    bit   smallint PRIMARY KEY CHECK (bit BETWEEN 0 AND 63),
    code  text NOT NULL UNIQUE,
    title text NOT NULL
);

CREATE TABLE identity.user_account (
    id            uuid PRIMARY KEY,
    max_user_id   text NOT NULL UNIQUE,
    created_at    timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    account_state text NOT NULL CHECK (account_state IN ('active', 'disabled')),
    account_kind  text NOT NULL DEFAULT 'max' CHECK (account_kind IN ('max', 'test')),
    -- Test accounts live in a separate id namespace so they can never collide with a real MAX user.
    CONSTRAINT user_account_test_namespace CHECK ((account_kind = 'test') = (max_user_id LIKE 'test:%'))
);

CREATE TABLE identity.auth_session (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES identity.user_account (id),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    issued_via text NOT NULL CHECK (issued_via IN ('max_init_data', 'test_cli')),
    platform   text,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (expires_at > created_at)
);
CREATE INDEX auth_session_active_user_idx ON identity.auth_session (user_id) WHERE revoked_at IS NULL;
CREATE INDEX auth_session_expires_idx ON identity.auth_session (expires_at);

CREATE TABLE integration.source (
    id                uuid PRIMARY KEY,
    source_key        text NOT NULL UNIQUE,
    name              text NOT NULL,
    documentation_url text,
    access_mode       text NOT NULL CHECK (access_mode IN ('api', 'export', 'prepared', 'synthetic')),
    license_info      text,
    owner_contact     text,
    poll_interval_s   integer CHECK (poll_interval_s > 0),
    schema_version    text NOT NULL,
    is_enabled        boolean NOT NULL
);

CREATE TABLE integration.source_record (
    id                uuid PRIMARY KEY,
    source_id         uuid NOT NULL REFERENCES integration.source (id),
    city              text NOT NULL REFERENCES ref.city (code),
    external_id       text NOT NULL,
    source_url        text NOT NULL,
    last_seen_at      timestamptz NOT NULL,
    source_updated_at timestamptz,
    accepted_hash     bytea,
    provider_version  text,
    data_mode         text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    UNIQUE (source_id, external_id)
);
CREATE INDEX source_record_city_source_seen_idx ON integration.source_record (city, source_id, last_seen_at);

CREATE TABLE integration.raw_ingest (
    id                uuid PRIMARY KEY,
    source_record_id  uuid NOT NULL REFERENCES integration.source_record (id),
    raw_payload       bytea NOT NULL,
    content_type      text NOT NULL,
    content_hash      bytea NOT NULL,
    fetched_at        timestamptz NOT NULL,
    source_updated_at timestamptz,
    data_mode         text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    processing_state  text NOT NULL
        CHECK (processing_state IN ('pending', 'processing', 'applied', 'quarantined', 'failed')),
    request_id        text,
    UNIQUE (id, source_record_id)
);
CREATE INDEX raw_ingest_record_fetched_idx ON integration.raw_ingest (source_record_id, fetched_at DESC);
CREATE INDEX raw_ingest_unfinished_idx ON integration.raw_ingest (processing_state, fetched_at)
    WHERE processing_state IN ('pending', 'failed');

CREATE TABLE integration.sync_cursor (
    source_id              uuid NOT NULL REFERENCES integration.source (id),
    city                   text NOT NULL REFERENCES ref.city (code),
    fetch_cursor           jsonb,
    published_watermark    jsonb,
    materialized_watermark jsonb,
    last_attempt_at        timestamptz,
    last_success_at        timestamptz,
    last_error_code        text,
    PRIMARY KEY (source_id, city)
);

CREATE TABLE catalog.place (
    id                    uuid NOT NULL,
    city                  text NOT NULL REFERENCES ref.city (code),
    title                 text NOT NULL,
    normalized_title      text NOT NULL,
    category              text REFERENCES ref.category (code),
    tag_mask              bit(64) NOT NULL DEFAULT 0::bit(64),
    coordinates           geometry(Point, 4326) NOT NULL,
    fias_id               uuid,
    address_text          text,
    opening_rules         jsonb NOT NULL DEFAULT '{}',
    data_mode             text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    card_source_record_id uuid REFERENCES integration.source_record (id),
    is_active             boolean NOT NULL,
    review_required       boolean NOT NULL,
    created_at            timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL,
    PRIMARY KEY (id, city),
    CONSTRAINT place_coordinates_valid CHECK (
        ST_IsValid(coordinates) AND ST_X(coordinates) BETWEEN -180 AND 180 AND ST_Y(coordinates) BETWEEN -90 AND 90
    )
) PARTITION BY LIST (city);
CREATE TABLE catalog.place_moscow PARTITION OF catalog.place FOR VALUES IN ('moscow');
CREATE TABLE catalog.place_perm PARTITION OF catalog.place FOR VALUES IN ('perm');
CREATE INDEX place_coordinates_gist ON catalog.place USING gist (coordinates);
CREATE INDEX place_normalized_title_trgm ON catalog.place USING gin (normalized_title gin_trgm_ops);

CREATE TABLE catalog.place_entrance (
    id                   uuid NOT NULL,
    city                 text NOT NULL,
    place_id             uuid NOT NULL,
    coordinates          geometry(Point, 4326) NOT NULL,
    allowed_modes        text[] NOT NULL,
    accessibility_status text NOT NULL CHECK (accessibility_status IN ('confirmed', 'unavailable', 'unknown')),
    verification_status  text NOT NULL CHECK (verification_status IN ('verified', 'estimated', 'unknown')),
    source_record_id     uuid REFERENCES integration.source_record (id),
    observed_at          timestamptz,
    updated_at           timestamptz NOT NULL,
    PRIMARY KEY (id, city),
    UNIQUE (id, place_id, city),
    FOREIGN KEY (place_id, city) REFERENCES catalog.place (id, city),
    CONSTRAINT place_entrance_coordinates_valid CHECK (
        ST_IsValid(coordinates) AND ST_X(coordinates) BETWEEN -180 AND 180 AND ST_Y(coordinates) BETWEEN -90 AND 90
    )
);
CREATE INDEX place_entrance_coordinates_gist ON catalog.place_entrance USING gist (coordinates);
CREATE INDEX place_entrance_place_idx ON catalog.place_entrance (place_id, city);

CREATE TABLE catalog.event (
    id                    uuid NOT NULL,
    city                  text NOT NULL,
    place_id              uuid NOT NULL,
    title                 text NOT NULL,
    normalized_title      text NOT NULL,
    category              text NOT NULL REFERENCES ref.category (code),
    tag_mask              bit(64) NOT NULL DEFAULT 0::bit(64),
    organizer_name        text,
    age_min               smallint CHECK (age_min >= 0),
    age_max               smallint CHECK (age_max >= 0),
    requirements          jsonb NOT NULL DEFAULT '{}',
    data_mode             text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    card_source_record_id uuid REFERENCES integration.source_record (id),
    is_active             boolean NOT NULL,
    review_required       boolean NOT NULL,
    created_at            timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL,
    PRIMARY KEY (id, city),
    UNIQUE (id, place_id, city),
    FOREIGN KEY (place_id, city) REFERENCES catalog.place (id, city),
    CHECK (age_min <= age_max)
);
CREATE INDEX event_place_idx ON catalog.event (place_id, city);
CREATE INDEX event_normalized_title_trgm ON catalog.event USING gin (normalized_title gin_trgm_ops);

CREATE TABLE catalog.session (
    id                       uuid NOT NULL,
    city                     text NOT NULL,
    event_id                 uuid NOT NULL,
    slot_type                text NOT NULL CHECK (slot_type IN ('FIXED_SESSION', 'CONTINUOUS_WINDOW')),
    starts_at                timestamptz NOT NULL,
    ends_at                  timestamptz NOT NULL,
    min_duration_s           integer NOT NULL CHECK (min_duration_s > 0),
    recommended_duration_s   integer NOT NULL,
    buffer_s                 integer NOT NULL DEFAULT 0 CHECK (buffer_s >= 0),
    last_entry_at            timestamptz,
    late_entry_allowed       boolean,
    registration_deadline    timestamptz,
    access_type              text NOT NULL CHECK (access_type IN ('free', 'registration', 'ticket')),
    availability_status      text NOT NULL
        CHECK (availability_status IN ('available', 'registration_required', 'sold_out', 'cancelled', 'unknown')),
    availability_observed_at timestamptz,
    is_hard_constraint       boolean NOT NULL,
    booking_url              text,
    cancellation_reason      text,
    data_mode                text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    card_source_record_id    uuid REFERENCES integration.source_record (id),
    version                  bigint NOT NULL CHECK (version > 0),
    updated_at               timestamptz NOT NULL,
    PRIMARY KEY (id, city),
    UNIQUE (id, event_id, city),
    FOREIGN KEY (event_id, city) REFERENCES catalog.event (id, city),
    CHECK (ends_at > starts_at),
    CHECK (recommended_duration_s >= min_duration_s),
    CHECK (last_entry_at BETWEEN starts_at AND ends_at)
);
CREATE INDEX session_event_window_idx ON catalog.session (event_id, city, starts_at, ends_at);
CREATE INDEX session_bookable_idx ON catalog.session (event_id, city, starts_at, ends_at)
    WHERE availability_status IN ('available', 'registration_required', 'unknown');

CREATE TABLE catalog.price_offer (
    id                  uuid NOT NULL,
    city                text NOT NULL,
    session_id          uuid NOT NULL,
    price_status        text NOT NULL CHECK (price_status IN ('free', 'fixed', 'range', 'unknown')),
    audience            text NOT NULL DEFAULT 'general'
        CHECK (audience IN ('general', 'child', 'student', 'senior', 'other')),
    tariff_label        text,
    eligibility_age_min smallint CHECK (eligibility_age_min >= 0),
    eligibility_age_max smallint CHECK (eligibility_age_max >= 0),
    amount_min          bigint,
    amount_max          bigint,
    currency            char(3),
    benefit_programs    text[] NOT NULL DEFAULT '{}',
    purchase_url        text,
    source_record_id    uuid NOT NULL REFERENCES integration.source_record (id),
    observed_at         timestamptz NOT NULL,
    valid_until         timestamptz,
    data_mode           text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    is_active           boolean NOT NULL,
    PRIMARY KEY (id, city),
    UNIQUE (id, session_id, city),
    FOREIGN KEY (session_id, city) REFERENCES catalog.session (id, city),
    CHECK (eligibility_age_min <= eligibility_age_max),
    CHECK (valid_until > observed_at),
    -- An unknown price must never be stored as zero, and a free one must be an explicit zero.
    CONSTRAINT price_offer_amounts_match_status CHECK (COALESCE(
        CASE price_status
            WHEN 'free' THEN amount_min = 0 AND amount_max = 0
            WHEN 'fixed' THEN amount_min >= 0 AND amount_min = amount_max
            WHEN 'range' THEN amount_min >= 0 AND amount_min <= amount_max
            WHEN 'unknown' THEN amount_min IS NULL AND amount_max IS NULL
        END, false)),
    CONSTRAINT price_offer_currency_required CHECK ((amount_min IS NULL AND amount_max IS NULL) OR currency IS NOT NULL)
);
CREATE INDEX price_offer_session_idx ON catalog.price_offer (session_id, city, is_active);

CREATE TABLE catalog.bridge_anchor (
    id                  uuid NOT NULL,
    city                text NOT NULL REFERENCES ref.city (code),
    title               text NOT NULL,
    coordinates         geometry(Point, 4326) NOT NULL,
    path_geometry       geometry(LineString, 4326),
    approaches          jsonb NOT NULL DEFAULT '{}',
    allowed_modes       text[] NOT NULL,
    verification_status text NOT NULL CHECK (verification_status IN ('verified', 'estimated', 'unknown')),
    source_record_id    uuid REFERENCES integration.source_record (id),
    observed_at         timestamptz,
    is_active           boolean NOT NULL,
    PRIMARY KEY (id, city),
    CONSTRAINT bridge_anchor_coordinates_valid CHECK (
        ST_IsValid(coordinates) AND ST_X(coordinates) BETWEEN -180 AND 180 AND ST_Y(coordinates) BETWEEN -90 AND 90
    ),
    CONSTRAINT bridge_anchor_path_valid CHECK (path_geometry IS NULL OR ST_IsValid(path_geometry)),
    CONSTRAINT bridge_anchor_verified_has_evidence CHECK (
        verification_status <> 'verified' OR (path_geometry IS NOT NULL AND source_record_id IS NOT NULL)
    )
);
CREATE INDEX bridge_anchor_coordinates_gist ON catalog.bridge_anchor USING gist (coordinates);
CREATE INDEX bridge_anchor_path_gist ON catalog.bridge_anchor USING gist (path_geometry);
CREATE INDEX bridge_anchor_city_active_idx ON catalog.bridge_anchor (city, is_active);

CREATE TABLE catalog.barrier (
    id                  uuid NOT NULL,
    city                text NOT NULL REFERENCES ref.city (code),
    barrier_type        text NOT NULL CHECK (barrier_type IN ('water', 'restricted', 'other')),
    geometry            geometry(MultiPolygon, 4326) NOT NULL CHECK (ST_IsValid(geometry)),
    restricted_modes    text[] NOT NULL,
    valid_from          timestamptz,
    valid_until         timestamptz,
    source_record_id    uuid NOT NULL REFERENCES integration.source_record (id),
    verification_status text NOT NULL,
    observed_at         timestamptz NOT NULL,
    PRIMARY KEY (id, city),
    CHECK (valid_until > valid_from)
);
CREATE INDEX barrier_geometry_gist ON catalog.barrier USING gist (geometry);
CREATE INDEX barrier_city_idx ON catalog.barrier (city);

CREATE TABLE integration.entity_link (
    id                uuid PRIMARY KEY,
    source_record_id  uuid NOT NULL REFERENCES integration.source_record (id),
    city              text NOT NULL REFERENCES ref.city (code),
    place_id          uuid,
    event_id          uuid,
    session_id        uuid,
    target_kind       text NOT NULL GENERATED ALWAYS AS (
        CASE
            WHEN place_id IS NOT NULL THEN 'place'
            WHEN event_id IS NOT NULL THEN 'event'
            WHEN session_id IS NOT NULL THEN 'session'
        END) STORED,
    target_id         uuid NOT NULL GENERATED ALWAYS AS (COALESCE(place_id, event_id, session_id)) STORED,
    match_score       numeric(6, 5),
    resolution_status text NOT NULL CHECK (resolution_status IN ('linked', 'review_required')),
    resolved_at       timestamptz NOT NULL,
    CHECK (num_nonnulls(place_id, event_id, session_id) = 1),
    FOREIGN KEY (place_id, city) REFERENCES catalog.place (id, city),
    FOREIGN KEY (event_id, city) REFERENCES catalog.event (id, city),
    FOREIGN KEY (session_id, city) REFERENCES catalog.session (id, city),
    UNIQUE (id, source_record_id, target_kind, target_id, city)
);
CREATE UNIQUE INDEX entity_link_place_uq ON integration.entity_link (source_record_id, place_id, city)
    WHERE place_id IS NOT NULL;
CREATE UNIQUE INDEX entity_link_event_uq ON integration.entity_link (source_record_id, event_id, city)
    WHERE event_id IS NOT NULL;
CREATE UNIQUE INDEX entity_link_session_uq ON integration.entity_link (source_record_id, session_id, city)
    WHERE session_id IS NOT NULL;
CREATE INDEX entity_link_place_idx ON integration.entity_link (place_id, city) WHERE place_id IS NOT NULL;
CREATE INDEX entity_link_event_idx ON integration.entity_link (event_id, city) WHERE event_id IS NOT NULL;
CREATE INDEX entity_link_session_idx ON integration.entity_link (session_id, city) WHERE session_id IS NOT NULL;

-- Composite keys pin a fact to one source record: its link and its raw payload cannot come from different records.
CREATE TABLE integration.attribute_fact (
    id                uuid PRIMARY KEY,
    entity_link_id    uuid NOT NULL,
    raw_ingest_id     uuid NOT NULL,
    source_record_id  uuid NOT NULL,
    target_kind       text NOT NULL CHECK (target_kind IN ('place', 'event', 'session')),
    target_id         uuid NOT NULL,
    city              text NOT NULL,
    attribute_name    text NOT NULL,
    value             jsonb NOT NULL,
    source_updated_at timestamptz,
    fetched_at        timestamptz NOT NULL,
    verified_at       timestamptz,
    data_mode         text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    trust_rank        smallint NOT NULL,
    is_selected       boolean NOT NULL DEFAULT false,
    FOREIGN KEY (entity_link_id, source_record_id, target_kind, target_id, city)
        REFERENCES integration.entity_link (id, source_record_id, target_kind, target_id, city),
    FOREIGN KEY (raw_ingest_id, source_record_id) REFERENCES integration.raw_ingest (id, source_record_id)
);
CREATE INDEX attribute_fact_link_attribute_idx ON integration.attribute_fact (entity_link_id, attribute_name, fetched_at DESC);
-- Spans all links of an entity. Not deferrable: clear the previous selection before selecting a new fact.
CREATE UNIQUE INDEX attribute_fact_selected_uq ON integration.attribute_fact (target_kind, target_id, city, attribute_name)
    WHERE is_selected;

CREATE TABLE integration.quarantine (
    id            uuid PRIMARY KEY,
    raw_ingest_id uuid NOT NULL REFERENCES integration.raw_ingest (id),
    reason_code   text NOT NULL CHECK (reason_code IN ('InvalidSchema', 'CorruptedGeometry', 'GEO_DISCREPANCY_QUARANTINE')),
    details       jsonb NOT NULL,
    state         text NOT NULL CHECK (state IN ('open', 'resolved', 'rejected')),
    created_at    timestamptz NOT NULL,
    resolved_at   timestamptz,
    UNIQUE (raw_ingest_id, reason_code)
);
CREATE INDEX quarantine_state_reason_idx ON integration.quarantine (state, reason_code, created_at);

CREATE TABLE integration.change_delivery (
    id               uuid PRIMARY KEY,
    change_id        uuid NOT NULL,
    city             text NOT NULL REFERENCES ref.city (code),
    catalog_revision bigint,
    destination      text NOT NULL CHECK (destination IN ('kafka', 'redis', 'gateway')),
    event_type       text NOT NULL,
    source_record_id uuid REFERENCES integration.source_record (id),
    raw_ingest_id    uuid REFERENCES integration.raw_ingest (id),
    payload          jsonb NOT NULL,
    state            text NOT NULL CHECK (state IN ('pending', 'in_flight', 'delivered', 'failed')),
    attempts         integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at  timestamptz NOT NULL,
    lease_until      timestamptz,
    created_at       timestamptz NOT NULL,
    delivered_at     timestamptz,
    UNIQUE (change_id, destination, event_type)
);
CREATE INDEX change_delivery_due_idx ON integration.change_delivery (next_attempt_at) WHERE state IN ('pending', 'failed');

CREATE TABLE catalog.leisure_poi (
    id               uuid NOT NULL,
    city             text NOT NULL,
    title            text NOT NULL,
    normalized_title text NOT NULL,
    categories       text[] NOT NULL CHECK (cardinality(categories) >= 1),
    tag_mask         bit(64) NOT NULL,
    data_mode        text NOT NULL CHECK (data_mode IN ('live', 'prepared', 'synthetic')),
    coordinates      geometry(Point, 4326) NOT NULL,
    h3_res8          bigint NOT NULL,
    h3_res11         bigint NOT NULL,
    base_score       numeric(8, 3) NOT NULL,
    benefit_programs text[] NOT NULL DEFAULT '{}',
    catalog_revision bigint NOT NULL,
    is_active        boolean NOT NULL,
    updated_at       timestamptz NOT NULL,
    PRIMARY KEY (id, city),
    FOREIGN KEY (id, city) REFERENCES catalog.place (id, city),
    CONSTRAINT leisure_poi_coordinates_valid CHECK (
        ST_IsValid(coordinates) AND ST_X(coordinates) BETWEEN -180 AND 180 AND ST_Y(coordinates) BETWEEN -90 AND 90
    )
) PARTITION BY LIST (city);
CREATE TABLE catalog.leisure_poi_moscow PARTITION OF catalog.leisure_poi FOR VALUES IN ('moscow');
CREATE TABLE catalog.leisure_poi_perm PARTITION OF catalog.leisure_poi FOR VALUES IN ('perm');
CREATE INDEX leisure_poi_coordinates_gist ON catalog.leisure_poi USING gist (coordinates);
CREATE INDEX leisure_poi_geography_gist ON catalog.leisure_poi USING gist ((coordinates::geography));
CREATE INDEX leisure_poi_normalized_title_trgm ON catalog.leisure_poi USING gin (normalized_title gin_trgm_ops);
CREATE INDEX leisure_poi_categories_gin ON catalog.leisure_poi USING gin (categories);
CREATE INDEX leisure_poi_h3_res8_idx ON catalog.leisure_poi (h3_res8);
CREATE INDEX leisure_poi_h3_res11_idx ON catalog.leisure_poi (h3_res11);

CREATE TABLE catalog.entity_embedding (
    id            uuid PRIMARY KEY,
    city          text NOT NULL REFERENCES ref.city (code),
    place_id      uuid,
    event_id      uuid,
    session_id    uuid,
    model_key     text NOT NULL,
    model_version text NOT NULL,
    embedding     vector(384) NOT NULL,
    content_hash  bytea NOT NULL,
    created_at    timestamptz NOT NULL,
    CHECK (num_nonnulls(place_id, event_id, session_id) = 1),
    FOREIGN KEY (place_id, city) REFERENCES catalog.place (id, city),
    FOREIGN KEY (event_id, city) REFERENCES catalog.event (id, city),
    FOREIGN KEY (session_id, city) REFERENCES catalog.session (id, city)
);
CREATE UNIQUE INDEX entity_embedding_place_uq ON catalog.entity_embedding (place_id, city, model_key, model_version)
    WHERE place_id IS NOT NULL;
CREATE UNIQUE INDEX entity_embedding_event_uq ON catalog.entity_embedding (event_id, city, model_key, model_version)
    WHERE event_id IS NOT NULL;
CREATE UNIQUE INDEX entity_embedding_session_uq ON catalog.entity_embedding (session_id, city, model_key, model_version)
    WHERE session_id IS NOT NULL;
-- Vectors of different models share this index; queries must filter by model_key and model_version.
CREATE INDEX entity_embedding_hnsw ON catalog.entity_embedding USING hnsw (embedding vector_cosine_ops);

CREATE TABLE planning.route (
    id                   uuid PRIMARY KEY,
    owner_id             uuid NOT NULL REFERENCES identity.user_account (id),
    city                 text NOT NULL REFERENCES ref.city (code),
    lifecycle_state      text NOT NULL CHECK (lifecycle_state IN ('draft', 'saved')),
    current_revision     bigint NOT NULL CHECK (current_revision >= 1),
    created_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL,
    draft_expires_at     timestamptz,
    copied_from_route_id uuid,
    copied_from_revision bigint,
    UNIQUE (id, city),
    UNIQUE (id, owner_id),
    CHECK ((copied_from_route_id IS NULL) = (copied_from_revision IS NULL))
);
CREATE INDEX route_owner_updated_idx ON planning.route (owner_id, updated_at DESC);

-- Everything that belongs to a route cascades from it, so deleting the route removes the whole aggregate
-- regardless of the order in which references are processed.
CREATE TABLE planning.route_revision (
    route_id             uuid NOT NULL REFERENCES planning.route (id) ON DELETE CASCADE,
    revision             bigint NOT NULL CHECK (revision >= 1),
    parent_revision      bigint,
    lifecycle_state      text NOT NULL CHECK (lifecycle_state IN ('draft', 'saved')),
    archetype_id         text NOT NULL,
    timezone             text NOT NULL,
    start_at             timestamptz NOT NULL,
    end_at               timestamptz NOT NULL,
    origin               geometry(Point, 4326) NOT NULL,
    destination          geometry(Point, 4326),
    input_schema_version integer NOT NULL,
    constraints          jsonb NOT NULL CHECK (jsonb_typeof(constraints) = 'object'),
    catalog_revision     bigint NOT NULL,
    result_status        text NOT NULL,
    warnings             jsonb NOT NULL,
    cost_summary         jsonb NOT NULL CHECK (jsonb_typeof(cost_summary) = 'object'),
    geometry_geojson     jsonb,
    mutation_kind        text NOT NULL
        CHECK (mutation_kind IN ('create', 'save', 'apply', 'pin', 'delete', 'participation', 'execution')),
    created_at           timestamptz NOT NULL,
    PRIMARY KEY (route_id, revision),
    FOREIGN KEY (route_id, parent_revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    CHECK (end_at > start_at),
    CHECK (parent_revision < revision)
);

-- Deferred so a route and its first revision can be inserted in either order within one transaction.
ALTER TABLE planning.route
    ADD CONSTRAINT route_current_revision_fk FOREIGN KEY (id, current_revision)
        REFERENCES planning.route_revision (route_id, revision) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT route_copied_from_fk FOREIGN KEY (copied_from_route_id, copied_from_revision)
        REFERENCES planning.route_revision (route_id, revision) ON DELETE SET NULL;
CREATE INDEX route_copied_from_idx ON planning.route (copied_from_route_id, copied_from_revision)
    WHERE copied_from_route_id IS NOT NULL;

CREATE TABLE planning.route_visit (
    route_id            uuid NOT NULL,
    visit_id            uuid NOT NULL,
    visit_kind          text NOT NULL CHECK (visit_kind IN ('visit', 'free_time')),
    city                text NOT NULL,
    place_id            uuid,
    entrance_id         uuid,
    event_id            uuid,
    session_id          uuid,
    price_offer_id      uuid,
    created_in_revision bigint NOT NULL,
    created_at          timestamptz NOT NULL,
    PRIMARY KEY (route_id, visit_id),
    FOREIGN KEY (route_id, city) REFERENCES planning.route (id, city) ON DELETE CASCADE,
    FOREIGN KEY (route_id, created_in_revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    FOREIGN KEY (place_id, city) REFERENCES catalog.place (id, city),
    FOREIGN KEY (entrance_id, place_id, city) REFERENCES catalog.place_entrance (id, place_id, city),
    FOREIGN KEY (event_id, place_id, city) REFERENCES catalog.event (id, place_id, city),
    FOREIGN KEY (session_id, event_id, city) REFERENCES catalog.session (id, event_id, city),
    FOREIGN KEY (price_offer_id, session_id, city) REFERENCES catalog.price_offer (id, session_id, city),
    CHECK (visit_kind <> 'free_time' OR num_nonnulls(place_id, entrance_id, event_id, session_id, price_offer_id) = 0),
    CHECK (visit_kind <> 'visit' OR place_id IS NOT NULL),
    CHECK (entrance_id IS NULL OR place_id IS NOT NULL),
    CHECK (session_id IS NULL OR event_id IS NOT NULL),
    CHECK (price_offer_id IS NULL OR session_id IS NOT NULL)
);
CREATE INDEX route_visit_session_idx ON planning.route_visit (city, session_id, route_id) WHERE session_id IS NOT NULL;
CREATE INDEX route_visit_place_idx ON planning.route_visit (city, place_id) WHERE place_id IS NOT NULL;
CREATE INDEX route_visit_entrance_idx ON planning.route_visit (city, entrance_id) WHERE entrance_id IS NOT NULL;
CREATE INDEX route_visit_event_idx ON planning.route_visit (city, event_id) WHERE event_id IS NOT NULL;
CREATE INDEX route_visit_price_offer_idx ON planning.route_visit (city, price_offer_id) WHERE price_offer_id IS NOT NULL;

CREATE TABLE planning.route_step (
    route_id               uuid NOT NULL,
    revision               bigint NOT NULL,
    position               integer NOT NULL CHECK (position > 0),
    visit_id               uuid NOT NULL,
    arrival_at             timestamptz NOT NULL,
    visit_start_at         timestamptz NOT NULL,
    visit_end_at           timestamptz NOT NULL,
    departure_at           timestamptz NOT NULL,
    min_duration_s         integer NOT NULL CHECK (min_duration_s >= 0),
    is_pinned              boolean NOT NULL,
    is_obligation          boolean NOT NULL,
    participation_snapshot jsonb NOT NULL,
    catalog_snapshot       jsonb NOT NULL,
    cost_snapshot          jsonb NOT NULL,
    applied_constraints    jsonb NOT NULL,
    PRIMARY KEY (route_id, revision, position),
    UNIQUE (route_id, revision, visit_id),
    FOREIGN KEY (route_id, revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    FOREIGN KEY (route_id, visit_id) REFERENCES planning.route_visit (route_id, visit_id) ON DELETE CASCADE,
    CHECK (arrival_at <= visit_start_at AND visit_start_at < visit_end_at AND visit_end_at <= departure_at),
    CHECK (visit_end_at - visit_start_at >= make_interval(secs => min_duration_s))
);
CREATE INDEX route_step_visit_idx ON planning.route_step (route_id, visit_id);

CREATE TABLE planning.route_leg (
    route_id            uuid NOT NULL,
    revision            bigint NOT NULL,
    position            integer NOT NULL,
    from_kind           text NOT NULL CHECK (from_kind IN ('origin', 'visit')),
    to_kind             text NOT NULL CHECK (to_kind IN ('visit', 'destination')),
    from_visit_id       uuid,
    to_visit_id         uuid,
    departure_at        timestamptz NOT NULL,
    arrival_at          timestamptz NOT NULL,
    mode                text NOT NULL,
    distance_m          numeric(12, 2) CHECK (distance_m >= 0),
    geometry            geometry(LineString, 4326),
    verification_status text NOT NULL CHECK (verification_status IN ('verified', 'estimated', 'unknown', 'unavailable')),
    evidence            jsonb NOT NULL,
    cost_snapshot       jsonb NOT NULL,
    PRIMARY KEY (route_id, revision, position),
    FOREIGN KEY (route_id, revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    FOREIGN KEY (route_id, revision, from_visit_id)
        REFERENCES planning.route_step (route_id, revision, visit_id) ON DELETE CASCADE,
    FOREIGN KEY (route_id, revision, to_visit_id)
        REFERENCES planning.route_step (route_id, revision, visit_id) ON DELETE CASCADE,
    CHECK (arrival_at >= departure_at),
    CHECK ((from_kind = 'origin') = (from_visit_id IS NULL)),
    CHECK ((to_kind = 'destination') = (to_visit_id IS NULL))
);

CREATE TABLE planning.participation (
    route_id                uuid NOT NULL,
    visit_id                uuid NOT NULL,
    status                  text NOT NULL CHECK (status IN (
        'not_required', 'action_required', 'user_reported_confirmed', 'provider_confirmed', 'unavailable')),
    evidence_source         text NOT NULL CHECK (evidence_source IN ('none', 'user', 'provider')),
    provider_record_id      uuid REFERENCES integration.source_record (id),
    private_reference       text,
    external_link_opened_at timestamptz,
    updated_in_revision     bigint NOT NULL,
    updated_at              timestamptz NOT NULL,
    PRIMARY KEY (route_id, visit_id),
    FOREIGN KEY (route_id, visit_id) REFERENCES planning.route_visit (route_id, visit_id) ON DELETE CASCADE,
    FOREIGN KEY (route_id, updated_in_revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    -- Following an external link is not a confirmation; only a provider record is.
    CONSTRAINT participation_provider_confirmed_has_record CHECK (
        status <> 'provider_confirmed' OR (evidence_source = 'provider' AND provider_record_id IS NOT NULL)
    )
);

CREATE TABLE planning.execution (
    route_id            uuid NOT NULL,
    visit_id            uuid NOT NULL,
    status              text NOT NULL CHECK (status IN ('planned', 'completed', 'skipped')),
    actual_started_at   timestamptz,
    actual_ended_at     timestamptz,
    confirmation_kind   text NOT NULL CHECK (confirmation_kind IN ('user_reported', 'provider_confirmed')),
    updated_in_revision bigint NOT NULL,
    updated_at          timestamptz NOT NULL,
    PRIMARY KEY (route_id, visit_id),
    FOREIGN KEY (route_id, visit_id) REFERENCES planning.route_visit (route_id, visit_id) ON DELETE CASCADE,
    FOREIGN KEY (route_id, updated_in_revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    CHECK (actual_ended_at >= actual_started_at)
);

CREATE TABLE planning.route_proposal (
    id                       uuid PRIMARY KEY,
    route_id                 uuid NOT NULL REFERENCES planning.route (id) ON DELETE CASCADE,
    base_revision            bigint NOT NULL,
    base_catalog_revision    bigint NOT NULL,
    reason                   text NOT NULL CHECK (reason IN ('delay', 'cancel', 'delete', 'pin', 'weather')),
    state                    text NOT NULL CHECK (state IN ('pending', 'applied', 'rejected', 'invalidated')),
    candidate_schema_version integer NOT NULL,
    candidate                jsonb NOT NULL,
    changes                  jsonb NOT NULL,
    conflicts                jsonb NOT NULL,
    effective_start_at       timestamptz,
    created_at               timestamptz NOT NULL,
    resolved_at              timestamptz,
    applied_revision         bigint,
    FOREIGN KEY (route_id, base_revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    FOREIGN KEY (route_id, applied_revision) REFERENCES planning.route_revision (route_id, revision) ON DELETE CASCADE,
    CHECK ((state = 'applied') = (applied_revision IS NOT NULL)),
    CHECK ((state = 'pending') = (resolved_at IS NULL))
);
CREATE INDEX route_proposal_route_state_idx ON planning.route_proposal (route_id, state, created_at DESC);
CREATE UNIQUE INDEX route_proposal_one_pending_uq ON planning.route_proposal (route_id) WHERE state = 'pending';

CREATE TABLE planning.route_issue (
    id               uuid PRIMARY KEY,
    route_id         uuid NOT NULL REFERENCES planning.route (id) ON DELETE CASCADE,
    visit_id         uuid,
    source_change_id uuid,
    issue_type       text NOT NULL CHECK (issue_type IN ('cancelled', 'unreachable', 'stale', 'unknown')),
    details          jsonb NOT NULL,
    state            text NOT NULL CHECK (state IN ('open', 'acknowledged', 'resolved')),
    catalog_revision bigint,
    created_at       timestamptz NOT NULL,
    resolved_at      timestamptz,
    FOREIGN KEY (route_id, visit_id) REFERENCES planning.route_visit (route_id, visit_id) ON DELETE CASCADE
);
CREATE INDEX route_issue_unresolved_idx ON planning.route_issue (route_id, state) WHERE state <> 'resolved';
CREATE UNIQUE INDEX route_issue_change_uq ON planning.route_issue
    (source_change_id, route_id, visit_id, issue_type) NULLS NOT DISTINCT
    WHERE source_change_id IS NOT NULL;

CREATE TABLE planning.route_share (
    id         uuid PRIMARY KEY,
    route_id   uuid NOT NULL REFERENCES planning.route (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL,
    revoked_at timestamptz,
    expires_at timestamptz,
    created_by uuid NOT NULL,
    FOREIGN KEY (route_id, created_by) REFERENCES planning.route (id, owner_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX route_share_one_active_uq ON planning.route_share (route_id) WHERE revoked_at IS NULL;

CREATE TABLE gateway_ops.command_result (
    actor_id           uuid NOT NULL REFERENCES identity.user_account (id),
    operation          text NOT NULL,
    idempotency_key    text NOT NULL,
    request_hash       bytea NOT NULL,
    route_id           uuid,
    resulting_revision bigint,
    http_status        integer NOT NULL,
    response_body      jsonb NOT NULL,
    created_at         timestamptz NOT NULL,
    PRIMARY KEY (actor_id, operation, idempotency_key)
);

CREATE TABLE gateway_ops.inbox (
    producer     text NOT NULL,
    event_id     text NOT NULL,
    payload_hash bytea NOT NULL,
    state        text NOT NULL CHECK (state IN ('received', 'processed', 'failed')),
    result_ref   jsonb,
    received_at  timestamptz NOT NULL,
    processed_at timestamptz,
    PRIMARY KEY (producer, event_id)
);
CREATE INDEX inbox_state_received_idx ON gateway_ops.inbox (state, received_at);

CREATE TABLE gateway_ops.conversation (
    user_id            uuid NOT NULL REFERENCES identity.user_account (id),
    conversation_key   text NOT NULL,
    step               text NOT NULL,
    state_version      bigint NOT NULL,
    confirmed_input    jsonb NOT NULL,
    pending_extraction jsonb,
    selected_route_id  uuid,
    updated_at         timestamptz NOT NULL,
    expires_at         timestamptz,
    PRIMARY KEY (user_id, conversation_key),
    FOREIGN KEY (selected_route_id, user_id) REFERENCES planning.route (id, owner_id)
        ON DELETE SET NULL (selected_route_id)
);
CREATE INDEX conversation_selected_route_idx ON gateway_ops.conversation (selected_route_id, user_id)
    WHERE selected_route_id IS NOT NULL;

CREATE TABLE gateway_ops.notification (
    id              uuid PRIMARY KEY,
    route_id        uuid NOT NULL REFERENCES planning.route (id) ON DELETE CASCADE,
    change_id       uuid NOT NULL,
    recipient_id    uuid NOT NULL REFERENCES identity.user_account (id),
    state           text NOT NULL CHECK (state IN ('pending', 'sent', 'failed', 'suppressed')),
    attempts        integer NOT NULL CHECK (attempts >= 0),
    payload         jsonb NOT NULL,
    next_attempt_at timestamptz,
    sent_at         timestamptz,
    UNIQUE (route_id, change_id, recipient_id)
);
CREATE INDEX notification_state_next_attempt_idx ON gateway_ops.notification (state, next_attempt_at);

CREATE TABLE gateway_ops.product_event (
    id          uuid PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
    event_type  text NOT NULL,
    city        text,
    flow_id     uuid NOT NULL,
    route_id    uuid REFERENCES planning.route (id) ON DELETE SET NULL,
    attributes  jsonb NOT NULL
);
CREATE INDEX product_event_type_occurred_idx ON gateway_ops.product_event (event_type, occurred_at);
CREATE INDEX product_event_flow_occurred_idx ON gateway_ops.product_event (flow_id, occurred_at);
CREATE INDEX product_event_route_idx ON gateway_ops.product_event (route_id) WHERE route_id IS NOT NULL;

-- Gateway must not be able to update cities, yet it needs to hold the city row while it commits a route,
-- so the share lock is taken on its behalf.
CREATE FUNCTION ref.lock_city_shared(p_city text) RETURNS bigint
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
    v_catalog_revision bigint;
BEGIN
    SELECT catalog_revision INTO v_catalog_revision FROM ref.city WHERE code = p_city FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'unknown city %', p_city USING ERRCODE = 'no_data_found';
    END IF;
    RETURN v_catalog_revision;
END $$;

-- Gateway has no DELETE on planning; this is the only way to remove a route, and it removes the whole aggregate.
-- It does not decide which routes should be deleted.
CREATE FUNCTION planning.purge_route(p_route_id uuid, p_owner_id uuid) RETURNS boolean
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    PERFORM 1 FROM planning.route WHERE id = p_route_id AND owner_id = p_owner_id FOR UPDATE;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    DELETE FROM planning.route WHERE id = p_route_id;
    RETURN true;
END $$;

REVOKE ALL ON FUNCTION ref.lock_city_shared(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION planning.purge_route(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION ref.lock_city_shared(text) TO impuls_gateway;
GRANT EXECUTE ON FUNCTION planning.purge_route(uuid, uuid) TO impuls_gateway;

GRANT USAGE ON SCHEMA ref, catalog TO impuls_gateway, impuls_syncer, impuls_optimizer;
GRANT USAGE ON SCHEMA integration TO impuls_syncer;
GRANT USAGE ON SCHEMA identity, planning, gateway_ops TO impuls_gateway;

GRANT SELECT ON ALL TABLES IN SCHEMA ref, catalog TO impuls_gateway, impuls_syncer, impuls_optimizer;
GRANT INSERT, UPDATE ON ref.city TO impuls_syncer;
GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA catalog TO impuls_syncer;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA integration TO impuls_syncer;

GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA identity TO impuls_gateway;
GRANT DELETE ON identity.auth_session TO impuls_gateway;
GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA planning TO impuls_gateway;
GRANT UPDATE ON planning.route, planning.participation, planning.execution,
    planning.route_proposal, planning.route_issue, planning.route_share TO impuls_gateway;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA gateway_ops TO impuls_gateway;

INSERT INTO ref.city (code, timezone, updated_at) VALUES
    ('moscow', 'Europe/Moscow', now()),
    ('perm', 'Asia/Yekaterinburg', now());

-- Jury account; its session token is issued by the gateway CLI and never stored here.
INSERT INTO identity.user_account (id, max_user_id, created_at, last_seen_at, account_state, account_kind)
VALUES ('00000000-0000-4000-8000-000000000001', 'test:jury', now(), now(), 'active', 'test');

COMMIT;
