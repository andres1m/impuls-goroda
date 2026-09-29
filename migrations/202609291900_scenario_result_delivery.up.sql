BEGIN;

CREATE TABLE gateway_ops.scenario_result_delivery (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL,
    conversation_key text NOT NULL CHECK (conversation_key ~ '^scenario:[0-9a-f-]{36}$'),
    scenario_version bigint NOT NULL CHECK (scenario_version > 0),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sent', 'failed', 'suppressed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    next_attempt_at timestamptz,
    lease_token uuid,
    lease_until timestamptz,
    created_at timestamptz NOT NULL,
    sent_at timestamptz,
    FOREIGN KEY (owner_id, conversation_key) REFERENCES gateway_ops.conversation(user_id, conversation_key) ON DELETE CASCADE,
    UNIQUE (owner_id, conversation_key, scenario_version),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (lease_token IS NULL OR state = 'pending'),
    CHECK ((state = 'pending') = (next_attempt_at IS NOT NULL)),
    CHECK ((state = 'sent') = (sent_at IS NOT NULL))
);

CREATE INDEX scenario_result_delivery_due_idx ON gateway_ops.scenario_result_delivery(next_attempt_at, id)
    WHERE state = 'pending';

GRANT SELECT, INSERT, UPDATE ON gateway_ops.scenario_result_delivery TO impuls_gateway;

COMMIT;
