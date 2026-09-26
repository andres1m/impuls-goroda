package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrInvalidVisitAction = errors.New("invalid visit action")

func (q *Queries) currentVisit(ctx context.Context, routeID domain.RouteID, revision domain.RouteRevisionNumber, visitID domain.VisitID) error {
	var exists bool
	err := q.db.QueryRow(ctx, `SELECT EXISTS (
    SELECT 1 FROM planning.route_step s
    JOIN planning.route_visit v ON v.route_id = s.route_id AND v.visit_id = s.visit_id
    WHERE s.route_id = $1 AND s.revision = $2 AND s.visit_id = $3 AND v.visit_kind = 'visit'
)`, encodeUUID([16]byte(routeID)), int64(revision), encodeUUID([16]byte(visitID))).Scan(&exists)
	if err != nil {
		return fmt.Errorf("find current visit: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func (q *Queries) Participation(ctx context.Context, routeID domain.RouteID, visitID domain.VisitID, revision domain.RouteRevisionNumber) (domain.Participation, error) {
	if err := q.currentVisit(ctx, routeID, revision, visitID); err != nil {
		return domain.Participation{}, err
	}
	var item domain.Participation
	var providerID pgtype.UUID
	item.RouteID, item.VisitID = routeID, visitID
	err := q.db.QueryRow(ctx, `SELECT status, evidence_source, provider_record_id, private_reference,
    external_link_opened_at, updated_in_revision, updated_at
FROM planning.participation WHERE route_id = $1 AND visit_id = $2 FOR UPDATE`,
		encodeUUID([16]byte(routeID)), encodeUUID([16]byte(visitID)),
	).Scan(&item.Status, &item.Evidence, &providerID, &item.PrivateReference,
		&item.ExternalLinkOpenedAt, &item.UpdatedInRevision, &item.UpdatedAt)
	if err != nil {
		return domain.Participation{}, mapQueryError("find participation", err)
	}
	if providerID.Valid {
		decoded, err := decodeUUID(providerID)
		if err != nil {
			return domain.Participation{}, err
		}
		id := domain.SourceRecordID(decoded)
		item.ProviderRecordID = &id
	}
	item.UpdatedAt = item.UpdatedAt.UTC()
	if item.ExternalLinkOpenedAt != nil {
		value := item.ExternalLinkOpenedAt.UTC()
		item.ExternalLinkOpenedAt = &value
	}
	return item, item.Validate()
}

func (q *Queries) InitialParticipation(ctx context.Context, routeID domain.RouteID, visitID domain.VisitID) (domain.ParticipationStatus, error) {
	var status domain.ParticipationStatus
	err := q.db.QueryRow(ctx, `SELECT COALESCE(participation_snapshot->>'status', participation_snapshot->>'Status')
FROM planning.route_step
WHERE route_id = $1 AND visit_id = $2
  AND COALESCE(participation_snapshot->>'evidence', participation_snapshot->>'Evidence') = 'none'
ORDER BY revision LIMIT 1`, encodeUUID([16]byte(routeID)), encodeUUID([16]byte(visitID))).Scan(&status)
	if err != nil {
		return "", mapQueryError("find initial participation", err)
	}
	if status != domain.ParticipationActionRequired && status != domain.ParticipationNotRequired {
		return "", ErrInvalidVisitAction
	}
	return status, nil
}

func (q *Queries) RecordLinkOpened(ctx context.Context, current domain.Participation, now time.Time) (domain.Participation, error) {
	if current.ExternalLinkOpenedAt != nil {
		return current, nil
	}
	err := q.db.QueryRow(ctx, `UPDATE planning.participation
SET external_link_opened_at = $3, updated_at = $3
WHERE route_id = $1 AND visit_id = $2
RETURNING updated_at`, encodeUUID([16]byte(current.RouteID)), encodeUUID([16]byte(current.VisitID)), now).Scan(&current.UpdatedAt)
	if err != nil {
		return domain.Participation{}, mapQueryError("record external link", err)
	}
	current.UpdatedAt = current.UpdatedAt.UTC()
	current.ExternalLinkOpenedAt = &now
	return current, nil
}

func (q *Queries) UpdateParticipation(ctx context.Context, current domain.Participation, next domain.Participation, revision domain.RouteRevisionNumber) error {
	_, err := q.db.Exec(ctx, `UPDATE planning.participation
SET status = $3, evidence_source = $4, private_reference = $5,
    updated_in_revision = $6, updated_at = $7
WHERE route_id = $1 AND visit_id = $2`,
		encodeUUID([16]byte(current.RouteID)), encodeUUID([16]byte(current.VisitID)),
		next.Status, next.Evidence, next.PrivateReference, int64(revision), next.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update participation: %w", err)
	}
	return nil
}

func (q *Queries) Execution(ctx context.Context, routeID domain.RouteID, visitID domain.VisitID, revision domain.RouteRevisionNumber) (domain.Execution, error) {
	if err := q.currentVisit(ctx, routeID, revision, visitID); err != nil {
		return domain.Execution{}, err
	}
	var item domain.Execution
	item.RouteID, item.VisitID = routeID, visitID
	err := q.db.QueryRow(ctx, `SELECT status, actual_started_at, actual_ended_at,
    confirmation_kind, updated_in_revision, updated_at
FROM planning.execution WHERE route_id = $1 AND visit_id = $2 FOR UPDATE`,
		encodeUUID([16]byte(routeID)), encodeUUID([16]byte(visitID)),
	).Scan(&item.Status, &item.ActualStartedAt, &item.ActualEndedAt,
		&item.Confirmation, &item.UpdatedInRevision, &item.UpdatedAt)
	if err != nil {
		return domain.Execution{}, mapQueryError("find execution", err)
	}
	item.UpdatedAt = item.UpdatedAt.UTC()
	if item.ActualStartedAt != nil {
		value := item.ActualStartedAt.UTC()
		item.ActualStartedAt = &value
	}
	if item.ActualEndedAt != nil {
		value := item.ActualEndedAt.UTC()
		item.ActualEndedAt = &value
	}
	return item, item.Validate()
}

func (q *Queries) UpdateExecution(ctx context.Context, next domain.Execution, revision domain.RouteRevisionNumber) error {
	_, err := q.db.Exec(ctx, `UPDATE planning.execution
SET status = $3, actual_started_at = $4, actual_ended_at = $5,
    confirmation_kind = $6, updated_in_revision = $7, updated_at = $8
WHERE route_id = $1 AND visit_id = $2`,
		encodeUUID([16]byte(next.RouteID)), encodeUUID([16]byte(next.VisitID)),
		next.Status, next.ActualStartedAt, next.ActualEndedAt,
		next.Confirmation, int64(revision), next.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update execution: %w", err)
	}
	return nil
}

func (q *Queries) CloneVisitRevision(ctx context.Context, routeID domain.RouteID, previous domain.RouteRevisionNumber, visitID domain.VisitID, mutation domain.RouteMutationKind, participation *domain.ParticipationSnapshot, now time.Time) (domain.RouteRevisionNumber, error) {
	next := previous + 1
	var snapshot []byte
	if participation != nil {
		var err error
		snapshot, err = json.Marshal(map[string]any{"status": participation.Status, "evidence": participation.Evidence})
		if err != nil {
			return 0, err
		}
	}
	id := encodeUUID([16]byte(routeID))
	_, err := q.db.Exec(ctx, `INSERT INTO planning.route_revision (
    route_id, revision, parent_revision, lifecycle_state, archetype_id, timezone,
    start_at, end_at, origin, destination, input_schema_version, constraints,
    catalog_revision, result_status, warnings, cost_summary, geometry_geojson,
    mutation_kind, created_at
) SELECT route_id, $2, revision, lifecycle_state, archetype_id, timezone,
    start_at, end_at, origin, destination, input_schema_version, constraints,
    catalog_revision, result_status, warnings, cost_summary, geometry_geojson,
    $3, $4 FROM planning.route_revision WHERE route_id = $1 AND revision = $5`,
		id, int64(next), string(mutation), now, int64(previous))
	if err != nil {
		return 0, fmt.Errorf("copy route revision: %w", err)
	}
	_, err = q.db.Exec(ctx, `INSERT INTO planning.route_step (
    route_id, revision, position, visit_id, arrival_at, visit_start_at,
    visit_end_at, departure_at, min_duration_s, is_pinned, is_obligation,
    participation_snapshot, catalog_snapshot, cost_snapshot, applied_constraints
) SELECT route_id, $2, position, visit_id, arrival_at, visit_start_at,
    visit_end_at, departure_at, min_duration_s, is_pinned, is_obligation,
    CASE WHEN visit_id = $3 AND $4::jsonb IS NOT NULL THEN $4::jsonb ELSE participation_snapshot END,
    catalog_snapshot, cost_snapshot, applied_constraints
FROM planning.route_step WHERE route_id = $1 AND revision = $5`,
		id, int64(next), encodeUUID([16]byte(visitID)), snapshot, int64(previous))
	if err != nil {
		return 0, fmt.Errorf("copy route steps: %w", err)
	}
	_, err = q.db.Exec(ctx, `INSERT INTO planning.route_leg (
    route_id, revision, position, from_kind, to_kind, from_visit_id, to_visit_id,
    departure_at, arrival_at, mode, distance_m, geometry, verification_status,
    evidence, cost_snapshot
) SELECT route_id, $2, position, from_kind, to_kind, from_visit_id, to_visit_id,
    departure_at, arrival_at, mode, distance_m, geometry, verification_status,
    evidence, cost_snapshot
FROM planning.route_leg WHERE route_id = $1 AND revision = $3`, id, int64(next), int64(previous))
	if err != nil {
		return 0, fmt.Errorf("copy route legs: %w", err)
	}
	_, err = q.db.Exec(ctx, `UPDATE planning.route_proposal
SET state = 'invalidated', resolved_at = $3
WHERE route_id = $1 AND state = 'pending' AND base_revision < $2`, id, int64(next), now)
	if err != nil {
		return 0, fmt.Errorf("invalidate route proposals: %w", err)
	}
	_, err = q.db.Exec(ctx, `UPDATE planning.route SET current_revision = $2, updated_at = $3
WHERE id = $1`, id, int64(next), now)
	if err != nil {
		return 0, fmt.Errorf("advance route revision: %w", err)
	}
	return next, nil
}
