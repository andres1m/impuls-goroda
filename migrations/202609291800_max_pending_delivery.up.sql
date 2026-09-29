BEGIN;

CREATE TABLE gateway_ops.max_pending_delivery_state (
    max_user_id bigint PRIMARY KEY CHECK (max_user_id > 0),
    stopped boolean NOT NULL DEFAULT false,
    stopped_observed_at_ms bigint CHECK (stopped_observed_at_ms > 0),
    muted boolean NOT NULL DEFAULT false,
    muted_observed_at_ms bigint CHECK (muted_observed_at_ms > 0),
    CHECK (stopped_observed_at_ms IS NOT NULL OR NOT stopped),
    CHECK (muted_observed_at_ms IS NOT NULL OR NOT muted)
);

GRANT SELECT, INSERT, UPDATE ON gateway_ops.max_pending_delivery_state TO impuls_gateway;

COMMIT;
