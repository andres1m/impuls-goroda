package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
)

type fakeGroup struct {
	polls       []kgo.Fetches
	cancel      context.CancelFunc
	committed   [][]*kgo.Record
	commitErr   error
	allowed     int
	closeBlocks chan struct{}
}

func (g *fakeGroup) Ping(context.Context) error { return nil }

func (g *fakeGroup) PollFetches(context.Context) kgo.Fetches {
	if len(g.polls) == 0 {
		g.cancel()
		return nil
	}
	next := g.polls[0]
	g.polls = g.polls[1:]
	return next
}

func (g *fakeGroup) CommitRecords(_ context.Context, records ...*kgo.Record) error {
	g.committed = append(g.committed, records)
	return g.commitErr
}

func (g *fakeGroup) AllowRebalance() { g.allowed++ }
func (g *fakeGroup) CloseAllowingRebalance() {
	if g.closeBlocks != nil {
		<-g.closeBlocks
	}
}

type fakeStarter struct {
	calls int
	start func(call int) (bool, error)
}

func (s *fakeStarter) Start(context.Context, *ingest.Envelope) (bool, error) {
	s.calls++
	return s.start(s.calls)
}

func pollOf(values ...[]byte) kgo.Fetches {
	records := make([]*kgo.Record, len(values))
	for i, v := range values {
		records[i] = &kgo.Record{Topic: "integration.raw", Value: v, Offset: int64(i)}
	}
	return kgo.Fetches{
		{Topics: []kgo.FetchTopic{{Topic: "integration.raw", Partitions: []kgo.FetchPartition{{Records: records}}}}},
	}
}

func envelopeBytes(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal(testEnvelope("0b5c3f6e-2d1a-4c8e-9f3b-7a6d5e4c3b2a"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runConsumer(t *testing.T, group *fakeGroup, starter *fakeStarter) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	group.cancel = cancel
	consumer := NewConsumer(zap.NewNop(), testConfig("unused:9092"), starter)
	consumer.client = group
	consumer.retryDelay = func(int) time.Duration { return 0 }
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func counter(result string) float64 { return testutil.ToFloat64(rawWorkflows.WithLabelValues(result)) }
func mismatches(source string) float64 {
	return testutil.ToFloat64(ingest.SchemaMismatch.WithLabelValues(source))
}

func TestConsumerCommitsAfterStart(t *testing.T) {
	before := counter("started")
	group := &fakeGroup{polls: []kgo.Fetches{pollOf(envelopeBytes(t))}}
	starter := &fakeStarter{start: func(int) (bool, error) { return false, nil }}

	runConsumer(t, group, starter)
	// Two polls: the one with the record and the final one that ends on cancellation.
	if starter.calls != 1 || len(group.committed) != 1 || len(group.committed[0]) != 1 || group.allowed != 2 {
		t.Fatalf("calls = %d, committed = %d, allowed = %d", starter.calls, len(group.committed), group.allowed)
	}
	if counter("started") != before+1 {
		t.Fatal("started counter not incremented")
	}
}

func TestConsumerCommitsExistingWorkflow(t *testing.T) {
	before := counter("existing")
	group := &fakeGroup{polls: []kgo.Fetches{pollOf(envelopeBytes(t))}}
	runConsumer(t, group, &fakeStarter{start: func(int) (bool, error) { return true, nil }})
	if len(group.committed) != 1 || counter("existing") != before+1 {
		t.Fatalf("committed = %d, existing delta = %v", len(group.committed), counter("existing")-before)
	}
}

func TestConsumerRetriesStartBeforeCommit(t *testing.T) {
	group := &fakeGroup{polls: []kgo.Fetches{pollOf(envelopeBytes(t))}}
	starter := &fakeStarter{start: func(call int) (bool, error) {
		if call < 3 {
			return false, errors.New("temporal unavailable")
		}
		return false, nil
	}}
	runConsumer(t, group, starter)
	if starter.calls != 3 || len(group.committed) != 1 {
		t.Fatalf("calls = %d, committed = %d", starter.calls, len(group.committed))
	}
}

func TestConsumerStopsRetryingWithoutCommit(t *testing.T) {
	group := &fakeGroup{polls: []kgo.Fetches{pollOf(envelopeBytes(t))}}
	starter := &fakeStarter{}
	starter.start = func(call int) (bool, error) {
		if call == 2 {
			group.cancel()
		}
		return false, errors.New("temporal unavailable")
	}
	runConsumer(t, group, starter)
	if len(group.committed) != 0 {
		t.Fatalf("committed %d batches while shutting down", len(group.committed))
	}
}

func TestConsumerCountsAndSkipsInvalidEnvelopes(t *testing.T) {
	unknown, kudago := mismatches("unknown"), mismatches("kudago")
	group := &fakeGroup{polls: []kgo.Fetches{pollOf(
		[]byte(`not json`),
		[]byte(`{"version":2,"source":"kudago"}`),
		[]byte(`{"version":1,"source":"<arbitrary text>"}`),
	)}}
	starter := &fakeStarter{start: func(int) (bool, error) { return false, nil }}

	runConsumer(t, group, starter)
	if starter.calls != 0 || len(group.committed) != 1 || len(group.committed[0]) != 3 {
		t.Fatalf("calls = %d, committed = %+v", starter.calls, group.committed)
	}
	if mismatches("unknown") != unknown+2 || mismatches("kudago") != kudago+1 {
		t.Fatalf("unknown +%v, kudago +%v", mismatches("unknown")-unknown, mismatches("kudago")-kudago)
	}
}

func TestConsumerContinuesAfterCommitFailure(t *testing.T) {
	group := &fakeGroup{
		polls:     []kgo.Fetches{pollOf(envelopeBytes(t)), pollOf(envelopeBytes(t))},
		commitErr: errors.New("rebalance in progress"),
	}
	starter := &fakeStarter{start: func(int) (bool, error) { return true, nil }}
	runConsumer(t, group, starter)
	if starter.calls != 2 || len(group.committed) != 2 {
		t.Fatalf("calls = %d, committed = %d", starter.calls, len(group.committed))
	}
}

// kgo counts a poll that returns on a cancelled context as an active poller, and leaving
// the group on close waits for it, so Run must release it on the way out.
func TestConsumerReleasesRebalanceOnExit(t *testing.T) {
	group := &fakeGroup{}
	runConsumer(t, group, &fakeStarter{})
	if group.allowed != 1 {
		t.Fatalf("allowed = %d after the final poll", group.allowed)
	}
}

func TestConsumerStopHonorsDeadline(t *testing.T) {
	group := &fakeGroup{closeBlocks: make(chan struct{})}
	defer close(group.closeBlocks)
	consumer := NewConsumer(zap.NewNop(), testConfig("unused:9092"), &fakeStarter{})
	consumer.client = group

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- consumer.Stop(ctx) }()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stop = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop ignored its deadline")
	}
}
