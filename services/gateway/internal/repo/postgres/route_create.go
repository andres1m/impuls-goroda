package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

func optionalRouteID[T ~[16]byte](id *T) any {
	if id == nil {
		return nil
	}
	return encodeUUID([16]byte(*id))
}

func geometryJSON(points []routewire.Coordinate) ([]byte, error) {
	if len(points) == 0 {
		return nil, nil
	}
	if len(points) < 2 {
		return nil, routewire.ErrInvalidResult
	}
	coordinates := make([][2]float64, 0, len(points))
	for _, point := range points {
		coordinates = append(coordinates, [2]float64{point.Longitude, point.Latitude})
	}
	return json.Marshal(struct {
		Type        string       `json:"type"`
		Coordinates [][2]float64 `json:"coordinates"`
	}{"LineString", coordinates})
}

func (q *Queries) CreateDraft(ctx context.Context, actor d.UserID, city string, snapshot d.RoutePlanSnapshot, now time.Time) (d.RouteID, error) {
	if actor == (d.UserID{}) || city == "" || now.IsZero() || snapshot.Lifecycle != d.RouteDraft || len(snapshot.Conflicts) != 0 {
		return d.RouteID{}, errors.New("invalid draft")
	}
	plan, err := routewire.PlanToWire(snapshot)
	if err != nil {
		return d.RouteID{}, err
	}
	newID, err := uuid.NewV7()
	if err != nil {
		return d.RouteID{}, err
	}
	id := d.RouteID(newID)
	route := encodeUUID([16]byte(id))
	if _, err = q.db.Exec(ctx, `INSERT INTO planning.route (id, owner_id, city, lifecycle_state, current_revision, created_at, updated_at)
VALUES ($1,$2,$3,'draft',1,$4,$4)`, route, encodeUUID([16]byte(actor)), city, now); err != nil {
		return d.RouteID{}, err
	}
	constraints, err := json.Marshal(plan.Constraints)
	if err != nil {
		return d.RouteID{}, err
	}
	warnings, err := json.Marshal(plan.Warnings)
	if err != nil {
		return d.RouteID{}, err
	}
	cost, err := json.Marshal(plan.Cost)
	if err != nil {
		return d.RouteID{}, err
	}
	geometry, err := geometryJSON(plan.Geometry)
	if err != nil {
		return d.RouteID{}, err
	}
	origin, err := pointJSON(plan.Origin)
	if err != nil {
		return d.RouteID{}, err
	}
	var destination []byte
	if plan.Destination != nil {
		destination, err = pointJSON(*plan.Destination)
		if err != nil {
			return d.RouteID{}, err
		}
	}
	_, err = q.db.Exec(ctx, `INSERT INTO planning.route_revision
(route_id,revision,lifecycle_state,archetype_id,timezone,start_at,end_at,origin,destination,input_schema_version,constraints,catalog_revision,result_status,warnings,cost_summary,geometry_geojson,mutation_kind,created_at)
VALUES ($1,1,'draft',$2,$3,$4,$5,ST_GeomFromGeoJSON($6::jsonb),ST_GeomFromGeoJSON($7::jsonb),$8,$9,$10,$11,$12,$13,$14,'create',$15)`,
		route, plan.ArchetypeID, plan.Timezone, plan.StartAt, plan.EndAt, origin, destination, plan.SchemaVersion, constraints, int64(snapshot.CatalogRevision), plan.Result, warnings, cost, geometry, now)
	if err != nil {
		return d.RouteID{}, err
	}
	for i, step := range snapshot.Steps {
		wire := plan.Steps[i]
		visit := encodeUUID([16]byte(step.VisitID))
		var place, entrance, event, session, offer any
		if step.Catalog != nil && step.Lunch == nil {
			place = optionalRouteID(step.Catalog.PlaceID)
			entrance = optionalRouteID(step.Catalog.EntranceID)
			event = optionalRouteID(step.Catalog.EventID)
			session = optionalRouteID(step.Catalog.SessionID)
		}
		if step.Cost != nil && step.Lunch == nil {
			offer = optionalRouteID(step.Cost.PriceOfferID)
		}
		identityKind := string(step.Kind)
		if step.Lunch != nil {
			identityKind = "lunch"
		}
		_, err = q.db.Exec(ctx, `INSERT INTO planning.route_visit (route_id,visit_id,visit_kind,city,place_id,entrance_id,event_id,session_id,price_offer_id,created_in_revision,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10)`, route, visit, identityKind, city, place, entrance, event, session, offer, now)
		if err != nil {
			return d.RouteID{}, err
		}
		participation, err := json.Marshal(wire.Participation)
		if err != nil {
			return d.RouteID{}, err
		}
		catalog, err := catalogSnapshotJSON(wire.Catalog, step.Catalog)
		if err != nil {
			return d.RouteID{}, err
		}
		cost, err := costSnapshotJSON(wire.Cost, step.Cost)
		if err != nil {
			return d.RouteID{}, err
		}
		applied, err := json.Marshal(wire.AppliedConstraints)
		if err != nil {
			return d.RouteID{}, err
		}
		_, err = q.db.Exec(ctx, `INSERT INTO planning.route_step (route_id,revision,position,visit_id,arrival_at,visit_start_at,visit_end_at,departure_at,min_duration_s,is_pinned,is_obligation,participation_snapshot,catalog_snapshot,cost_snapshot,applied_constraints)
VALUES ($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, route, step.Position, visit, step.ArrivalAt, step.VisitStartAt, step.VisitEndAt, step.DepartureAt, step.MinDurationSeconds, step.Pinned, step.Obligation, participation, catalog, cost, applied)
		if err != nil {
			return d.RouteID{}, err
		}
		if step.Participation.Evidence == d.EvidenceProvider || step.Participation.Status == d.ParticipationProviderConfirmed {
			return d.RouteID{}, routewire.ErrInvalidResult
		}
		_, err = q.db.Exec(ctx, `INSERT INTO planning.participation (route_id,visit_id,status,evidence_source,updated_in_revision,updated_at)
VALUES ($1,$2,$3,$4,1,$5)`, route, visit, string(step.Participation.Status), string(step.Participation.Evidence), now)
		if err != nil {
			return d.RouteID{}, err
		}
		_, err = q.db.Exec(ctx, `INSERT INTO planning.execution (route_id,visit_id,status,confirmation_kind,updated_in_revision,updated_at)
VALUES ($1,$2,'planned','user_reported',1,$3)`, route, visit, now)
		if err != nil {
			return d.RouteID{}, err
		}
	}
	if err := q.insertLunchSteps(ctx, route, 1, city, snapshot, plan); err != nil {
		return d.RouteID{}, err
	}
	for i, leg := range plan.Legs {
		geometry, err := geometryJSON(leg.Geometry)
		if err != nil {
			return d.RouteID{}, err
		}
		evidence, err := json.Marshal(leg.Evidence)
		if err != nil {
			return d.RouteID{}, err
		}
		cost, err := costSnapshotJSON(&leg.Cost, &snapshot.Legs[i].Cost)
		if err != nil {
			return d.RouteID{}, err
		}
		var from, to any
		if leg.FromVisitID != nil {
			from = *leg.FromVisitID
		}
		if leg.ToVisitID != nil {
			to = *leg.ToVisitID
		}
		_, err = q.db.Exec(ctx, `INSERT INTO planning.route_leg (route_id,revision,position,from_kind,to_kind,from_visit_id,to_visit_id,departure_at,arrival_at,mode,distance_m,geometry,verification_status,evidence,cost_snapshot)
VALUES ($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,ST_GeomFromGeoJSON($11::jsonb),$12,$13,$14)`, route, leg.Position, leg.FromKind, leg.ToKind, from, to, leg.DepartureAt, leg.ArrivalAt, leg.Mode, leg.DistanceMeters, geometry, leg.Verification, evidence, cost)
		if err != nil {
			return d.RouteID{}, err
		}
	}
	return id, nil
}

func pointJSON(point routewire.Coordinate) ([]byte, error) {
	return json.Marshal(struct {
		Type        string     `json:"type"`
		Coordinates [2]float64 `json:"coordinates"`
	}{"Point", [2]float64{point.Longitude, point.Latitude}})
}

type storedProvenance struct {
	routewire.PublicProvenance
	SourceRecordID *string `json:"source_record_id,omitempty"`
}

func snapshotID[T ~[16]byte](id *T) *string {
	if id == nil {
		return nil
	}
	value := uuid.UUID(*id).String()
	return &value
}

func catalogSnapshotJSON(wire *routewire.CatalogSnapshot, snapshot *d.CatalogSnapshot) ([]byte, error) {
	if snapshot == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		*routewire.CatalogSnapshot
		EntranceID *string          `json:"entrance_id,omitempty"`
		Provenance storedProvenance `json:"provenance"`
	}{wire, snapshotID(snapshot.EntranceID), storedProvenance{wire.Provenance, snapshotID(snapshot.Provenance.SourceRecordID)}})
}

func costSnapshotJSON(wire *routewire.CostSnapshot, snapshot *d.CostSnapshot) ([]byte, error) {
	if snapshot == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		*routewire.CostSnapshot
		Provenance storedProvenance `json:"provenance"`
	}{wire, storedProvenance{wire.Provenance, snapshotID(snapshot.Provenance.SourceRecordID)}})
}
