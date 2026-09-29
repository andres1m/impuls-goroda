BEGIN;

DROP INDEX gateway_ops.notification_recipient_attempt_idx;
DROP INDEX gateway_ops.notification_recipient_lease_idx;

ALTER TABLE gateway_ops.notification
    DROP CONSTRAINT notification_error_code,
    DROP CONSTRAINT notification_lease_state,
    DROP CONSTRAINT notification_lease_pair,
    DROP COLUMN last_error_code,
    DROP COLUMN last_attempt_at,
    DROP COLUMN lease_until,
    DROP COLUMN lease_token;

DROP TABLE gateway_ops.max_delivery_state;
DROP TABLE gateway_ops.route_notification_preference;

COMMIT;
