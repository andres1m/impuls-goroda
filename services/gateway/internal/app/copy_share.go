package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/command"
	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/sharewire"
	"github.com/jackc/pgx/v5"
)

type CopyRefusal struct {
	Status    string
	Conflicts []routewire.Conflict
}

func (e *CopyRefusal) Error() string { return "shared route cannot be copied" }

func (r *Runtime) CopySharedRoute(ctx context.Context, actor d.UserID, key [16]byte,
	token sharewire.Token, revision d.RouteRevisionNumber, input sharewire.CopyInput, requestID string) (command.Result, error) {
	if r.queries == nil || r.transactor == nil || r.commands == nil {
		return command.Result{}, errors.New("gateway commands are not initialized")
	}
	hash, err := token.Hash()
	if err != nil {
		return command.Result{}, postgres.ErrNotFound
	}
	fingerprint, err := command.Fingerprint(&command.FingerprintInput{ActorID: actor, Operation: command.CopySharedRoute,
		Path:             []command.PathComponent{{Name: "share_token_hash", Value: hex.EncodeToString(hash[:])}},
		ExpectedRevision: &revision, Body: input})
	if err != nil {
		return command.Result{}, err
	}
	envelope := command.Envelope{ActorID: actor, Operation: command.CopySharedRoute, Key: key, RequestHash: fingerprint}
	if result, found, err := r.queries.LookupCommand(ctx, envelope); err != nil || found {
		return result, err
	}
	if r.cfg.Optimizer == nil {
		return command.Result{}, optimizerclient.ErrUnavailable
	}
	for attempt := 0; attempt < 2; attempt++ {
		var source postgres.SharedCopySource
		err = r.transactor.WithinTx(ctx, &pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(q *postgres.Queries) error {
			var readErr error
			source, readErr = q.ReadSharedCopySource(ctx, hash, false)
			return readErr
		})
		if err != nil {
			return command.Result{}, err
		}
		if source.Revision != revision {
			return command.Result{}, &command.RevisionConflictError{Current: source.Revision}
		}
		request, confirmed, err := routewire.BuildCopyRequest(source.RouteID, source.City, source.Plan,
			input.Origin, input.Destination, input.AcceptedUnknowns)
		if err != nil {
			return command.Result{}, optimizerclient.ErrInvalidInput
		}
		response, err := r.cfg.Optimizer.CopyRoute(ctx, request, requestID)
		if err != nil {
			return command.Result{}, err
		}
		computed, err := routewire.DecodeCopyResult(confirmed, response)
		if err != nil {
			return command.Result{}, err
		}
		if len(computed.Plans) == 0 {
			return command.Result{}, &CopyRefusal{Status: computed.Diagnostics.Status, Conflicts: computed.Diagnostics.Conflicts}
		}
		if len(computed.Plans) != 1 || !copyPreservesVisits(source.Plan, computed.Plans[0]) {
			return command.Result{}, optimizerclient.ErrInvalidResponse
		}
		result, err := r.commands.Execute(ctx, &envelope, func(q *postgres.Queries) (command.Result, error) {
			current, err := q.ReadSharedCopySource(ctx, hash, true)
			if err != nil {
				return command.Result{}, err
			}
			if current.RouteID != source.RouteID || current.Revision != source.Revision {
				return command.Result{}, &command.RevisionConflictError{Current: current.Revision}
			}
			if err := q.LockCatalog(ctx, source.City, computed.Diagnostics.CatalogRevision); err != nil {
				return command.Result{}, err
			}
			if err := q.CheckPlanCatalog(ctx, source.City, computed.Plans[0]); err != nil {
				return command.Result{}, err
			}
			id, err := q.CreateDraft(ctx, actor, source.City, computed.Plans[0], r.clock().UTC())
			if err != nil {
				return command.Result{}, err
			}
			if err := q.SetCopyOrigin(ctx, id, source); err != nil {
				return command.Result{}, err
			}
			route, err := q.ReadOwnerRoute(ctx, id, actor)
			if err != nil {
				return command.Result{}, err
			}
			body, err := json.Marshal(struct {
				Status string               `json:"status"`
				Route  routewire.OwnerRoute `json:"route"`
			}{Status: computed.Diagnostics.Status, Route: route})
			if err != nil {
				return command.Result{}, err
			}
			newRevision := d.RouteRevisionNumber(1)
			return command.Result{HTTPStatus: http.StatusOK, ResponseBody: body, RouteID: &id, ResultingRevision: &newRevision}, nil
		})
		if !errors.Is(err, postgres.ErrCatalogChanged) {
			return result, err
		}
	}
	return command.Result{}, postgres.ErrCatalogChanged
}

func copyPreservesVisits(base, copied d.RoutePlanSnapshot) bool {
	type visit struct {
		place   d.PlaceID
		event   *d.EventID
		session *d.EventSessionID
	}
	var original, result []visit
	seen := make(map[d.VisitID]bool, len(base.Steps))
	for _, step := range base.Steps {
		seen[step.VisitID] = true
		if step.Kind == d.VisitPlace && step.Catalog != nil && step.Catalog.PlaceID != nil {
			original = append(original, visit{*step.Catalog.PlaceID, step.Catalog.EventID, step.Catalog.SessionID})
		}
	}
	for _, step := range copied.Steps {
		if seen[step.VisitID] || step.Participation.Evidence != d.EvidenceNone {
			return false
		}
		if step.Kind == d.VisitPlace && step.Catalog != nil && step.Catalog.PlaceID != nil {
			result = append(result, visit{*step.Catalog.PlaceID, step.Catalog.EventID, step.Catalog.SessionID})
		}
	}
	if len(original) != len(result) {
		return false
	}
	for i := range original {
		if original[i].place != result[i].place ||
			(original[i].event == nil) != (result[i].event == nil) ||
			(original[i].event != nil && *original[i].event != *result[i].event) ||
			(original[i].session == nil) != (result[i].session == nil) ||
			(original[i].session != nil && *original[i].session != *result[i].session) {
			return false
		}
	}
	return true
}
