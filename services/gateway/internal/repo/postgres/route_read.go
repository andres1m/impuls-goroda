package postgres

import (
	"context"
	"encoding/json"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
)

const ownerRouteSQL = `SELECT jsonb_build_object(
'route_id',r.id::text,'revision',r.current_revision::text,'city',r.city,'lifecycle',r.lifecycle_state,'created_at',r.created_at,'updated_at',r.updated_at,
'plan',jsonb_build_object('schema_version',v.input_schema_version,'lifecycle',v.lifecycle_state,'archetype_id',v.archetype_id,'timezone',v.timezone,
'start_at',v.start_at,'end_at',v.end_at,'origin',jsonb_build_object('longitude',ST_X(v.origin),'latitude',ST_Y(v.origin)),
'destination',CASE WHEN v.destination IS NULL THEN NULL ELSE jsonb_build_object('longitude',ST_X(v.destination),'latitude',ST_Y(v.destination)) END,
'constraints',v.constraints,'catalog_revision',v.catalog_revision::text,'result',v.result_status,'warnings',v.warnings,'conflicts','[]'::jsonb,'cost',v.cost_summary,
'geometry',COALESCE((SELECT jsonb_agg(jsonb_build_object('longitude',point->0,'latitude',point->1) ORDER BY n)
FROM jsonb_array_elements(v.geometry_geojson->'coordinates') WITH ORDINALITY AS points(point,n)),'[]'::jsonb),
'steps',COALESCE((SELECT jsonb_agg(jsonb_build_object('visit_id',s.visit_id::text,'kind',CASE WHEN lunch.venue_kind='external' THEN 'external_lunch' WHEN lunch.venue_kind='free_time' THEN 'free_time' WHEN lunch.venue_kind='catalog' THEN 'visit' ELSE visit.visit_kind END,'position',s.position,
'arrival_at',s.arrival_at,'visit_start_at',s.visit_start_at,'visit_end_at',s.visit_end_at,'departure_at',s.departure_at,'min_duration_seconds',s.min_duration_s,
'pinned',s.is_pinned,'obligation',s.is_obligation,'participation',s.participation_snapshot,'catalog',s.catalog_snapshot,'cost',s.cost_snapshot,'applied_constraints',s.applied_constraints,
'lunch',CASE WHEN lunch.visit_id IS NULL THEN NULL ELSE jsonb_build_object('after_visit_id',lunch.after_visit_id::text,'duration_seconds',lunch.duration_seconds) END,
'external_venue',lunch.external_snapshot) ORDER BY s.position)
FROM planning.route_step s JOIN planning.route_visit visit ON visit.route_id=s.route_id AND visit.visit_id=s.visit_id
LEFT JOIN planning.route_lunch_step lunch ON lunch.route_id=s.route_id AND lunch.revision=s.revision AND lunch.visit_id=s.visit_id
WHERE s.route_id=r.id AND s.revision=r.current_revision),'[]'::jsonb),
'legs',COALESCE((SELECT jsonb_agg(jsonb_build_object('position',l.position,'from_kind',l.from_kind,'to_kind',l.to_kind,'from_visit_id',l.from_visit_id::text,'to_visit_id',l.to_visit_id::text,
'departure_at',l.departure_at,'arrival_at',l.arrival_at,'mode',l.mode,'distance_meters',l.distance_m,'verification',l.verification_status,'evidence',l.evidence,'cost',l.cost_snapshot,
'geometry',COALESCE((SELECT jsonb_agg(jsonb_build_object('longitude',point->0,'latitude',point->1) ORDER BY n)
FROM jsonb_array_elements(ST_AsGeoJSON(l.geometry,15,0)::jsonb->'coordinates') WITH ORDINALITY AS points(point,n)),'[]'::jsonb)) ORDER BY l.position)
FROM planning.route_leg l WHERE l.route_id=r.id AND l.revision=r.current_revision),'[]'::jsonb)),
'participation',COALESCE((SELECT jsonb_agg(jsonb_build_object('visit_id',p.visit_id::text,'status',p.status,'evidence',p.evidence_source,'private_reference',p.private_reference,
'external_link_opened_at',p.external_link_opened_at,'updated_in_revision',p.updated_in_revision::text,'updated_at',p.updated_at) ORDER BY p.visit_id) FROM planning.participation p WHERE p.route_id=r.id),'[]'::jsonb),
'execution',COALESCE((SELECT jsonb_agg(jsonb_build_object('visit_id',e.visit_id::text,'status',e.status,'confirmation_kind',e.confirmation_kind,'actual_started_at',e.actual_started_at,'actual_ended_at',e.actual_ended_at,
'updated_in_revision',e.updated_in_revision::text,'updated_at',e.updated_at) ORDER BY e.visit_id) FROM planning.execution e WHERE e.route_id=r.id),'[]'::jsonb),
'issues',COALESCE((SELECT jsonb_agg(jsonb_build_object('issue_id',i.id::text,'visit_id',i.visit_id::text,'type',i.issue_type,'state',i.state,'code',COALESCE(i.details->>'code',i.details->>'Code'),
'message',COALESCE(i.details->>'message',i.details->>'Message'),'catalog_revision',i.catalog_revision::text,'created_at',i.created_at,'resolved_at',i.resolved_at) ORDER BY i.created_at,i.id) FROM planning.route_issue i WHERE i.route_id=r.id),'[]'::jsonb))
FROM planning.route r JOIN planning.route_revision v ON v.route_id=r.id AND v.revision=r.current_revision
WHERE r.id=$1 AND r.owner_id=$2`

func (q *Queries) ReadOwnerRoute(ctx context.Context, id d.RouteID, actor d.UserID) (routewire.OwnerRoute, error) {
	var raw []byte
	if err := q.db.QueryRow(ctx, ownerRouteSQL, encodeUUID([16]byte(id)), encodeUUID([16]byte(actor))).Scan(&raw); err != nil {
		return routewire.OwnerRoute{}, mapQueryError("read owner route", err)
	}
	var route routewire.OwnerRoute
	if err := json.Unmarshal(raw, &route); err != nil {
		return routewire.OwnerRoute{}, err
	}
	return route, nil
}
