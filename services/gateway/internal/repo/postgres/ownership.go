package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5/pgtype"
)

const findRouteAccessSQL = `
SELECT id, owner_id, lifecycle_state, current_revision
FROM planning.route
WHERE id = $1 AND owner_id = $2`

type RouteAccess struct {
	RouteID   domain.RouteID
	OwnerID   domain.UserID
	Lifecycle domain.RouteLifecycle
	Revision  domain.RouteRevisionNumber
}

func (q *Queries) FindRouteAccess(
	ctx context.Context,
	routeID domain.RouteID,
	userID domain.UserID,
) (RouteAccess, error) {
	if routeID == (domain.RouteID{}) || userID == (domain.UserID{}) {
		return RouteAccess{}, errors.New("route and user identifiers are required")
	}

	var routeUUID, ownerUUID pgtype.UUID
	var access RouteAccess
	err := q.db.QueryRow(
		ctx,
		findRouteAccessSQL,
		encodeUUID([16]byte(routeID)),
		encodeUUID([16]byte(userID)),
	).Scan(&routeUUID, &ownerUUID, &access.Lifecycle, &access.Revision)
	if err != nil {
		return RouteAccess{}, mapQueryError("find route access", err)
	}

	decodedRouteID, err := decodeUUID(routeUUID)
	if err != nil {
		return RouteAccess{}, fmt.Errorf("decode route access: %w", err)
	}
	decodedOwnerID, err := decodeUUID(ownerUUID)
	if err != nil {
		return RouteAccess{}, fmt.Errorf("decode route access: %w", err)
	}
	access.RouteID = domain.RouteID(decodedRouteID)
	access.OwnerID = domain.UserID(decodedOwnerID)
	if access.RouteID != routeID || access.OwnerID != userID {
		return RouteAccess{}, errors.New("stored route access disagrees with lookup")
	}
	if access.Lifecycle != domain.RouteDraft && access.Lifecycle != domain.RouteSaved {
		return RouteAccess{}, errors.New("stored route access has invalid lifecycle")
	}
	if err := access.Revision.Validate(); err != nil {
		return RouteAccess{}, fmt.Errorf("stored route access has invalid revision: %w", err)
	}
	return access, nil
}
