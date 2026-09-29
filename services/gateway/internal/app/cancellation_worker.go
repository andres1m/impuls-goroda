package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	d "github.com/andres1m/impuls-goroda/services/gateway/internal/domain"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/optimizerclient"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"go.uber.org/zap"
)

type CancellationWorker struct{ runtime *Runtime }

var errCancellationHistoryIncomplete = errors.New("cancellation recompute requires actual execution times")

func NewCancellationWorker(runtime *Runtime) *CancellationWorker {
	return &CancellationWorker{runtime: runtime}
}

func (w *CancellationWorker) Name() string                   { return "gateway-cancellation-worker" }
func (w *CancellationWorker) DependsOn() []string            { return []string{"gateway-auth"} }
func (w *CancellationWorker) Init(ctx context.Context) error { return w.HealthCheck(ctx) }
func (w *CancellationWorker) HealthCheck(ctx context.Context) error {
	if w.runtime == nil || w.runtime.queries == nil || w.runtime.transactor == nil {
		return errors.New("cancellation worker is not initialized")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ready, err := w.runtime.queries.CancellationStorageReady(checkCtx)
	if err != nil || !ready {
		return errors.New("cancellation queue storage is unavailable")
	}
	return nil
}
func (w *CancellationWorker) Stop(context.Context) error { return nil }

func (w *CancellationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.runtime.runCancellationJob(ctx)
		}
	}
}

func (r *Runtime) runCancellationJob(ctx context.Context) {
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	job, err := r.queries.ClaimCancellationJob(claimCtx, r.clock().UTC())
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			r.log.Warn("cancellation queue claim failed")
		}
		return
	}
	if job == nil {
		return
	}
	workCtx, stop := context.WithTimeout(ctx, 25*time.Second)
	err = r.processCancellationJob(workCtx, *job)
	stop()
	if err == nil || errors.Is(err, postgres.ErrCancellationLeaseLost) || ctx.Err() != nil {
		return
	}
	code := "RECOMPUTE_FAILED"
	if errors.Is(err, postgres.ErrCatalogChanged) {
		code = "CATALOG_CHANGED"
	}
	if errors.Is(err, routewire.ErrInvalidResult) {
		code = "INVALID_RECOMPUTE_RESULT"
	}
	if errors.Is(err, routewire.ErrInvalidRecomputeInput) {
		code = "INVALID_RECOMPUTE_INPUT"
	}
	if errors.Is(err, errCancellationHistoryIncomplete) {
		code = "EXECUTION_TIMES_REQUIRED"
	}
	retryCtx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	if retryErr := r.deferCancellationJob(retryCtx, *job, code); retryErr != nil {
		if errors.Is(retryErr, postgres.ErrCancellationLeaseLost) {
			return
		}
		r.log.Warn("cancellation queue retry persistence failed")
		return
	}
	r.log.Warn("cancellation recompute deferred", zap.String("code", code))
}

type cancellationJobResult struct {
	Status     string               `json:"status"`
	ProposalID string               `json:"proposal_id,omitempty"`
	Conflicts  []routewire.Conflict `json:"conflicts,omitempty"`
}

func (r *Runtime) processCancellationJob(ctx context.Context, job postgres.CancellationJob) error {
	visits, minimum, err := r.queries.CurrentCancellationVisits(ctx, job)
	if err != nil {
		return err
	}
	if len(visits) == 0 {
		return r.finishObsoleteCancellationJob(ctx, job)
	}
	state, err := r.queries.ReadRecomputeState(ctx, job.RouteID, job.OwnerID)
	if err != nil {
		return err
	}
	for _, execution := range state.History {
		if execution.Status == d.ExecutionCompleted && (execution.ActualStartedAt == nil || execution.ActualEndedAt == nil) {
			return errCancellationHistoryIncomplete
		}
	}
	request, err := routewire.BuildCancellationRecompute(job.RouteID, state.City, state.Plan, state.History, visits, minimum)
	if err != nil {
		return err
	}
	optimizer, ok := r.cfg.Optimizer.(Recomputer)
	if !ok || optimizer == nil {
		return optimizerclient.ErrUnavailable
	}
	response, err := optimizer.Recompute(ctx, request, job.ID.String())
	if err != nil {
		return err
	}
	computed, err := routewire.DecodeCheckedCancellationRecompute(job.RouteID, state.City, state.Plan, state.History, visits, minimum, response)
	if err != nil {
		return err
	}
	return r.transactor.WithinTx(ctx, nil, func(q *postgres.Queries) error {
		if _, err := q.CheckRecomputeBasis(ctx, state, computed.Diagnostics.CatalogRevision); err != nil {
			return err
		}
		now := r.clock().UTC()
		if err := q.LockCancellationJob(ctx, job, now); err != nil {
			return err
		}
		result := cancellationJobResult{Status: computed.Diagnostics.Status, Conflicts: computed.Diagnostics.Conflicts}
		if computed.Diagnostics.Status == "PROPOSED" {
			proposal, err := q.SaveCancellationProposal(ctx, state, visits, minimum, computed, now)
			if err != nil {
				return err
			}
			result.ProposalID = proposal.ProposalID
			if err := q.ExplainCancellationProblem(ctx, job.RouteID, visits, d.CatalogRevision(computed.Diagnostics.CatalogRevision), "CANCELLATION_PROPOSAL_READY", "Сеанс отменён. Подготовлено предложение обновления маршрута. Просмотрите изменения перед применением."); err != nil {
				return err
			}
		} else if computed.Diagnostics.Status == "CONFLICT" {
			if err := q.ExplainCancellationProblem(ctx, job.RouteID, visits, d.CatalogRevision(computed.Diagnostics.CatalogRevision), "CANCELLATION_RECOMPUTE_CONFLICT", "Сеанс отменён. Сохранить остальные условия маршрута не удалось. Измените условия или удалите отменённое посещение."); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return q.FinishCancellationJob(ctx, job, encoded, r.clock().UTC())
	})
}

func (r *Runtime) deferCancellationJob(ctx context.Context, job postgres.CancellationJob, code string) error {
	message := "Сеанс отменён. Пересчёт пока недоступен; расписание не изменено. Попробуйте обновить маршрут позже."
	issueCode := "CANCELLATION_RECOMPUTE_DEFERRED"
	if code == "EXECUTION_TIMES_REQUIRED" {
		issueCode = code
		message = "Сеанс отменён. Для пересчёта укажите фактическое начало и конец пройденных посещений. Расписание пока не изменено."
	}
	return r.transactor.WithinTx(ctx, nil, func(q *postgres.Queries) error {
		if _, err := q.LockOwnedRoute(ctx, job.RouteID, job.OwnerID); err != nil {
			return err
		}
		now := r.clock().UTC()
		if err := q.LockCancellationJob(ctx, job, now); err != nil {
			return err
		}
		visits, minimum, err := q.CurrentCancellationVisits(ctx, job)
		if err != nil {
			return err
		}
		if err := q.ExplainCancellationProblem(ctx, job.RouteID, visits, minimum, issueCode, message); err != nil {
			return err
		}
		return q.RetryCancellationJob(ctx, job, r.clock().UTC(), code)
	})
}

func (r *Runtime) finishObsoleteCancellationJob(ctx context.Context, job postgres.CancellationJob) error {
	return r.transactor.WithinTx(ctx, nil, func(q *postgres.Queries) error {
		if _, err := q.LockOwnedRoute(ctx, job.RouteID, job.OwnerID); err != nil {
			return err
		}
		if err := q.LockCancellationJob(ctx, job, r.clock().UTC()); err != nil {
			return err
		}
		visits, _, err := q.CurrentCancellationVisits(ctx, job)
		if err != nil {
			return err
		}
		if len(visits) != 0 {
			return postgres.ErrCatalogChanged
		}
		return q.FinishCancellationJob(ctx, job, json.RawMessage(`{"status":"UNCHANGED"}`), r.clock().UTC())
	})
}
