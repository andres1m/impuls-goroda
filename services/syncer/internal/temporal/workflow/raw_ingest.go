package workflow

import (
	"go.temporal.io/sdk/workflow"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

func ProcessRawIngest(ctx workflow.Context, envelope ingest.Envelope) error {
	workflow.GetLogger(ctx).Info("raw ingest received", "raw_ingest_id", envelope.RawIngestID, "source", envelope.Source)
	return nil
}
