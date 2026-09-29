package temporal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

var envelope = ingest.Envelope{
	Version:     ingest.EnvelopeVersion,
	RawIngestID: "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a",
	Source:      domain.KudaGo,
	City:        domain.Moscow,
	ExternalID:  "event:1",
	DataMode:    domain.Live,
}

func startWith(t *testing.T, err error) (*mocks.Client, bool, error) {
	t.Helper()
	fake := &mocks.Client{}
	fake.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, err).Once()
	existed, startErr := NewStarter(
		func() client.Client { return fake },
		"catalog-sync",
	).Start(context.Background(), &envelope)
	return fake, existed, startErr
}

func TestStartUsesStableIDAndRejectsDuplicates(t *testing.T) {
	fake, existed, err := startWith(t, nil)
	if err != nil || existed {
		t.Fatalf("existed = %v, err = %v", existed, err)
	}
	call := fake.Calls[0]
	o, ok := call.Arguments.Get(1).(client.StartWorkflowOptions)
	if !ok || o.ID != "raw:kudago:0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a" || o.TaskQueue != "catalog-sync" ||
		o.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE ||
		o.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL ||
		!o.WorkflowExecutionErrorWhenAlreadyStarted {
		t.Fatalf("options = %+v", o)
	}
	arg, ok := call.Arguments.Get(3).(*ingest.Envelope)
	if !ok || *arg != envelope {
		t.Fatalf("arg = %+v", call.Arguments.Get(3))
	}
}

func TestStartTreatsAlreadyStartedAsExisting(t *testing.T) {
	_, existed, err := startWith(
		t,
		serviceerror.NewWorkflowExecutionAlreadyStarted("exists", "", "run-1"),
	)
	if err != nil || !existed {
		t.Fatalf("existed = %v, err = %v", existed, err)
	}
}

func TestStartReturnsOtherErrors(t *testing.T) {
	cause := errors.New("connection refused")
	_, existed, err := startWith(t, cause)
	if !errors.Is(err, cause) || existed {
		t.Fatalf("existed = %v, err = %v", existed, err)
	}
}
