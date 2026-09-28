package kafka

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"

	"github.com/andres1m/impuls-goroda/services/syncer/internal/ingest"
	rawtemporal "github.com/andres1m/impuls-goroda/services/syncer/internal/temporal"
	"github.com/andres1m/impuls-goroda/services/syncer/internal/temporal/workflow"
)

func TestHandoffIntegration(t *testing.T) {
	brokers := kafkaBrokers(t)
	hostPort := os.Getenv("SYNCER_TEST_TEMPORAL_HOSTPORT")
	if hostPort == "" {
		t.Skip("SYNCER_TEST_TEMPORAL_HOSTPORT is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	suffix := randomSuffix(t)

	temporalClient, err := client.DialContext(ctx, client.Options{HostPort: hostPort})
	if err != nil {
		t.Fatal(err)
	}
	defer temporalClient.Close()
	queue := "syncer-test-" + suffix
	w := worker.New(temporalClient, queue, worker.Options{})
	w.RegisterWorkflow(workflow.ProcessRawIngest)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	cfg := testConfig(brokers...)
	cfg.RawTopic = "integration.raw.test-" + suffix
	producer, err := NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	envelope := testEnvelope(uuid.NewString())
	if err := producer.Publish(ctx, []ingest.Envelope{envelope}); err != nil {
		t.Fatal(err)
	}

	starter := rawtemporal.NewStarter(func() client.Client { return temporalClient }, queue)
	consumeUntil := func(group string, done func() bool) {
		t.Helper()
		groupCfg := cfg
		groupCfg.ConsumerGroup = group
		consumer := NewConsumer(zap.NewNop(), groupCfg, starter)
		if err := consumer.Init(ctx); err != nil {
			t.Fatal(err)
		}
		runCtx, stop := context.WithCancel(ctx)
		finished := make(chan error, 1)
		go func() { finished <- consumer.Run(runCtx) }()
		for !done() {
			if ctx.Err() != nil {
				t.Fatal("timed out waiting for the consumer")
			}
			time.Sleep(200 * time.Millisecond)
		}
		stop()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if err := consumer.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
	id := rawtemporal.WorkflowID(envelope)
	describe := func() (string, enumspb.WorkflowExecutionStatus) {
		resp, err := temporalClient.DescribeWorkflowExecution(ctx, id, "")
		if err != nil {
			return "", enumspb.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED
		}
		info := resp.GetWorkflowExecutionInfo()
		return info.GetExecution().GetRunId(), info.GetStatus()
	}

	started := counter("started")
	consumeUntil("syncer-test-a-"+suffix, func() bool {
		_, status := describe()
		return status == enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED
	})
	firstRun, _ := describe()
	if counter("started") != started+1 {
		t.Fatalf("started delta = %v", counter("started")-started)
	}

	// The same envelope published again and a new group reading from the start replay a
	// consumer that crashed after starting the workflow but before committing.
	if err := producer.Publish(ctx, []ingest.Envelope{envelope}); err != nil {
		t.Fatal(err)
	}
	existing := counter("existing")
	consumeUntil("syncer-test-b-"+suffix, func() bool { return counter("existing") >= existing+2 })
	if run, _ := describe(); run != firstRun || counter("started") != started+1 {
		t.Fatalf("run %s → %s, started delta %v", firstRun, run, counter("started")-started)
	}
}
