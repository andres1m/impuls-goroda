#!/bin/sh
# Login users for the services; privileges come from the group roles created by migrations.
# Safe to rerun: passwords are reset to the current values.
set -eu

export PGPASSWORD="$POSTGRES_PASSWORD"

psql -v ON_ERROR_STOP=1 --host postgres --username "$POSTGRES_USER" --dbname impuls \
    -v gateway_password="$GATEWAY_DB_PASSWORD" \
    -v optimizer_password="$OPTIMIZER_DB_PASSWORD" \
    -v syncer_password="$SYNCER_DB_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN IN ROLE %I', login_name, group_name)
FROM (VALUES ('gateway_svc', 'impuls_gateway'),
             ('optimizer_svc', 'impuls_optimizer'),
             ('syncer_svc', 'impuls_syncer')) AS users (login_name, group_name)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = login_name)
\gexec

ALTER ROLE gateway_svc PASSWORD :'gateway_password';
ALTER ROLE optimizer_svc PASSWORD :'optimizer_password';
ALTER ROLE syncer_svc PASSWORD :'syncer_password';
SQL
