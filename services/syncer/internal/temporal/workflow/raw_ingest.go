package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/activity"
)

var acts *activity.Activities

const (
	retryInitialInterval    = 2 * time.Second
	retryBackoffCoefficient = 2.0
	retryMaxAttempts        = 5
	enqueueActivityTimeout  = 30 * time.Second
)

// retries is the catalog's activity retry policy: 2 s doubling up to a minute, five attempts.
var retries = &temporal.RetryPolicy{
	InitialInterval:    retryInitialInterval,
	BackoffCoefficient: retryBackoffCoefficient,
	MaximumInterval:    time.Minute,
	MaximumAttempts:    retryMaxAttempts,
}

// ProcessRawIngest passes the stored raw record on to its city's batch.
func ProcessRawIngest(ctx workflow.Context, envelope *ingest.Envelope) error {
	ctx = workflow.WithActivityOptions(
		ctx,
		workflow.ActivityOptions{StartToCloseTimeout: enqueueActivityTimeout, RetryPolicy: retries},
	)
	err := workflow.ExecuteActivity(ctx, acts.Enqueue, envelope.City, envelope.RawIngestID).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("enqueue raw ingest %s: %w", envelope.RawIngestID, err)
	}
	return nil
}
