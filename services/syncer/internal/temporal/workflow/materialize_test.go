package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/activity"
)

type materializeRun struct {
	env     *testsuite.TestWorkflowEnvironment
	batches [][]string
	at      []time.Duration
	start   time.Time
}

func newMaterializeRun() *materializeRun {
	var suite testsuite.WorkflowTestSuite
	r := &materializeRun{env: suite.NewTestWorkflowEnvironment()}
	r.env.RegisterActivity(acts)
	r.env.OnActivity(acts.ApplyBatch, mock.Anything, domain.Perm, mock.Anything).Return(
		func(_ context.Context, _ domain.City, ids []string) (materialize.Result, error) {
			r.batches = append(r.batches, slices.Clone(ids))
			r.at = append(r.at, r.env.Now().Sub(r.start))
			return materialize.Result{Applied: len(ids)}, nil
		}).Maybe()
	r.start = r.env.Now()
	return r
}

func (r *materializeRun) signal(after time.Duration, ids ...string) {
	r.env.RegisterDelayedCallback(func() {
		for _, id := range ids {
			r.env.SignalWorkflow(activity.MaterializeSignal, id)
		}
	}, after)
}

func (r *materializeRun) run(t *testing.T, carried []string) {
	t.Helper()
	r.env.ExecuteWorkflow(MaterializeCity, domain.Perm, carried)
	if !r.env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
}

func ids(from, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("raw-%d", from+i)
	}
	return out
}

func TestMaterializeFlushesAFullBatchAtOnce(t *testing.T) {
	r := newMaterializeRun()
	r.signal(time.Second, ids(0, 500)...)
	r.run(t, nil)
	if r.env.GetWorkflowError() != nil || len(r.batches) != 1 || len(r.batches[0]) != 500 || r.at[0] != time.Second {
		t.Fatalf("batches %d at %v, err %v", len(r.batches), r.at, r.env.GetWorkflowError())
	}
}

func TestMaterializeFlushesAfterQuiet(t *testing.T) {
	r := newMaterializeRun()
	r.signal(time.Second, "a", "b", "a")
	r.run(t, nil)
	if len(r.batches) != 1 || !slices.Equal(r.batches[0], []string{"a", "b"}) || r.at[0] != 3*time.Second {
		t.Fatalf("batches %v at %v", r.batches, r.at)
	}
}

func TestMaterializeFlushesAtTheLatestTenSecondsAfterTheFirstID(t *testing.T) {
	r := newMaterializeRun()
	for i := range 15 {
		r.signal(time.Duration(i+1)*time.Second, strconv.Itoa(i))
	}
	r.run(t, nil)
	if len(r.batches) < 2 || r.at[0] != 11*time.Second || len(r.batches[0]) != 10 {
		t.Fatalf("batches %v at %v", r.batches, r.at)
	}
}

func TestMaterializeStartsWithCarriedIDs(t *testing.T) {
	r := newMaterializeRun()
	r.run(t, []string{"x", "y"})
	if len(r.batches) != 1 || !slices.Equal(r.batches[0], []string{"x", "y"}) {
		t.Fatalf("batches %v", r.batches)
	}
}

func TestMaterializeEndsAfterAnIdleMinute(t *testing.T) {
	r := newMaterializeRun()
	r.signal(time.Second, "a")
	r.signal(50*time.Second, "b")
	r.run(t, nil)
	if r.env.GetWorkflowError() != nil || len(r.batches) != 2 {
		t.Fatalf("batches %v, err %v", r.batches, r.env.GetWorkflowError())
	}
	if end := r.env.Now().Sub(r.start); end < 52*time.Second+time.Minute || end > 53*time.Second+time.Minute {
		t.Fatalf("ended after %v", end)
	}
}

func TestMaterializeKeepsGoingWhenABatchFails(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(acts)
	var calls int
	env.OnActivity(acts.ApplyBatch, mock.Anything, domain.Perm, mock.Anything).Return(
		func(_ context.Context, _ domain.City, ids []string) (materialize.Result, error) {
			calls++
			if ids[0] == "bad" {
				return materialize.Result{}, temporal.NewNonRetryableApplicationError(
					"down",
					"test",
					errors.New("down"),
				)
			}
			return materialize.Result{}, nil
		})
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(activity.MaterializeSignal, "bad") }, time.Second)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(activity.MaterializeSignal, "good") }, 10*time.Second)
	env.ExecuteWorkflow(MaterializeCity, domain.Perm, []string(nil))
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil || calls != 2 {
		t.Fatalf("calls %d, err %v", calls, env.GetWorkflowError())
	}
}

func TestMaterializeContinuesAsNewAfterManyBatches(t *testing.T) {
	r := newMaterializeRun()
	for i := range batchesPerRun + 1 {
		r.signal(time.Duration(i*5+1)*time.Second, strconv.Itoa(i))
	}
	r.env.ExecuteWorkflow(MaterializeCity, domain.Perm, []string(nil))
	var next *workflow.ContinueAsNewError
	if !errors.As(r.env.GetWorkflowError(), &next) || len(r.batches) != batchesPerRun {
		t.Fatalf("batches %d, err %v", len(r.batches), r.env.GetWorkflowError())
	}
}

func TestProcessRawIngestEnqueuesTheRecord(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(acts)
	env.OnActivity(acts.Enqueue, mock.Anything, domain.Moscow, "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a").
		Return(nil).
		Once()
	env.ExecuteWorkflow(ProcessRawIngest, &ingest.Envelope{
		Version: ingest.EnvelopeVersion, RawIngestID: "0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a", Source: domain.KudaGo,
		City: domain.Moscow, ExternalID: "event:1", FetchedAt: time.Now().UTC(), DataMode: domain.Live,
	})
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() != nil {
		t.Fatalf("completed = %v, err = %v", env.IsWorkflowCompleted(), env.GetWorkflowError())
	}
	env.AssertExpectations(t)
}

func TestMaterializeContinuesAsNewBeforeTooManySignals(t *testing.T) {
	defer func(n int) { signalsPerRun = n }(signalsPerRun)
	signalsPerRun = 30
	r := newMaterializeRun()
	r.signal(time.Second, ids(0, signalsPerRun+100)...)
	r.env.ExecuteWorkflow(MaterializeCity, domain.Perm, []string(nil))
	var next *workflow.ContinueAsNewError
	if !errors.As(r.env.GetWorkflowError(), &next) {
		t.Fatalf("err %v", r.env.GetWorkflowError())
	}
	var city domain.City
	var carried []string
	if err := converter.GetDefaultDataConverter().FromPayloads(next.Input, &city, &carried); err != nil {
		t.Fatal(err)
	}
	applied := 0
	for _, b := range r.batches {
		applied += len(b)
	}
	// The test environment drops signals sent after the run ends; the server hands them to the next run.
	if city != domain.Perm || applied+len(carried) != signalsPerRun || len(carried) == 0 {
		t.Fatalf("applied %d, carried %d", applied, len(carried))
	}
}
