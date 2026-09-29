package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/maxbot"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
	"github.com/google/uuid"
)

type NotificationWorker struct {
	runtime *Runtime
	client  *maxbot.Client
}

func NewNotificationWorker(runtime *Runtime, client *maxbot.Client) *NotificationWorker {
	return &NotificationWorker{runtime: runtime, client: client}
}

func (w *NotificationWorker) Name() string                   { return "gateway-notification-worker" }
func (w *NotificationWorker) DependsOn() []string            { return []string{"gateway-auth"} }
func (w *NotificationWorker) Init(ctx context.Context) error { return w.HealthCheck(ctx) }
func (w *NotificationWorker) HealthCheck(ctx context.Context) error {
	if w.runtime == nil || w.runtime.queries == nil || w.runtime.transactor == nil || w.client == nil {
		return errors.New("notification worker is not initialized")
	}
	if !w.runtime.cfg.NotificationDeliveryEnabled {
		return errors.New("notification delivery is disabled")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ready, err := w.runtime.queries.NotificationStorageReady(checkCtx)
	if err != nil || !ready {
		return errors.New("notification storage is unavailable")
	}
	return nil
}
func (w *NotificationWorker) Stop(context.Context) error { return nil }
func (w *NotificationWorker) Run(ctx context.Context) error {
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

func (w *NotificationWorker) runOne(ctx context.Context) {
	workCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	job, err := w.runtime.queries.ClaimNotification(workCtx, w.runtime.clock().UTC())
	if err != nil {
		if ctx.Err() == nil {
			w.runtime.log.Warn("notification claim failed")
		}
		return
	}
	if job == nil {
		return
	}
	var userID int64
	var allowed bool
	err = w.runtime.transactor.WithinTx(workCtx, nil, func(q *postgres.Queries) error {
		var err error
		userID, allowed, err = q.CheckNotificationDelivery(workCtx, *job, w.runtime.clock().UTC())
		if err != nil {
			return err
		}
		if !allowed {
			return q.FinishNotification(workCtx, *job, "suppressed", "DELIVERY_NOT_ALLOWED", nil, w.runtime.clock().UTC())
		}
		return nil
	})
	if err != nil || !allowed {
		if err != nil && !errors.Is(err, postgres.ErrNotificationLeaseLost) && ctx.Err() == nil {
			w.runtime.log.Warn("notification permission check failed")
		}
		return
	}
	state, code := "sent", ""
	var next *time.Time
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		RouteID       string `json:"route_id"`
	}
	_, linkErr := w.client.OwnerRouteURL(uuid.UUID(job.RouteID))
	decoder := json.NewDecoder(bytes.NewReader(job.Payload))
	decoder.DisallowUnknownFields()
	payloadErr := decoder.Decode(&payload)
	if len(job.Payload) > 1024 || payloadErr != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || payload.SchemaVersion != 1 || payload.RouteID != uuid.UUID(job.RouteID).String() || linkErr != nil {
		state, code = "failed", "INVALID_NOTIFICATION_PAYLOAD"
	} else {
		message := "Посещение в вашем маршруте стало недоступно. Откройте маршрут, чтобы посмотреть изменения."
		_, sendErr := w.client.SendRouteNotification(workCtx, userID, uuid.UUID(job.RouteID), message)
		if sendErr != nil {
			state, code = "failed", "MAX_REQUEST_REJECTED"
			var failure *maxbot.NotificationSendError
			if errors.As(sendErr, &failure) {
				if failure.HTTPStatus == 401 {
					code = "MAX_CREDENTIALS_REJECTED"
				}
				if failure.Retryable {
					code = "MAX_DELIVERY_RETRY"
					if failure.DeliveryUnknown {
						code = "MAX_DELIVERY_UNKNOWN"
					}
					delay := time.Second
					for i := 1; i < job.Attempts && delay < time.Minute; i++ {
						delay = min(2*delay, time.Minute)
					}
					due := w.runtime.clock().UTC().Add(max(delay, failure.RetryAfter))
					next = &due
				}
			}
		}
	}
	finishCtx, finish := context.WithTimeout(ctx, 3*time.Second)
	defer finish()
	err = w.runtime.transactor.WithinTx(finishCtx, nil, func(q *postgres.Queries) error {
		return q.FinishNotification(finishCtx, *job, state, code, next, w.runtime.clock().UTC())
	})
	if err != nil && !errors.Is(err, postgres.ErrNotificationLeaseLost) && ctx.Err() == nil {
		w.runtime.log.Warn("notification completion persistence failed")
	}
}
