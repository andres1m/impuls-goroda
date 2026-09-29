package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

type RoutePageQuery struct {
	Lifecycle  string
	Limit      int
	BeforeTime *time.Time
	BeforeID   d.RouteID
}

func (q *Queries) ListOwnerRoutes(ctx context.Context, actor d.UserID, page RoutePageQuery) ([]routewire.RouteSummary, error) {
	if actor == (d.UserID{}) || page.Limit < 1 || page.Limit > 101 || (page.Lifecycle != "" && page.Lifecycle != "draft" && page.Lifecycle != "saved") || (page.BeforeTime == nil) != (page.BeforeID == (d.RouteID{})) {
		return nil, errors.New("invalid owner page query")
	}
	rows, err := q.db.Query(ctx, `SELECT jsonb_build_object(
'route_id',r.id::text,'revision',r.current_revision::text,'lifecycle',r.lifecycle_state,'city',r.city,
'timezone',v.timezone,'start_at',v.start_at,'end_at',v.end_at,'result',v.result_status,'archetype_id',v.archetype_id,'cost',v.cost_summary,'updated_at',r.updated_at)
FROM planning.route r JOIN planning.route_revision v ON v.route_id=r.id AND v.revision=r.current_revision
WHERE r.owner_id=$1 AND ($2::text='' OR r.lifecycle_state=$2)
AND ($3::timestamptz IS NULL OR (r.updated_at,r.id)<($3::timestamptz,$4::uuid))
ORDER BY r.updated_at DESC,r.id DESC LIMIT $5`, encodeUUID([16]byte(actor)), page.Lifecycle, page.BeforeTime, encodeUUID([16]byte(page.BeforeID)), page.Limit)
	if err != nil {
		return nil, mapQueryError("list owner routes", err)
	}
	defer rows.Close()
	routes := make([]routewire.RouteSummary, 0, page.Limit)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, mapQueryError("scan owner summary", err)
		}
		var route routewire.RouteSummary
		if err := json.Unmarshal(raw, &route); err != nil {
			return nil, mapQueryError("decode owner summary", err)
		}
		if err := route.Validate(); err != nil {
			return nil, mapQueryError("validate owner summary", err)
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, mapQueryError("read owner summaries", err)
	}
	return routes, nil
}
