package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/framework/observability"
)

type jobObservationSink struct {
	mu      sync.Mutex
	spans   []observability.SpanRecord
	metrics []observability.Metric
}

func (sink *jobObservationSink) RecordSpan(record observability.SpanRecord) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.spans = append(sink.spans, record)
}

func (sink *jobObservationSink) RecordMetric(metric observability.Metric) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.metrics = append(sink.metrics, metric)
}

func (sink *jobObservationSink) snapshot() (
	[]observability.SpanRecord,
	[]observability.Metric,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]observability.SpanRecord(nil), sink.spans...),
		append([]observability.Metric(nil), sink.metrics...)
}

type tracingQueue struct {
	mu              sync.Mutex
	enqueueRequest  EnqueueRequest
	failSpanContext observability.SpanContext
}

func (queue *tracingQueue) Enqueue(
	ctx context.Context,
	request EnqueueRequest,
) (EnqueueResult, error) {
	if _, ok := observability.SpanContextFromContext(ctx); !ok {
		return EnqueueResult{}, errors.New("producer context is missing")
	}
	queue.mu.Lock()
	queue.enqueueRequest = request
	queue.mu.Unlock()
	return EnqueueResult{
		Created: true,
		Job: Job{
			Type:    request.Type,
			Version: request.Version,
			Trace:   request.Trace,
		},
	}, nil
}

func (queue *tracingQueue) Claim(
	context.Context,
	ClaimRequest,
) ([]Job, error) {
	return []Job{}, nil
}

func (queue *tracingQueue) Heartbeat(
	context.Context,
	string,
	string,
) error {
	return nil
}

func (queue *tracingQueue) Succeed(
	context.Context,
	string,
	string,
) error {
	return nil
}

func (queue *tracingQueue) Fail(
	ctx context.Context,
	_,
	_ string,
	_ error,
) (State, error) {
	current, ok := observability.SpanContextFromContext(ctx)
	if !ok {
		return "", errors.New("settlement context is missing")
	}
	queue.mu.Lock()
	queue.failSpanContext = current
	queue.mu.Unlock()
	return StateFailed, nil
}

func (queue *tracingQueue) RetryDead(context.Context, string) error {
	return nil
}

func (queue *tracingQueue) captured() (
	EnqueueRequest,
	observability.SpanContext,
) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.enqueueRequest, queue.failSpanContext
}

func TestEnqueueAndRunnerPreserveTraceWithoutSensitiveDimensions(t *testing.T) {
	sink := &jobObservationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	rawQueue := &tracingQueue{}
	queue, err := ObserveQueue(rawQueue, recorder)
	if err != nil {
		t.Fatal(err)
	}
	upstreamContext, upstreamSpan := recorder.Start(
		context.Background(),
		observability.SpanStart{
			Name: "http.request",
			Kind: observability.SpanKindServer,
		},
	)
	result, err := queue.Enqueue(upstreamContext, EnqueueRequest{
		Type:           "storage.cleanup",
		Version:        1,
		Payload:        json.RawMessage(`{"marker":"payload-secret-marker"}`),
		IdempotencyKey: "idempotency-secret-marker",
		CreatedBy:      authz.NewUserActor("user-secret-marker"),
		Trace: TraceContext{
			RequestID: "request-secret-marker",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Fatal("enqueue result was not created")
	}
	capturedRequest, _ := rawQueue.captured()
	if capturedRequest.Trace.TraceParent == "" {
		t.Fatal("enqueue did not persist a traceparent")
	}

	registry := module.NewRegistry()
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    "storage.cleanup",
		Version: 1,
		Handle: func(context.Context, json.RawMessage) error {
			return errors.New("handler-secret-marker")
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(rawQueue, registry, RunnerConfig{
		WorkerID:          "worker-test",
		Concurrency:       1,
		PollInterval:      time.Millisecond,
		LeaseDuration:     time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
		OperationTimeout:  time.Second,
		ShutdownTimeout:   time.Second,
		Recorder:          recorder,
	})
	if err != nil {
		t.Fatal(err)
	}
	job := testJob("job-secret-marker")
	job.IdempotencyKey = "idempotency-secret-marker"
	job.CreatedBy = authz.NewUserActor("user-secret-marker")
	job.Trace = capturedRequest.Trace
	if err := runner.execute(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	upstreamSpan.End(observability.SpanEnd{Outcome: observability.OutcomeOK})

	spans, metrics := sink.snapshot()
	producer := requireJobSpan(t, spans, "jobs.enqueue")
	consumer := requireJobSpan(t, spans, "jobs.process")
	settlement := requireJobSpan(t, spans, "jobs.settle")
	if producer.Kind != observability.SpanKindProducer ||
		consumer.Kind != observability.SpanKindConsumer {
		t.Fatalf(
			"span kinds = producer %q consumer %q",
			producer.Kind,
			consumer.Kind,
		)
	}
	if capturedRequest.Trace.TraceParent != producer.Context.TraceParent() {
		t.Fatalf(
			"persisted traceparent = %q, want producer %q",
			capturedRequest.Trace.TraceParent,
			producer.Context.TraceParent(),
		)
	}
	if producer.Context.TraceID() != consumer.Context.TraceID() ||
		consumer.Context.TraceID() != settlement.Context.TraceID() {
		t.Fatal("producer, consumer, and settlement did not share a trace")
	}
	if !consumer.HasParent ||
		consumer.Parent.SpanID() != producer.Context.SpanID() {
		t.Fatal("consumer was not parented to the producer")
	}
	if !settlement.HasParent ||
		settlement.Parent.SpanID() != consumer.Context.SpanID() {
		t.Fatal("settlement was not parented to the consumer")
	}
	_, failContext := rawQueue.captured()
	if failContext.SpanID() != settlement.Context.SpanID() {
		t.Fatal("queue failure did not receive the settlement span context")
	}
	if consumer.Outcome != observability.OutcomeError ||
		consumer.Error != errObservedJobOperation.Error() {
		t.Fatalf("consumer outcome = %#v", consumer)
	}

	for _, span := range spans {
		assertNoJobSecret(
			t,
			span.Name+span.Error,
			span.Attributes.Values(),
		)
	}
	for _, metric := range metrics {
		assertNoJobSecret(t, metric.Name, metric.Attributes.Values())
	}
}

func TestRunnerRecordsClaimHeartbeatHandlerSettlementAndActiveMetrics(
	t *testing.T,
) {
	sink := &jobObservationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	queue := newFakeQueue(testJob("job-1"))
	registry := module.NewRegistry()
	release := make(chan struct{})
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    "storage.cleanup",
		Version: 1,
		Handle: func(ctx context.Context, _ json.RawMessage) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	queue.onSecondHeartbeat = func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}
	runner := newTestRunner(t, queue, registry, func(config *RunnerConfig) {
		config.HeartbeatInterval = 5 * time.Millisecond
		config.LeaseDuration = 100 * time.Millisecond
		config.Recorder = recorder
	})

	cancel, result := startTestRunner(t, runner)
	waitFor(t, time.Second, func() bool {
		return queue.succeededCount() == 1
	})
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	_, metrics := sink.snapshot()
	names := make(map[string]int)
	for _, metric := range metrics {
		names[metric.Name]++
	}
	for _, name := range []string{
		"jobs.worker.claim.requests",
		"jobs.worker.claim.duration",
		"jobs.worker.claimed",
		"jobs.worker.heartbeat.requests",
		"jobs.worker.heartbeat.duration",
		"jobs.handler.executions",
		"jobs.handler.duration",
		"jobs.worker.settlement.requests",
		"jobs.worker.settlement.duration",
		"jobs.worker.active",
	} {
		if names[name] == 0 {
			t.Errorf("metric %q was not recorded", name)
		}
	}
}

func TestObserveQueueRejectsNil(t *testing.T) {
	if _, err := ObserveQueue(nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ObserveQueue(nil) error = %v, want ErrInvalid", err)
	}
	var typedNil *tracingQueue
	if _, err := ObserveQueue(typedNil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ObserveQueue(typed nil) error = %v, want ErrInvalid", err)
	}
}

func requireJobSpan(
	t *testing.T,
	spans []observability.SpanRecord,
	name string,
) observability.SpanRecord {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q was not recorded", name)
	return observability.SpanRecord{}
}

func assertNoJobSecret(
	t *testing.T,
	text string,
	attributes map[string]string,
) {
	t.Helper()
	for key, value := range attributes {
		text += key + value
	}
	for _, secret := range []string{
		"job-secret-marker",
		"request-secret-marker",
		"user-secret-marker",
		"idempotency-secret-marker",
		"payload-secret-marker",
		"handler-secret-marker",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("observation leaked %q: %q", secret, text)
		}
	}
}
