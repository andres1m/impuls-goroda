package postgres

import (
	"context"
	"errors"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SharedCopySource is the private snapshot behind a valid share capability.
// Only the server uses its route ID; the anonymous projection never exposes it.
type SharedCopySource struct {
	RouteID  d.RouteID
	Revision d.RouteRevisionNumber
	City     string
	Plan     d.RoutePlanSnapshot
}

func (q *Queries) SetCopyOrigin(ctx context.Context, newRouteID d.RouteID, source SharedCopySource) error {
	if newRouteID == (d.RouteID{}) || source.RouteID == (d.RouteID{}) || source.Revision < 1 {
		return errors.New("invalid copy origin")
	}
	_, err := q.db.Exec(ctx, `UPDATE planning.route SET copied_from_route_id=$2,copied_from_revision=$3 WHERE id=$1`,
		encodeUUID([16]byte(newRouteID)), encodeUUID([16]byte(source.RouteID)), int64(source.Revision))
	return err
}

// ReadSharedCopySource optionally locks the capability until the caller's transaction commits.
// Locking is required before creating a copy so revoke and copy cannot pass each other unseen.
func (q *Queries) ReadSharedCopySource(ctx context.Context, hash [32]byte, lock bool) (SharedCopySource, error) {
	if _, ok := q.db.(pgx.Tx); !ok {
		return SharedCopySource{}, errors.New("shared copy source requires a transaction")
	}
	statement := `SELECT r.id,r.owner_id,r.city,r.lifecycle_state,r.current_revision
FROM planning.route_share s JOIN planning.route r ON r.id=s.route_id
WHERE s.token_hash=$1 AND s.revoked_at IS NULL
AND (s.expires_at IS NULL OR s.expires_at>transaction_timestamp())
AND (r.draft_expires_at IS NULL OR r.draft_expires_at>transaction_timestamp())`
	if lock {
		statement += ` FOR SHARE OF s,r`
	}
	var id, owner pgtype.UUID
	var city string
	var lifecycle d.RouteLifecycle
	var revision d.RouteRevisionNumber
	if err := q.db.QueryRow(ctx, statement, hash[:]).Scan(&id, &owner, &city, &lifecycle, &revision); err != nil {
		return SharedCopySource{}, mapQueryError("read shared copy source", err)
	}
	routeID, err := decodeUUID(id)
	if err != nil {
		return SharedCopySource{}, err
	}
	ownerID, err := decodeUUID(owner)
	if err != nil {
		return SharedCopySource{}, err
	}
	state, err := q.ReadRecomputeState(ctx, d.RouteID(routeID), d.UserID(ownerID))
	if err != nil {
		return SharedCopySource{}, err
	}
	if state.Access.Revision != revision || state.Access.Lifecycle != lifecycle || state.City != city ||
		(state.Plan.Result != d.ResultReady && state.Plan.Result != d.ResultPartial) {
		return SharedCopySource{}, errors.New("shared copy source is inconsistent")
	}
	return SharedCopySource{RouteID: d.RouteID(routeID), Revision: revision, City: city, Plan: state.Plan}, nil
}
