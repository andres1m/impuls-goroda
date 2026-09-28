package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/activity"
)

var acts *activity.Activities

// retries is the catalog's activity retry policy: 2 s doubling up to a minute, five attempts.
var retries = &temporal.RetryPolicy{InitialInterval: 2 * time.Second, BackoffCoefficient: 2, MaximumInterval: time.Minute, MaximumAttempts: 5}

// ProcessRawIngest passes the stored raw record on to its city's batch.
func ProcessRawIngest(ctx workflow.Context, envelope ingest.Envelope) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second, RetryPolicy: retries})
	return workflow.ExecuteActivity(ctx, acts.Enqueue, envelope.City, envelope.RawIngestID).Get(ctx, nil)
}
