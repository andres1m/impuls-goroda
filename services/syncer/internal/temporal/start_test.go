package temporal

import (
	"context"
	"errors"
	"testing"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

type fakeClient struct {
	client.Client
	options client.StartWorkflowOptions
	args    []any
	err     error
}

func (f *fakeClient) ExecuteWorkflow(_ context.Context, options client.StartWorkflowOptions, _ any, args ...any) (client.WorkflowRun, error) {
	f.options, f.args = options, args
	return nil, f.err
}

var envelope = ingest.Envelope{Version: ingest.EnvelopeVersion, RawIngestID: "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a", Source: domain.KudaGo, City: domain.Moscow, ExternalID: "event:1", DataMode: domain.Live}

func startWith(t *testing.T, fake *fakeClient) (bool, error) {
	t.Helper()
	return NewStarter(func() client.Client { return fake }, "catalog-sync").Start(context.Background(), envelope)
}

func TestStartUsesStableIDAndRejectsDuplicates(t *testing.T) {
	fake := &fakeClient{}
	existed, err := startWith(t, fake)
	if err != nil || existed {
		t.Fatalf("existed = %v, err = %v", existed, err)
	}
	o := fake.options
	if o.ID != "raw:kudago:0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a" || o.TaskQueue != "catalog-sync" ||
		o.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE ||
		o.WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL ||
		!o.WorkflowExecutionErrorWhenAlreadyStarted {
		t.Fatalf("options = %+v", o)
	}
	if len(fake.args) != 1 || fake.args[0] != envelope {
		t.Fatalf("args = %+v", fake.args)
	}
}

func TestStartTreatsAlreadyStartedAsExisting(t *testing.T) {
	existed, err := startWith(t, &fakeClient{err: serviceerror.NewWorkflowExecutionAlreadyStarted("exists", "", "run-1")})
	if err != nil || !existed {
		t.Fatalf("existed = %v, err = %v", existed, err)
	}
}

func TestStartReturnsOtherErrors(t *testing.T) {
	cause := errors.New("connection refused")
	existed, err := startWith(t, &fakeClient{err: cause})
	if !errors.Is(err, cause) || existed {
		t.Fatalf("existed = %v, err = %v", existed, err)
	}
}
