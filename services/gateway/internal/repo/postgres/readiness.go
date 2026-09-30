package postgres

import "context"

type RouteStorageReadiness struct {
	Readable bool
	Writable bool
}

func (q *Queries) RouteStorageReadiness(ctx context.Context) (RouteStorageReadiness, error) {
	var result RouteStorageReadiness
	err := q.db.QueryRow(ctx, `WITH required_tables(name, insertable, updatable) AS (VALUES
('identity.user_account', false, false), ('identity.auth_session', false, false),
('planning.route', true, true), ('planning.route_revision', true, false),
('planning.route_visit', true, false), ('planning.route_step', true, false),
('planning.route_lunch_step', true, false),
('planning.route_leg', true, false), ('planning.participation', true, true),
('planning.execution', true, true), ('planning.route_issue', true, true),
('planning.route_proposal', true, true), ('planning.route_share', true, true),
('gateway_ops.command_result', true, true))
SELECT bool_and(COALESCE(has_schema_privilege(c.relnamespace, 'USAGE')
AND has_table_privilege(c.oid, 'SELECT'), false)),
NOT pg_is_in_recovery() AND current_setting('transaction_read_only') = 'off'
AND bool_and(COALESCE(has_schema_privilege(c.relnamespace, 'USAGE')
AND (NOT t.insertable OR has_table_privilege(c.oid, 'INSERT'))
AND (NOT t.updatable OR has_table_privilege(c.oid, 'UPDATE')), false))
FROM required_tables t LEFT JOIN pg_class c ON c.oid = to_regclass(t.name)`).Scan(&result.Readable, &result.Writable)
	return result, err
}
