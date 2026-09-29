BEGIN;

CREATE TABLE catalog.entity_enrichment (
    id           uuid PRIMARY KEY,
    city         text NOT NULL REFERENCES ref.city (code),
    place_id     uuid,
    event_id     uuid,
    model        text NOT NULL,
    input_hash   bytea NOT NULL,
    llm_tag_mask bit(64) NOT NULL,
    enriched_at  timestamptz NOT NULL,
    CHECK (num_nonnulls(place_id, event_id) = 1),
    FOREIGN KEY (place_id, city) REFERENCES catalog.place (id, city),
    FOREIGN KEY (event_id, city) REFERENCES catalog.event (id, city)
);
CREATE UNIQUE INDEX entity_enrichment_place_uq ON catalog.entity_enrichment (place_id, city)
    WHERE place_id IS NOT NULL;
CREATE UNIQUE INDEX entity_enrichment_event_uq ON catalog.entity_enrichment (event_id, city)
    WHERE event_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON catalog.entity_enrichment TO impuls_syncer;

COMMIT;
