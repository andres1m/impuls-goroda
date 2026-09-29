package temporal

import (
	"context"
	"errors"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/workflow"
)

func WorkflowID(e *ingest.Envelope) string {
	return "raw:" + string(e.Source) + ":" + e.RawIngestID
}

type Starter struct {
	client func() client.Client
	queue  string
}

func NewStarter(getClient func() client.Client, queue string) *Starter {
	return &Starter{client: getClient, queue: queue}
}

// Start reports existed when a workflow with this ID is running or already finished:
// a redelivered envelope must not process the same raw ingest twice.
func (s *Starter) Start(ctx context.Context, e *ingest.Envelope) (bool, error) {
	_, err := s.client().ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                                       WorkflowID(e),
		TaskQueue:                                s.queue,
		WorkflowIDReusePolicy:                    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowIDConflictPolicy:                 enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}, workflow.ProcessRawIngest, e)
	if alreadyStarted, ok := errors.AsType[*serviceerror.WorkflowExecutionAlreadyStarted](err); ok {
		return alreadyStarted.Error() != "", nil
	}
	if err != nil {
		return false, fmt.Errorf("start workflow %s: %w", WorkflowID(e), err)
	}
	return false, nil
}
