BEGIN;

ALTER TABLE planning.route_visit
    DROP CONSTRAINT route_visit_visit_kind_check,
    ADD CONSTRAINT route_visit_visit_kind_check
        CHECK (visit_kind IN ('visit', 'free_time', 'lunch')),
    ADD CONSTRAINT route_visit_lunch_neutral_check
        CHECK (visit_kind <> 'lunch' OR num_nonnulls(place_id, entrance_id, event_id, session_id, price_offer_id) = 0);

ALTER TABLE planning.route_proposal
    DROP CONSTRAINT route_proposal_reason_check,
    ADD CONSTRAINT route_proposal_reason_check
        CHECK (reason IN ('delay', 'cancel', 'delete', 'pin', 'weather', 'lunch'));

CREATE TABLE planning.route_lunch_step (
    route_id           uuid NOT NULL,
    revision           bigint NOT NULL,
    visit_id           uuid NOT NULL,
    after_visit_id     uuid NOT NULL,
    duration_seconds   integer NOT NULL CHECK (duration_seconds BETWEEN 2700 AND 3600),
    venue_kind         text NOT NULL CHECK (venue_kind IN ('free_time', 'catalog', 'external')),
    city               text NOT NULL,
    place_id           uuid,
    entrance_id        uuid,
    event_id           uuid,
    session_id         uuid,
    price_offer_id     uuid,
    external_snapshot  jsonb,
    PRIMARY KEY (route_id, revision, visit_id),
    FOREIGN KEY (route_id, city) REFERENCES planning.route (id, city) ON DELETE CASCADE,
    FOREIGN KEY (route_id, revision, visit_id)
        REFERENCES planning.route_step (route_id, revision, visit_id) ON DELETE CASCADE,
    CONSTRAINT route_lunch_step_anchor_fk FOREIGN KEY (route_id, revision, after_visit_id)
        REFERENCES planning.route_step (route_id, revision, visit_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (place_id, city) REFERENCES catalog.place (id, city),
    FOREIGN KEY (entrance_id, place_id, city) REFERENCES catalog.place_entrance (id, place_id, city),
    FOREIGN KEY (event_id, place_id, city) REFERENCES catalog.event (id, place_id, city),
    FOREIGN KEY (session_id, event_id, city) REFERENCES catalog.session (id, event_id, city),
    FOREIGN KEY (price_offer_id, session_id, city) REFERENCES catalog.price_offer (id, session_id, city),
    CHECK (visit_id <> after_visit_id),
    CHECK (entrance_id IS NULL OR place_id IS NOT NULL),
    CHECK (session_id IS NULL OR event_id IS NOT NULL),
    CHECK (price_offer_id IS NULL OR session_id IS NOT NULL),
    CHECK (
        (venue_kind = 'catalog' AND place_id IS NOT NULL AND external_snapshot IS NULL)
        OR (venue_kind = 'external' AND num_nonnulls(place_id, entrance_id, event_id, session_id, price_offer_id) = 0
            AND external_snapshot IS NOT NULL)
        OR (venue_kind = 'free_time' AND num_nonnulls(place_id, entrance_id, event_id, session_id, price_offer_id) = 0
            AND external_snapshot IS NULL)
    ),
    CHECK (external_snapshot IS NULL OR (
        (jsonb_typeof(external_snapshot) = 'object') IS TRUE
        AND (external_snapshot->>'provider' = '2gis') IS TRUE
        AND coalesce(length(external_snapshot->>'external_id'), 0) BETWEEN 1 AND 128
        AND coalesce(length(external_snapshot->>'title'), 0) BETWEEN 1 AND 500
        AND (jsonb_typeof(external_snapshot->'position') = 'object') IS TRUE
        AND (jsonb_typeof(external_snapshot->'observed_at') = 'string') IS TRUE
        AND (jsonb_typeof(external_snapshot->'price') = 'object') IS TRUE
        AND (external_snapshot->'price'->>'status' = 'unknown') IS TRUE
        AND (NOT (external_snapshot->'price' ?| ARRAY['lower_minor', 'upper_minor'])) IS TRUE
        AND (external_snapshot->>'availability' = 'unknown') IS TRUE
        AND (external_snapshot->>'hours_verification' = 'unknown') IS TRUE
    ))
);

CREATE INDEX route_lunch_step_anchor_idx
    ON planning.route_lunch_step (route_id, revision, after_visit_id);
CREATE INDEX route_lunch_step_catalog_idx
    ON planning.route_lunch_step (city, place_id, route_id) WHERE place_id IS NOT NULL;
CREATE INDEX route_lunch_step_session_idx
    ON planning.route_lunch_step (city, session_id, route_id) WHERE session_id IS NOT NULL;

GRANT SELECT, INSERT ON planning.route_lunch_step TO impuls_gateway;

COMMIT;
