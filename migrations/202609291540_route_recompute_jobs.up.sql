BEGIN;

CREATE TABLE gateway_ops.route_recompute_job (
    id uuid PRIMARY KEY,
    route_id uuid NOT NULL,
    city text NOT NULL,
    change_id uuid NOT NULL,
    min_catalog_revision bigint NOT NULL CHECK (min_catalog_revision > 0),
    state text NOT NULL CHECK (state IN ('pending', 'running', 'done')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL,
    lease_token uuid,
    lease_until timestamptz,
    result jsonb,
    last_error_code text,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    FOREIGN KEY (route_id, city) REFERENCES planning.route(id, city) ON DELETE CASCADE,
    UNIQUE (route_id, change_id),
    CHECK ((state = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (state = 'running' OR (lease_token IS NULL AND lease_until IS NULL)),
    CHECK ((state = 'done') = (completed_at IS NOT NULL)),
    CHECK (state <> 'done' OR result IS NOT NULL)
);

CREATE INDEX route_recompute_job_due_idx
    ON gateway_ops.route_recompute_job(next_attempt_at, created_at) WHERE state <> 'done';
CREATE UNIQUE INDEX route_recompute_job_running_idx
    ON gateway_ops.route_recompute_job(route_id) WHERE state = 'running';

GRANT SELECT, INSERT, UPDATE ON gateway_ops.route_recompute_job TO impuls_gateway;

COMMIT;
