BEGIN;

CREATE TABLE gateway_ops.route_notification_preference (
    route_id uuid PRIMARY KEY,
    owner_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    version bigint NOT NULL CHECK (version > 0),
    changed_at timestamptz NOT NULL,
    FOREIGN KEY (route_id, owner_id) REFERENCES planning.route(id, owner_id) ON DELETE CASCADE
);

CREATE TABLE gateway_ops.max_delivery_state (
    user_account_id uuid PRIMARY KEY REFERENCES identity.user_account(id) ON DELETE CASCADE,
    stopped boolean NOT NULL DEFAULT false,
    stopped_observed_at_ms bigint CHECK (stopped_observed_at_ms > 0),
    muted boolean NOT NULL DEFAULT false,
    muted_observed_at_ms bigint CHECK (muted_observed_at_ms > 0),
    CHECK (stopped_observed_at_ms IS NOT NULL OR NOT stopped),
    CHECK (muted_observed_at_ms IS NOT NULL OR NOT muted)
);

ALTER TABLE gateway_ops.notification
    ADD COLUMN lease_token uuid,
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN last_attempt_at timestamptz,
    ADD COLUMN last_error_code text,
    ADD CONSTRAINT notification_lease_pair CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    ADD CONSTRAINT notification_lease_state CHECK (lease_token IS NULL OR state IN ('pending', 'failed')),
    ADD CONSTRAINT notification_error_code CHECK (last_error_code IS NULL OR
        (length(last_error_code) <= 64 AND last_error_code ~ '^[A-Z][A-Z0-9_]*$'));

GRANT SELECT, INSERT, UPDATE ON gateway_ops.route_notification_preference,
    gateway_ops.max_delivery_state TO impuls_gateway;

CREATE UNIQUE INDEX notification_recipient_lease_idx ON gateway_ops.notification(recipient_id)
    WHERE lease_token IS NOT NULL;
CREATE INDEX notification_recipient_attempt_idx ON gateway_ops.notification(recipient_id, last_attempt_at);

COMMIT;
