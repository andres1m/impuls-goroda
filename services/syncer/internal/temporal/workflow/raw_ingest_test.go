package workflow

import (
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

func TestProcessRawIngestCompletes(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(ProcessRawIngest, ingest.Envelope{
		Version: ingest.EnvelopeVersion, RawIngestID: "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a", Source: domain.KudaGo,
		City: domain.Moscow, ExternalID: "event:1", FetchedAt: time.Now().UTC(), DataMode: domain.Live,
	})
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("completed = %v, err = %v", env.IsWorkflowCompleted(), env.GetWorkflowError())
	}
}
