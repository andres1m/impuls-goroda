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

	"github.com/andres1m/impuls-goroda/services/syncer/internal/domain"
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

type fakeDeadLetters struct {
	letters []ingest.DeadLetter
	keys    [][]byte
	calls   int
	write   func(call int) error
}

func (d *fakeDeadLetters) Write(_ context.Context, key []byte, letter *ingest.DeadLetter) error {
	d.calls++
	if d.write != nil {
		if err := d.write(d.calls); err != nil {
			return err
		}
	}
	d.letters = append(d.letters, *letter)
	d.keys = append(d.keys, key)
	return nil
}

func runConsumer(t *testing.T, group *fakeGroup, starter *fakeStarter) {
	t.Helper()
	runConsumerWith(t, group, starter, &fakeDeadLetters{})
}

func runConsumerWith(t *testing.T, group *fakeGroup, starter *fakeStarter, letters *fakeDeadLetters) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	group.cancel = cancel
	consumer := NewConsumer(zap.NewNop(), testConfig("unused:9092"), starter, letters)
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

func TestConsumerSendsInvalidEnvelopesToDeadLetters(t *testing.T) {
	unknown, kudago := mismatches("unknown"), mismatches("kudago")
	values := [][]byte{
		[]byte(`not json`),
		[]byte(`{"version":2,"source":"kudago"}`),
		[]byte(`{"version":1,"source":"<arbitrary text>"}`),
	}
	poll := pollOf(values...)
	poll[0].Topics[0].Partitions[0].Records[0].Key = []byte("perm:node/1")
	group := &fakeGroup{polls: []kgo.Fetches{poll}}
	starter := &fakeStarter{start: func(int) (bool, error) { return false, nil }}
	letters := &fakeDeadLetters{}

	runConsumerWith(t, group, starter, letters)
	if starter.calls != 0 || len(group.committed) != 1 || len(group.committed[0]) != 3 || len(letters.letters) != 3 {
		t.Fatalf("calls = %d, committed = %+v, letters = %d", starter.calls, group.committed, len(letters.letters))
	}
	for i, letter := range letters.letters {
		if letter.Stage != ingest.StageEnvelope || letter.Reason != domain.InvalidSchema ||
			string(letter.Record) != string(values[i]) || letter.Error == "" {
			t.Fatalf("letter %d = %+v", i, letter)
		}
	}
	if string(letters.keys[0]) != "perm:node/1" || letters.letters[1].Source != domain.KudaGo ||
		letters.letters[2].Source != "unknown" {
		t.Fatalf("keys %q, letters %+v", letters.keys, letters.letters)
	}
	if mismatches("unknown") != unknown+2 || mismatches("kudago") != kudago+1 {
		t.Fatalf("unknown +%v, kudago +%v", mismatches("unknown")-unknown, mismatches("kudago")-kudago)
	}
}

func TestConsumerRetriesDeadLetterBeforeCommit(t *testing.T) {
	group := &fakeGroup{polls: []kgo.Fetches{pollOf([]byte(`not json`))}}
	letters := &fakeDeadLetters{write: func(call int) error {
		if call < 3 {
			return errors.New("dead letter topic unavailable")
		}
		return nil
	}}
	runConsumerWith(t, group, &fakeStarter{}, letters)
	if letters.calls != 3 || len(letters.letters) != 1 || len(group.committed) != 1 {
		t.Fatalf("calls = %d, letters = %d, committed = %d", letters.calls, len(letters.letters), len(group.committed))
	}
}

func TestConsumerStopsDeadLetterRetryWithoutCommit(t *testing.T) {
	group := &fakeGroup{polls: []kgo.Fetches{pollOf([]byte(`not json`))}}
	letters := &fakeDeadLetters{}
	letters.write = func(call int) error {
		if call == 2 {
			group.cancel()
		}
		return errors.New("dead letter topic unavailable")
	}
	runConsumerWith(t, group, &fakeStarter{}, letters)
	if len(group.committed) != 0 {
		t.Fatalf("committed %d batches without the dead letter", len(group.committed))
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
	consumer := NewConsumer(zap.NewNop(), testConfig("unused:9092"), &fakeStarter{}, &fakeDeadLetters{})
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
