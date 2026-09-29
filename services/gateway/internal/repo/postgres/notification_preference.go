package postgres

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotificationPreferenceConflict = errors.New("notification preference version is stale")
	ErrNotificationRouteNotSaved      = errors.New("notification preference requires a saved route")
)

func (q *Queries) ReadNotificationPreference(ctx context.Context, routeID d.RouteID, owner d.UserID) (routewire.NotificationPreferenceResponse, error) {
	var result routewire.NotificationPreferenceResponse
	var revision, version int64
	err := q.db.QueryRow(ctx, `SELECT r.id::text,r.current_revision,COALESCE(p.enabled,false),COALESCE(p.version,0),
CASE WHEN m.stopped THEN 'stopped' WHEN m.muted THEN 'muted'
WHEN m.stopped_observed_at_ms IS NULL AND m.muted_observed_at_ms IS NULL THEN 'unknown' ELSE 'available' END
FROM planning.route r LEFT JOIN gateway_ops.route_notification_preference p ON p.route_id=r.id AND p.owner_id=r.owner_id
LEFT JOIN gateway_ops.max_delivery_state m ON m.user_account_id=r.owner_id
WHERE r.id=$1 AND r.owner_id=$2`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(owner))).Scan(
		&result.RouteID, &revision, &result.Preference.Enabled, &version, &result.Preference.PlatformState)
	if err != nil {
		return result, mapQueryError("read notification preference", err)
	}
	result.Revision = strconv.FormatInt(revision, 10)
	result.Preference.Version = strconv.FormatInt(version, 10)
	return result, nil
}

func (q *Queries) SetNotificationPreference(ctx context.Context, access RouteAccess, input routewire.NotificationPreferenceInput, now time.Time) (routewire.NotificationPreferenceResponse, error) {
	if _, ok := q.db.(pgx.Tx); !ok || now.IsZero() {
		return routewire.NotificationPreferenceResponse{}, routewire.ErrInvalidNotificationPreference
	}
	if access.Lifecycle != d.RouteSaved {
		return routewire.NotificationPreferenceResponse{}, ErrNotificationRouteNotSaved
	}
	current, err := q.ReadNotificationPreference(ctx, access.RouteID, access.OwnerID)
	if err != nil {
		return current, err
	}
	if current.Preference.Version != input.ExpectedVersion {
		return current, ErrNotificationPreferenceConflict
	}
	if current.Preference.Enabled != input.Enabled {
		version, err := strconv.ParseInt(current.Preference.Version, 10, 64)
		if err != nil || version < 0 || version == math.MaxInt64 {
			return current, ErrNotificationPreferenceConflict
		}
		_, err = q.db.Exec(ctx, `INSERT INTO gateway_ops.route_notification_preference(route_id,owner_id,enabled,version,changed_at)
VALUES($1,$2,$3,$4,$5) ON CONFLICT(route_id) DO UPDATE SET enabled=EXCLUDED.enabled,version=EXCLUDED.version,changed_at=EXCLUDED.changed_at`,
			encodeUUID([16]byte(access.RouteID)), encodeUUID([16]byte(access.OwnerID)), input.Enabled, version+1, now)
		if err != nil {
			return current, err
		}
	}
	if !input.Enabled {
		if _, err := q.db.Exec(ctx, `UPDATE gateway_ops.notification SET state='suppressed',lease_token=NULL,lease_until=NULL,
last_error_code='NOTIFICATIONS_DISABLED' WHERE route_id=$1 AND recipient_id=$2 AND state IN ('pending','failed')`,
			encodeUUID([16]byte(access.RouteID)), encodeUUID([16]byte(access.OwnerID))); err != nil {
			return current, err
		}
	}
	return q.ReadNotificationPreference(ctx, access.RouteID, access.OwnerID)
}
