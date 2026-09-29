package app

import (
	"context"
	"errors"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/maxbot"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/routewire"
	"github.com/google/uuid"
)

type ScenarioResultWorker struct {
	runtime *Runtime
	client  *maxbot.Client
}

func NewScenarioResultWorker(runtime *Runtime, client *maxbot.Client) *ScenarioResultWorker {
	return &ScenarioResultWorker{runtime: runtime, client: client}
}

func (w *ScenarioResultWorker) Name() string                   { return "gateway-scenario-result-worker" }
func (w *ScenarioResultWorker) DependsOn() []string            { return []string{"gateway-auth"} }
func (w *ScenarioResultWorker) Init(ctx context.Context) error { return w.HealthCheck(ctx) }
func (w *ScenarioResultWorker) Stop(context.Context) error     { return nil }
func (w *ScenarioResultWorker) HealthCheck(ctx context.Context) error {
	if w.runtime == nil || w.client == nil || w.runtime.queries == nil || w.runtime.transactor == nil || !w.runtime.cfg.ScenarioResultDeliveryEnabled {
		return errors.New("scenario result worker is not initialized")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ready, err := w.runtime.queries.ScenarioDeliveryStorageReady(checkCtx)
	if err != nil || !ready {
		return errors.New("scenario result delivery storage is unavailable")
	}
	return nil
}

func (w *ScenarioResultWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.runOne(ctx)
		}
	}
}

func (w *ScenarioResultWorker) runOne(ctx context.Context) {
	workCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	job, err := w.runtime.queries.ClaimScenarioResult(workCtx, w.runtime.clock().UTC())
	if err != nil {
		if ctx.Err() == nil {
			w.runtime.log.Warn("scenario result claim failed")
		}
		return
	}
	if job == nil {
		return
	}
	var userID int64
	var allowed bool
	var text string
	err = w.runtime.transactor.WithinTx(workCtx, nil, func(q *postgres.Queries) error {
		var err error
		userID, allowed, err = q.CheckScenarioResultDelivery(workCtx, *job, w.runtime.clock().UTC())
		if err != nil {
			return err
		}
		if !allowed {
			return q.FinishScenarioResult(workCtx, *job, "suppressed", nil, w.runtime.clock().UTC())
		}
		scenario, err := q.ReadBotScenario(workCtx, job.OwnerID, job.ScenarioID)
		if err != nil {
			return err
		}
		text, err = scenarioResultMessage(scenario)
		return err
	})
	if err != nil || !allowed {
		if err != nil && !errors.Is(err, postgres.ErrScenarioDeliveryLeaseLost) && ctx.Err() == nil {
			w.runtime.log.Warn("scenario result preparation failed")
		}
		return
	}
	state := "sent"
	var next *time.Time
	id, err := uuid.Parse(job.ScenarioID)
	if err == nil {
		_, err = w.client.SendScenarioResult(workCtx, userID, id, text)
	}
	if err != nil {
		state = "failed"
		var failure *maxbot.NotificationSendError
		if errors.As(err, &failure) && failure.Retryable && job.Attempts < 5 {
			state = "pending"
			delay := time.Second << (job.Attempts - 1)
			due := w.runtime.clock().UTC().Add(max(delay, failure.RetryAfter))
			next = &due
		}
	}
	finishCtx, finish := context.WithTimeout(ctx, 3*time.Second)
	defer finish()
	err = w.runtime.transactor.WithinTx(finishCtx, nil, func(q *postgres.Queries) error {
		return q.FinishScenarioResult(finishCtx, *job, state, next, w.runtime.clock().UTC())
	})
	if err != nil && !errors.Is(err, postgres.ErrScenarioDeliveryLeaseLost) && ctx.Err() == nil {
		w.runtime.log.Warn("scenario result completion persistence failed")
	}
}

func scenarioResultMessage(scenario routewire.BotScenario) (string, error) {
	if scenario.Outcome == nil {
		return "", errors.New("scenario result is missing")
	}
	var text string
	switch scenario.Outcome.Status {
	case "READY":
		text = "Маршруты рассчитаны. Выберите вариант в Mini App."
	case "PARTIAL":
		text = "Маршруты рассчитаны с предупреждениями. Проверьте их перед выбором в Mini App."
	case "NO_FEASIBLE_ROUTE", "CONFLICT":
		text = "С этими условиями маршрут подобрать не удалось. Причина и сохранённые условия доступны в Mini App."
	default:
		return "", errors.New("invalid scenario result status")
	}
	switch scenario.Outcome.DataMode {
	case "live":
		text += "\nДанные расчёта: актуальные."
	case "prepared":
		text += "\nДанные расчёта: подготовленные."
	case "synthetic":
		text += "\nДанные расчёта: демонстрационные."
	default:
		return "", errors.New("invalid scenario result data mode")
	}
	if scenario.Outcome.Status == "READY" && len(scenario.Outcome.Warnings) > 0 {
		text += "\nУ расчёта есть предупреждения — проверьте их в Mini App."
	}
	return text, nil
}
