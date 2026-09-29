package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/jackc/pgx/v5"
)

func (q *Queries) SavePinRevision(ctx context.Context, access RouteAccess, city string, base d.RoutePlanSnapshot, history []d.Execution, visitID d.VisitID, input routewire.PinVisitInput, result routewire.RecomputedResult, now time.Time) (d.RouteRevisionNumber, error) {
	if _, ok := q.db.(pgx.Tx); !ok {
		return 0, errors.New("pin revision requires a transaction")
	}
	if result.Diagnostics.Status != "PROPOSED" || result.Candidate == nil || now.IsZero() || access.Revision <= 0 || access.Revision == math.MaxInt64 || base.Lifecycle != access.Lifecycle {
		return 0, routewire.ErrInvalidResult
	}
	if _, err := routewire.BuildPinRecompute(access.RouteID, city, base, history, visitID, input); err != nil {
		return 0, err
	}
	snapshot := result.Candidate.Clone()
	if err := routewire.ValidatePinCandidate(base, snapshot, history, visitID, input, result.Changes); err != nil {
		return 0, err
	}
	if int64(snapshot.CatalogRevision) != result.Diagnostics.CatalogRevision {
		return 0, routewire.ErrInvalidResult
	}
	current, err := q.LockOwnedRoute(ctx, access.RouteID, access.OwnerID)
	if err != nil {
		return 0, err
	}
	if err := RequireRevision(current, access.Revision); err != nil {
		return 0, err
	}
	if current.Lifecycle != access.Lifecycle {
		return 0, routewire.ErrInvalidResult
	}
	stored, err := q.ReadRecomputeState(ctx, access.RouteID, access.OwnerID)
	if err != nil {
		return 0, err
	}
	if stored.Access != current || stored.City != city || !reflect.DeepEqual(stored.Plan, base) {
		return 0, routewire.ErrInvalidRecomputeInput
	}
	if err := q.checkPinHistory(ctx, access.RouteID, base, history); err != nil {
		return 0, err
	}
	if err := q.CheckCity(ctx, city, snapshot.Timezone); err != nil {
		return 0, err
	}
	if err := q.LockCatalog(ctx, city, int64(snapshot.CatalogRevision)); err != nil {
		return 0, err
	}
	future := snapshot.Clone()
	done := make(map[d.VisitID]bool, len(history))
	for _, execution := range history {
		done[execution.VisitID] = execution.Status == d.ExecutionCompleted
	}
	future.Steps = nil
	for _, step := range snapshot.Steps {
		if !done[step.VisitID] {
			future.Steps = append(future.Steps, step)
		}
	}
	if err := q.CheckPlanCatalog(ctx, city, future); err != nil {
		return 0, err
	}
	return q.saveRecomputeRevision(ctx, access, city, base, snapshot, "pin", d.ProposalID{}, now)
}

func (q *Queries) saveRecomputeRevision(ctx context.Context, access RouteAccess, city string, base, snapshot d.RoutePlanSnapshot, mutation string, proposalID d.ProposalID, now time.Time) (d.RouteRevisionNumber, error) {
	if _, ok := q.db.(pgx.Tx); !ok || access.Revision <= 0 || access.Revision == math.MaxInt64 || now.IsZero() || (mutation != "pin" && mutation != "apply") {
		return 0, routewire.ErrInvalidResult
	}
	plan, err := routewire.PlanToWire(snapshot)
	if err != nil {
		return 0, err
	}
	warnings, err := json.Marshal(plan.Warnings)
	if err != nil {
		return 0, err
	}
	cost, err := json.Marshal(plan.Cost)
	if err != nil {
		return 0, err
	}
	geometry, err := geometryJSON(plan.Geometry)
	if err != nil {
		return 0, err
	}
	next := access.Revision + 1
	route := encodeUUID([16]byte(access.RouteID))
	inserted, err := q.db.Exec(ctx, `INSERT INTO planning.route_revision
(route_id,revision,parent_revision,lifecycle_state,archetype_id,timezone,start_at,end_at,origin,destination,input_schema_version,constraints,catalog_revision,result_status,warnings,cost_summary,geometry_geojson,mutation_kind,created_at)
SELECT route_id,$2,revision,lifecycle_state,archetype_id,timezone,start_at,end_at,origin,destination,input_schema_version,constraints,$3,$4,$5,$6,$7,$10,$8
FROM planning.route_revision WHERE route_id=$1 AND revision=$9`, route, int64(next), int64(snapshot.CatalogRevision), plan.Result, warnings, cost, geometry, now, int64(access.Revision), mutation)
	if err != nil {
		return 0, err
	}
	if inserted.RowsAffected() != 1 {
		return 0, ErrNotFound
	}
	existing := make(map[d.VisitID]bool, len(base.Steps))
	for _, step := range base.Steps {
		existing[step.VisitID] = true
	}
	for i, step := range snapshot.Steps {
		if err := q.insertPinStep(ctx, route, next, city, step, plan.Steps[i], existing[step.VisitID], now); err != nil {
			return 0, err
		}
	}
	for i, leg := range plan.Legs {
		geometry, err := geometryJSON(leg.Geometry)
		if err != nil {
			return 0, err
		}
		evidence, err := json.Marshal(leg.Evidence)
		if err != nil {
			return 0, err
		}
		cost, err := costSnapshotJSON(&leg.Cost, &snapshot.Legs[i].Cost)
		if err != nil {
			return 0, err
		}
		_, err = q.db.Exec(ctx, `INSERT INTO planning.route_leg
(route_id,revision,position,from_kind,to_kind,from_visit_id,to_visit_id,departure_at,arrival_at,mode,distance_m,geometry,verification_status,evidence,cost_snapshot)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,ST_GeomFromGeoJSON($12::jsonb),$13,$14,$15)`, route, int64(next), leg.Position, leg.FromKind, leg.ToKind, optionalRouteID(snapshot.Legs[i].FromVisitID), optionalRouteID(snapshot.Legs[i].ToVisitID), leg.DepartureAt, leg.ArrivalAt, leg.Mode, leg.DistanceMeters, geometry, leg.Verification, evidence, cost)
		if err != nil {
			return 0, err
		}
	}
	if _, err := q.db.Exec(ctx, `UPDATE planning.route_proposal SET state='invalidated',resolved_at=$3
WHERE route_id=$1 AND state='pending' AND base_revision<$2 AND id<>$4`, route, int64(next), now, encodeUUID([16]byte(proposalID))); err != nil {
		return 0, err
	}
	updated, err := q.db.Exec(ctx, `UPDATE planning.route SET current_revision=$2,updated_at=$3
WHERE id=$1 AND owner_id=$4 AND city=$5 AND current_revision=$6`, route, int64(next), now, encodeUUID([16]byte(access.OwnerID)), city, int64(access.Revision))
	if err != nil {
		return 0, err
	}
	if updated.RowsAffected() != 1 {
		return 0, routewire.ErrInvalidResult
	}
	if _, err := q.db.Exec(ctx, `UPDATE planning.route_issue i SET state='resolved',resolved_at=$5
WHERE i.route_id=$1 AND i.issue_type='cancelled' AND i.state<>'resolved' AND i.catalog_revision<=$4
AND EXISTS (SELECT 1 FROM planning.route_step s WHERE s.route_id=i.route_id AND s.revision=$2 AND s.visit_id=i.visit_id)
AND NOT EXISTS (SELECT 1 FROM planning.route_step s WHERE s.route_id=i.route_id AND s.revision=$3 AND s.visit_id=i.visit_id)`, route, int64(access.Revision), int64(next), int64(snapshot.CatalogRevision), now); err != nil {
		return 0, err
	}
	return next, nil
}

func (q *Queries) checkPinHistory(ctx context.Context, routeID d.RouteID, base d.RoutePlanSnapshot, history []d.Execution) error {
	expected := make(map[d.VisitID]d.Execution, len(history))
	for _, execution := range history {
		expected[execution.VisitID] = execution
	}
	for _, step := range base.Steps {
		stored := d.Execution{RouteID: routeID, VisitID: step.VisitID}
		if err := q.db.QueryRow(ctx, `SELECT status,actual_started_at,actual_ended_at,confirmation_kind,updated_in_revision,updated_at
FROM planning.execution WHERE route_id=$1 AND visit_id=$2`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(step.VisitID))).Scan(&stored.Status, &stored.ActualStartedAt, &stored.ActualEndedAt, &stored.Confirmation, &stored.UpdatedInRevision, &stored.UpdatedAt); err != nil {
			return mapQueryError("read pin execution", err)
		}
		if err := stored.Validate(); err != nil {
			return err
		}
		value, exists := expected[step.VisitID]
		if !exists {
			if stored.Status != d.ExecutionPlanned || stored.ActualStartedAt != nil || stored.ActualEndedAt != nil {
				return routewire.ErrInvalidRecomputeInput
			}
			continue
		}
		if value.Status != stored.Status || value.Confirmation != stored.Confirmation || value.UpdatedInRevision != stored.UpdatedInRevision || !value.UpdatedAt.Equal(stored.UpdatedAt) || !samePinTime(value.ActualStartedAt, stored.ActualStartedAt) || !samePinTime(value.ActualEndedAt, stored.ActualEndedAt) {
			return routewire.ErrInvalidRecomputeInput
		}
	}
	return nil
}

func samePinTime(before, after *time.Time) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.Equal(*after)
}

func (q *Queries) insertPinStep(ctx context.Context, route any, revision d.RouteRevisionNumber, city string, step d.RouteStep, wire routewire.RouteStep, existing bool, now time.Time) error {
	visit := encodeUUID([16]byte(step.VisitID))
	var place, entrance, event, session, offer any
	if step.Catalog != nil {
		place, entrance, event, session = optionalRouteID(step.Catalog.PlaceID), optionalRouteID(step.Catalog.EntranceID), optionalRouteID(step.Catalog.EventID), optionalRouteID(step.Catalog.SessionID)
	}
	if step.Cost != nil {
		offer = optionalRouteID(step.Cost.PriceOfferID)
	}
	inserted, err := q.db.Exec(ctx, `INSERT INTO planning.route_visit
(route_id,visit_id,visit_kind,city,place_id,entrance_id,event_id,session_id,price_offer_id,created_in_revision,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (route_id,visit_id) DO NOTHING`, route, visit, string(step.Kind), city, place, entrance, event, session, offer, int64(revision), now)
	if err != nil {
		return err
	}
	if (inserted.RowsAffected() == 0) != existing {
		return routewire.ErrInvalidResult
	}
	if inserted.RowsAffected() == 0 {
		var matches bool
		if err := q.db.QueryRow(ctx, `SELECT v.visit_kind=$3 AND v.city=$4 AND v.place_id IS NOT DISTINCT FROM $5::uuid
AND v.event_id IS NOT DISTINCT FROM $6::uuid AND v.session_id IS NOT DISTINCT FROM $7::uuid
AND p.status=$8 AND p.evidence_source=$9
FROM planning.route_visit v JOIN planning.participation p ON p.route_id=v.route_id AND p.visit_id=v.visit_id
WHERE v.route_id=$1 AND v.visit_id=$2`, route, visit, string(step.Kind), city, place, event, session, string(step.Participation.Status), string(step.Participation.Evidence)).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return routewire.ErrInvalidResult
		}
	} else {
		if step.Participation.Evidence != d.EvidenceNone || step.Participation.Status == d.ParticipationUserReported || step.Participation.Status == d.ParticipationProviderConfirmed {
			return routewire.ErrInvalidResult
		}
		if _, err := q.db.Exec(ctx, `INSERT INTO planning.participation (route_id,visit_id,status,evidence_source,updated_in_revision,updated_at)
VALUES ($1,$2,$3,$4,$5,$6)`, route, visit, string(step.Participation.Status), string(step.Participation.Evidence), int64(revision), now); err != nil {
			return err
		}
		if _, err := q.db.Exec(ctx, `INSERT INTO planning.execution (route_id,visit_id,status,confirmation_kind,updated_in_revision,updated_at)
VALUES ($1,$2,'planned','user_reported',$3,$4)`, route, visit, int64(revision), now); err != nil {
			return err
		}
	}
	participation, err := json.Marshal(wire.Participation)
	if err != nil {
		return err
	}
	catalog, err := catalogSnapshotJSON(wire.Catalog, step.Catalog)
	if err != nil {
		return err
	}
	cost, err := costSnapshotJSON(wire.Cost, step.Cost)
	if err != nil {
		return err
	}
	applied, err := json.Marshal(wire.AppliedConstraints)
	if err != nil {
		return err
	}
	_, err = q.db.Exec(ctx, `INSERT INTO planning.route_step
(route_id,revision,position,visit_id,arrival_at,visit_start_at,visit_end_at,departure_at,min_duration_s,is_pinned,is_obligation,participation_snapshot,catalog_snapshot,cost_snapshot,applied_constraints)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, route, int64(revision), step.Position, visit, step.ArrivalAt, step.VisitStartAt, step.VisitEndAt, step.DepartureAt, step.MinDurationSeconds, step.Pinned, step.Obligation, participation, catalog, cost, applied)
	return err
}
