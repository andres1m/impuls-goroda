package workflow

import (
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/activity"
)

// A city is published in batches: each batch is one catalog revision, and every revision makes
// readers rebuild their copy of the city, so records are gathered rather than published one by one.
const (
	batchSize         = 500
	quietFlush        = 2 * time.Second
	longestWait       = 10 * time.Second
	batchesPerRun     = 100
	idleExit          = time.Minute
	applyBatchTimeout = 5 * time.Minute
)

// signalsPerRun keeps a run well below the server's limits on signals and history per execution;
// the rest go on in a new run.
var signalsPerRun = 2000

// MaterializeCity gathers the city's raw record ids from signals and publishes them in batches.
// It ends after a minute without records; the next signal starts it again.
func MaterializeCity(ctx workflow.Context, city domain.City, carried []string) error {
	ctx = workflow.WithActivityOptions(
		ctx,
		workflow.ActivityOptions{StartToCloseTimeout: applyBatchTimeout, RetryPolicy: retries},
	)
	signals := workflow.GetSignalChannel(ctx, activity.MaterializeSignal)
	b := &buffer{seen: make(map[string]bool)}
	for _, id := range carried {
		b.add(id, workflow.Now(ctx))
	}
	received := 0
	receive := func(c workflow.ReceiveChannel, _ bool) {
		var id string
		c.Receive(ctx, &id)
		received++
		b.add(id, workflow.Now(ctx))
	}

	for batches := 0; ; {
		if received >= signalsPerRun || workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			drainSignals(ctx, signals, b)
			return workflow.NewContinueAsNewError(ctx, MaterializeCity, city, b.ids)
		}
		if len(b.ids) == 0 {
			if !waitWhenIdle(ctx, signals, receive, b) {
				return nil
			}
			continue
		}
		if waitForMore(ctx, signals, receive, b) {
			continue
		}
		apply(ctx, city, b.take(batchSize))
		batches++
		if batches >= batchesPerRun {
			drainSignals(ctx, signals, b)
			return workflow.NewContinueAsNewError(ctx, MaterializeCity, city, b.ids)
		}
	}
}

func drainSignals(ctx workflow.Context, signals workflow.ReceiveChannel, b *buffer) {
	var id string
	for signals.ReceiveAsync(&id) {
		b.add(id, workflow.Now(ctx))
	}
}

func waitWhenIdle(
	ctx workflow.Context,
	signals workflow.ReceiveChannel,
	receive func(workflow.ReceiveChannel, bool),
	b *buffer,
) bool {
	if waitFor(ctx, signals, receive, idleExit) {
		return true
	}
	drainSignals(ctx, signals, b)
	return len(b.ids) > 0
}

func waitForMore(
	ctx workflow.Context,
	signals workflow.ReceiveChannel,
	receive func(workflow.ReceiveChannel, bool),
	b *buffer,
) bool {
	if len(b.ids) >= batchSize {
		return false
	}
	wait := min(quietFlush, b.first.Add(longestWait).Sub(workflow.Now(ctx)))
	return wait > 0 && waitFor(ctx, signals, receive, wait)
}

// waitFor reports whether a signal came before the timeout.
func waitFor(
	ctx workflow.Context,
	signals workflow.ReceiveChannel,
	receive func(workflow.ReceiveChannel, bool),
	timeout time.Duration,
) bool {
	timerCtx, cancel := workflow.WithCancel(ctx)
	defer cancel()
	received := false
	workflow.NewSelector(ctx).
		AddReceive(signals, func(c workflow.ReceiveChannel, more bool) { receive(c, more); received = true }).
		AddFuture(workflow.NewTimer(timerCtx, timeout), func(workflow.Future) {}).
		Select(ctx)
	return received
}

// apply publishes one batch. A batch that still fails after its retries stays pending in the
// database; the workflow goes on with the next one.
func apply(ctx workflow.Context, city domain.City, ids []string) {
	var res materialize.Result
	if err := workflow.ExecuteActivity(ctx, acts.ApplyBatch, city, ids).Get(ctx, &res); err != nil {
		workflow.GetLogger(ctx).Error("materialize batch failed", "city", city, "records", len(ids), "error", err)
		return
	}
	workflow.GetLogger(ctx).Info("materialized batch", "city", city, "applied", res.Applied, "unchanged", res.Unchanged,
		"superseded", res.Superseded, "failed", res.Failed, "quarantined", res.Quarantined, "deferred", res.Deferred, "catalog_revision", res.CatalogRevision)
}

type buffer struct {
	ids   []string
	seen  map[string]bool
	first time.Time
}

func (b *buffer) add(id string, now time.Time) {
	if b.seen[id] {
		return
	}
	if len(b.ids) == 0 {
		b.first = now
	}
	b.seen[id] = true
	b.ids = append(b.ids, id)
}

func (b *buffer) take(n int) []string {
	n = min(n, len(b.ids))
	out := b.ids[:n:n]
	for _, id := range out {
		delete(b.seen, id)
	}
	b.ids = append([]string(nil), b.ids[n:]...)
	return out
}
