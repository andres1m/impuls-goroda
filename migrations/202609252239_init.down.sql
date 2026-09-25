BEGIN;

DROP SCHEMA IF EXISTS gateway_ops, planning, identity, integration, catalog, ref CASCADE;

-- Roles are cluster-wide: keep them while another database on the same server still grants them privileges.
DO $$
DECLARE
    role_name text;
BEGIN
    FOREACH role_name IN ARRAY ARRAY['impuls_gateway', 'impuls_syncer', 'impuls_optimizer'] LOOP
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = role_name)
            AND NOT EXISTS (
                SELECT 1
                FROM pg_shdepend d
                JOIN pg_roles r ON r.oid = d.refobjid AND d.refclassid = 'pg_authid'::regclass
                WHERE r.rolname = role_name
                  AND d.dbid <> (SELECT oid FROM pg_database WHERE datname = current_database())
            ) THEN
            EXECUTE format('DROP ROLE %I', role_name);
        END IF;
    END LOOP;
END $$;

DROP EXTENSION IF EXISTS vector, pg_trgm, postgis;

COMMIT;
