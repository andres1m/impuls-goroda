package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/jackc/pgx/v5/pgtype"
)

func (q *Queries) ReadScenarioContext(ctx context.Context, actor d.UserID) (routewire.ScenarioContext, error) {
	if actor == (d.UserID{}) {
		return routewire.ScenarioContext{}, errors.New("context owner is required")
	}
	var raw []byte
	var updated time.Time
	var routeID pgtype.UUID
	var revision pgtype.Int8
	err := q.db.QueryRow(ctx, `SELECT c.confirmed_input,c.updated_at,r.id,r.current_revision
FROM gateway_ops.conversation c
LEFT JOIN planning.route r ON r.id=c.selected_route_id AND r.owner_id=c.user_id
WHERE c.user_id=$1 AND c.confirmed_input<>'{}'::jsonb AND c.confirmed_input<>'null'::jsonb
AND (c.expires_at IS NULL OR c.expires_at>statement_timestamp())
ORDER BY c.updated_at DESC,c.conversation_key DESC LIMIT 1`, encodeUUID([16]byte(actor))).Scan(&raw, &updated, &routeID, &revision)
	if err != nil {
		mapped := mapQueryError("read scenario context", err)
		if errors.Is(mapped, ErrNotFound) {
			return routewire.ScenarioContext{}, nil
		}
		return routewire.ScenarioContext{}, mapped
	}
	input, err := routewire.DecodeInput(raw)
	if err != nil || updated.IsZero() {
		return routewire.ScenarioContext{}, errors.New("stored scenario context is invalid")
	}
	out := routewire.ScenarioContext{ConfirmedInput: &input, UpdatedAt: &updated}
	if routeID.Valid {
		id, err := decodeUUID(routeID)
		if err != nil || !revision.Valid || d.RouteRevisionNumber(revision.Int64).Validate() != nil {
			return routewire.ScenarioContext{}, errors.New("stored scenario selection is invalid")
		}
		selected, version := formatUUID(id), strconv.FormatInt(revision.Int64, 10)
		out.SelectedRouteID, out.SelectedRevision = &selected, &version
	}
	return out, nil
}

func (q *Queries) SelectScenarioRoute(ctx context.Context, access RouteAccess) error {
	var raw []byte
	err := q.db.QueryRow(ctx, `SELECT jsonb_strip_nulls(jsonb_build_object(
'city',r.city,'timezone',v.timezone,'start_at',v.start_at,'end_at',v.end_at,
'origin',jsonb_build_object('longitude',ST_X(v.origin),'latitude',ST_Y(v.origin)),
'destination',CASE WHEN v.destination IS NULL THEN NULL ELSE jsonb_build_object('longitude',ST_X(v.destination),'latitude',ST_Y(v.destination)) END,
'constraints',v.constraints))
FROM planning.route r JOIN planning.route_revision v ON v.route_id=r.id AND v.revision=r.current_revision
WHERE r.id=$1 AND r.owner_id=$2 AND r.current_revision=$3`, encodeUUID([16]byte(access.RouteID)), encodeUUID([16]byte(access.OwnerID)), int64(access.Revision)).Scan(&raw)
	if err != nil {
		return mapQueryError("read selected scenario input", err)
	}
	if _, err := routewire.DecodeInput(raw); err != nil {
		return errors.New("selected scenario input is invalid")
	}
	_, err = q.db.Exec(ctx, `INSERT INTO gateway_ops.conversation
(user_id,conversation_key,step,state_version,confirmed_input,pending_extraction,selected_route_id,updated_at,expires_at)
VALUES ($1,'miniapp','selected',1,$2,NULL,$3,clock_timestamp(),NULL)
ON CONFLICT (user_id,conversation_key) DO UPDATE SET
step='selected',state_version=gateway_ops.conversation.state_version+1,
confirmed_input=EXCLUDED.confirmed_input,pending_extraction=NULL,selected_route_id=EXCLUDED.selected_route_id,
updated_at=clock_timestamp(),expires_at=NULL`, encodeUUID([16]byte(access.OwnerID)), raw, encodeUUID([16]byte(access.RouteID)))
	if err != nil {
		return mapQueryError("select scenario route", err)
	}
	return nil
}
