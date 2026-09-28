package activity

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
)

type signalCall struct {
	workflowID, signal string
	arg                any
	options            client.StartWorkflowOptions
	workflow           any
	args               []any
}

type fakeClient struct {
	client.Client
	calls []signalCall
}

func (f *fakeClient) SignalWithStartWorkflow(_ context.Context, workflowID, signalName string, signalArg any, options client.StartWorkflowOptions, workflow any, workflowArgs ...any) (client.WorkflowRun, error) {
	f.calls = append(f.calls, signalCall{workflowID, signalName, signalArg, options, workflow, workflowArgs})
	return nil, nil
}

func TestEnqueueSignalsTheCityWorkflow(t *testing.T) {
	c := &fakeClient{}
	a := &Activities{Client: func() client.Client { return c }, Queue: "catalog-sync"}
	if err := a.Enqueue(context.Background(), domain.Perm, "raw-1"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 1 {
		t.Fatalf("calls %d", len(c.calls))
	}
	call := c.calls[0]
	if call.workflowID != "materialize:perm" || call.signal != MaterializeSignal || call.arg != "raw-1" ||
		call.options.ID != "materialize:perm" || call.options.TaskQueue != "catalog-sync" || call.workflow != MaterializeWorkflowName {
		t.Fatalf("call %+v", call)
	}
	if len(call.args) != 2 || call.args[0] != domain.Perm || call.args[1].([]string) != nil {
		t.Fatalf("workflow args %#v", call.args)
	}
}

type fakeStore struct{ asked []string }

func (f *fakeStore) PendingBatch(_ context.Context, _ domain.City, ids []string) ([]materialize.Raw, error) {
	f.asked = ids
	return nil, nil
}

func (f *fakeStore) Publish(context.Context, domain.City, materialize.Outcome, time.Time) (int64, bool, error) {
	return 0, false, nil
}

func TestApplyBatchMaterializesTheIDs(t *testing.T) {
	s := &fakeStore{}
	a := &Activities{Store: s, Now: time.Now}
	if _, err := a.ApplyBatch(context.Background(), domain.Perm, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.asked, []string{"a", "b"}) {
		t.Fatalf("asked %v", s.asked)
	}
}

func TestSignalMaterializeUsesTheQueue(t *testing.T) {
	c := &fakeClient{}
	if err := SignalMaterialize(context.Background(), c, "q", domain.Moscow, "raw-2"); err != nil {
		t.Fatal(err)
	}
	if len(c.calls) != 1 || c.calls[0].workflowID != "materialize:moscow" || c.calls[0].options.TaskQueue != "q" || c.calls[0].arg != "raw-2" {
		t.Fatalf("calls %+v", c.calls)
	}
}
