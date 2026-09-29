package app

import (
	"context"
	"errors"

	"github.com/andres1m/impuls-goroda/services/gateway/internal/lifecycle"
	"github.com/andres1m/impuls-goroda/services/gateway/internal/repo/postgres"
)

func (r *Runtime) ReceiveCatalogLifecycle(ctx context.Context, change lifecycle.Change, hash [32]byte) error {
	if r.transactor == nil {
		return errors.New("catalog lifecycle receiver is not initialized")
	}
	return r.transactor.WithinTx(ctx, nil, func(q *postgres.Queries) error {
		return q.ReceiveCatalogLifecycle(ctx, change, hash, r.clock().UTC(), r.cfg.CancellationWorkerEnabled, r.cfg.NotificationDeliveryEnabled)
	})
}
