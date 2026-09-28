// Package activity holds the side effects of the catalog workflows.
package activity

import (
	"context"
	"fmt"
	"time"

	sdkactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
)

const (
	// MaterializeSignal carries one raw ingest id to the city's materializing workflow.
	MaterializeSignal = "raw"
	// MaterializeWorkflowName is the registered name of the city's materializing workflow.
	MaterializeWorkflowName = "MaterializeCity"
)

func MaterializeWorkflowID(city domain.City) string {
	return "materialize:" + string(city)
}

type Activities struct {
	Client func() client.Client
	Queue  string
	Store  materialize.Store
	Now    func() time.Time
}

// SignalMaterialize hands a raw record to its city's workflow, starting it when none runs. A repeated
// signal is harmless: the batch skips records that are no longer pending.
func SignalMaterialize(ctx context.Context, c client.Client, queue string, city domain.City, rawIngestID string) error {
	id := MaterializeWorkflowID(city)
	_, err := c.SignalWithStartWorkflow(ctx, id, MaterializeSignal, rawIngestID,
		client.StartWorkflowOptions{ID: id, TaskQueue: queue}, MaterializeWorkflowName, city, []string(nil))
	if err != nil {
		return fmt.Errorf("signal %s: %w", id, err)
	}
	return nil
}

func (a *Activities) Enqueue(ctx context.Context, city domain.City, rawIngestID string) error {
	return SignalMaterialize(ctx, a.Client(), a.Queue, city, rawIngestID)
}

func (a *Activities) ApplyBatch(ctx context.Context, city domain.City, ids []string) (materialize.Result, error) {
	res, err := materialize.Apply(ctx, a.Store, city, ids, a.Now)
	for _, f := range res.Failures {
		sdkactivity.GetLogger(ctx).Warn("raw record cannot become a catalog row",
			"raw_ingest_id", f.RawIngestID, "source", f.Source, "code", f.Code)
	}
	return res, err
}
