package activity

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/materialize"
)

func TestEnqueueSignalsTheCityWorkflow(t *testing.T) {
	c := &mocks.Client{}
	c.On(
		"SignalWithStartWorkflow",
		mock.Anything,
		"materialize:perm",
		MaterializeSignal,
		"raw-1",
		client.StartWorkflowOptions{ID: "materialize:perm", TaskQueue: "catalog-sync"},
		MaterializeWorkflowName,
		domain.Perm,
		[]string(nil),
	).Return(nil, nil).Once()

	a := &Activities{Client: func() client.Client { return c }, Queue: "catalog-sync"}
	if err := a.Enqueue(context.Background(), domain.Perm, "raw-1"); err != nil {
		t.Fatal(err)
	}
	c.AssertExpectations(t)
}

type fakeStore struct{ asked []string }

func (f *fakeStore) PendingBatch(_ context.Context, _ domain.City, ids []string) ([]materialize.Raw, error) {
	f.asked = ids
	return nil, nil
}

func (f *fakeStore) Publish(
	context.Context,
	domain.City,
	*materialize.Outcome,
	time.Time,
) (revision int64, published bool, err error) {
	return 0, false, nil
}

func (f *fakeStore) OutsideBoundary(
	context.Context,
	domain.City,
	[]materialize.Point,
) (outside []bool, known bool, err error) {
	return nil, false, nil
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
	c := &mocks.Client{}
	c.On("SignalWithStartWorkflow", mock.Anything, "materialize:moscow", MaterializeSignal, "raw-2", client.StartWorkflowOptions{ID: "materialize:moscow", TaskQueue: "q"}, MaterializeWorkflowName, domain.Moscow, []string(nil)).
		Return(nil, nil).
		Once()
	if err := SignalMaterialize(context.Background(), c, "q", domain.Moscow, "raw-2"); err != nil {
		t.Fatal(err)
	}
	c.AssertExpectations(t)
}
