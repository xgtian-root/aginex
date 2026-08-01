package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/module"
)

func TestDispatcherUsesExactTypeAndVersionAndExposesExecutionMetadata(t *testing.T) {
	registry := module.NewRegistry()
	called := make(chan string, 1)
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    "storage.cleanup",
		Version: 1,
		Handle: func(ctx context.Context, payload json.RawMessage) error {
			metadata, ok := ExecutionFromContext(ctx)
			if !ok {
				return errors.New("execution metadata is missing")
			}
			actor, actorOK := ActorFromContext(ctx)
			trace, traceOK := TraceFromContext(ctx)
			requestID, requestOK := RequestIDFromContext(ctx)
			if !actorOK || !traceOK || !requestOK {
				return errors.New("context accessors did not expose metadata")
			}
			if metadata.JobID != "job-1" ||
				metadata.Type != "storage.cleanup" ||
				metadata.Version != 1 ||
				metadata.Attempt != 2 ||
				metadata.MaxAttempts != 5 ||
				metadata.IdempotencyKey != "file-1:delete" ||
				actor.ID != "user-1" ||
				requestID != "request-1" ||
				trace.TraceParent != "00-abcdef-123456-01" {
				return fmt.Errorf("unexpected execution metadata: %#v %#v %#v", metadata, actor, trace)
			}
			called <- string(payload)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    "storage.cleanup",
		Version: 2,
		Handle: func(context.Context, json.RawMessage) error {
			return errors.New("version 2 handler must not be selected")
		},
	}); err != nil {
		t.Fatal(err)
	}

	dispatcher, err := NewDispatcher(registry)
	if err != nil {
		t.Fatal(err)
	}
	job := testJob("job-1")
	job.Attempts = 2
	job.MaxAttempts = 5
	job.IdempotencyKey = "file-1:delete"
	job.CreatedBy = authz.NewUserActor("user-1", authz.Grant{
		Permission: "files:delete",
		Scope:      authz.ScopeOwn,
	})
	job.Trace = TraceContext{
		RequestID:   "request-1",
		TraceParent: "00-abcdef-123456-01",
	}
	if err := dispatcher.Dispatch(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if got := <-called; got != `{"fileId":"file-1"}` {
		t.Fatalf("payload = %q", got)
	}

	job.Version = 3
	if err := dispatcher.Dispatch(context.Background(), job); !errors.Is(err, ErrHandlerNotFound) {
		t.Fatalf("unknown version error = %v, want ErrHandlerNotFound", err)
	}
}

func TestRunnerSucceedsAndHeartbeatsLongRunningJob(t *testing.T) {
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
	})

	cancel, result := startTestRunner(t, runner)
	waitFor(t, time.Second, func() bool {
		return queue.succeededCount() == 1
	})
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("runner error = %v", err)
	}
	if got := queue.heartbeatCount(); got < 2 {
		t.Fatalf("heartbeat count = %d, want at least 2", got)
	}
	if got := queue.failureCount(); got != 0 {
		t.Fatalf("failure count = %d", got)
	}
}

func TestRunnerDelegatesRetryAndDeadStateToQueueFail(t *testing.T) {
	tests := []struct {
		name        string
		attempts    int
		maxAttempts int
		wantState   State
	}{
		{name: "retry", attempts: 1, maxAttempts: 3, wantState: StateFailed},
		{name: "dead", attempts: 3, maxAttempts: 3, wantState: StateDead},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := testJob("job-" + test.name)
			job.Attempts = test.attempts
			job.MaxAttempts = test.maxAttempts
			queue := newFakeQueue(job)
			registry := module.NewRegistry()
			handlerError := errors.New("object store unavailable")
			if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
				Type:    job.Type,
				Version: job.Version,
				Handle: func(context.Context, json.RawMessage) error {
					return handlerError
				},
			}); err != nil {
				t.Fatal(err)
			}
			runner := newTestRunner(t, queue, registry, nil)

			cancel, result := startTestRunner(t, runner)
			waitFor(t, time.Second, func() bool {
				return queue.failureCount() == 1
			})
			cancel()
			if err := <-result; err != nil {
				t.Fatalf("runner error = %v", err)
			}
			failure := queue.failuresSnapshot()[0]
			if !errors.Is(failure.err, handlerError) {
				t.Fatalf("failure error = %v", failure.err)
			}
			if failure.state != test.wantState {
				t.Fatalf("failure state = %q, want %q", failure.state, test.wantState)
			}
			if got := queue.succeededCount(); got != 0 {
				t.Fatalf("succeeded count = %d", got)
			}
		})
	}
}

func TestRunnerFailsClosedForUnknownHandler(t *testing.T) {
	queue := newFakeQueue(testJob("job-unknown"))
	runner := newTestRunner(t, queue, module.NewRegistry(), nil)

	cancel, result := startTestRunner(t, runner)
	waitFor(t, time.Second, func() bool {
		return queue.failureCount() == 1
	})
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("runner error = %v", err)
	}
	failure := queue.failuresSnapshot()[0]
	if !errors.Is(failure.err, ErrHandlerNotFound) {
		t.Fatalf("failure error = %v, want ErrHandlerNotFound", failure.err)
	}
	if got := queue.succeededCount(); got != 0 {
		t.Fatalf("succeeded count = %d", got)
	}
}

func TestRunnerRecoversHandlerPanicAndMarksFailure(t *testing.T) {
	job := testJob("job-panic")
	queue := newFakeQueue(job)
	registry := module.NewRegistry()
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    job.Type,
		Version: job.Version,
		Handle: func(context.Context, json.RawMessage) error {
			panic("boom")
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t, queue, registry, nil)

	cancel, result := startTestRunner(t, runner)
	waitFor(t, time.Second, func() bool {
		return queue.failureCount() == 1
	})
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("runner error = %v", err)
	}
	if failure := queue.failuresSnapshot()[0]; !errors.Is(failure.err, ErrHandlerPanic) {
		t.Fatalf("failure error = %v, want ErrHandlerPanic", failure.err)
	}
}

func TestRunnerCancellationNeverMarksCanceledHandlerSuccessful(t *testing.T) {
	job := testJob("job-canceled")
	queue := newFakeQueue(job)
	registry := module.NewRegistry()
	started := make(chan struct{})
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    job.Type,
		Version: job.Version,
		Handle: func(ctx context.Context, _ json.RawMessage) error {
			close(started)
			<-ctx.Done()
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t, queue, registry, nil)

	cancel, result := startTestRunner(t, runner)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("runner error = %v", err)
	}
	waitFor(t, time.Second, func() bool {
		return queue.failureCount() == 1
	})
	if got := queue.succeededCount(); got != 0 {
		t.Fatalf("succeeded count = %d", got)
	}
	if failure := queue.failuresSnapshot()[0]; !errors.Is(failure.err, context.Canceled) {
		t.Fatalf("failure error = %v, want context.Canceled", failure.err)
	}
}

func TestRunnerBoundsConcurrencyAndCoalescesDuplicateActiveClaims(t *testing.T) {
	jobs := make([]Job, 0, 9)
	jobs = append(jobs, testJob("job-0"), testJob("job-0"))
	for index := 1; index < 8; index++ {
		jobs = append(jobs, testJob(fmt.Sprintf("job-%d", index)))
	}
	queue := newFakeQueue(jobs...)
	registry := module.NewRegistry()
	release := make(chan struct{})
	var running atomic.Int64
	var maximum atomic.Int64
	var firstJobExecutions atomic.Int64
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    "storage.cleanup",
		Version: 1,
		Handle: func(ctx context.Context, _ json.RawMessage) error {
			current := running.Add(1)
			defer running.Add(-1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			metadata, _ := ExecutionFromContext(ctx)
			if metadata.JobID == "job-0" {
				firstJobExecutions.Add(1)
			}
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
	runner := newTestRunner(t, queue, registry, func(config *RunnerConfig) {
		config.Concurrency = 2
	})

	cancel, result := startTestRunner(t, runner)
	waitFor(t, time.Second, func() bool {
		return maximum.Load() == 2
	})
	close(release)
	waitFor(t, time.Second, func() bool {
		return queue.succeededCount() == 8
	})
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("runner error = %v", err)
	}
	if got := maximum.Load(); got > 2 {
		t.Fatalf("maximum concurrency = %d, want <= 2", got)
	}
	if got := firstJobExecutions.Load(); got != 1 {
		t.Fatalf("duplicate active job executions = %d, want 1", got)
	}
}

func TestNewRunnerRejectsHeartbeatAtOrBeyondLease(t *testing.T) {
	registry := module.NewRegistry()
	queue := newFakeQueue()
	for _, heartbeat := range []time.Duration{time.Second, 2 * time.Second} {
		_, err := NewRunner(queue, registry, RunnerConfig{
			WorkerID:          "worker-test",
			Concurrency:       1,
			PollInterval:      time.Millisecond,
			LeaseDuration:     time.Second,
			HeartbeatInterval: heartbeat,
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("heartbeat %s error = %v, want ErrInvalid", heartbeat, err)
		}
	}
}

func TestNewRunnerRejectsTypedNilQueue(t *testing.T) {
	var queue *fakeQueue
	_, err := NewRunner(queue, module.NewRegistry(), RunnerConfig{
		WorkerID:          "worker-test",
		Concurrency:       1,
		PollInterval:      time.Millisecond,
		LeaseDuration:     time.Second,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewRunner(typed nil queue) error = %v, want ErrInvalid", err)
	}
}

func TestRunnerCancellationUsesOneBudgetAndWaitsForSlowFail(t *testing.T) {
	job := testJob("job-slow-fail")
	queue := newDeadlineFailQueue(newFakeQueue(job))
	registry := module.NewRegistry()
	handlerStarted := make(chan struct{})
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    job.Type,
		Version: job.Version,
		Handle: func(ctx context.Context, _ json.RawMessage) error {
			close(handlerStarted)
			<-ctx.Done()
			time.Sleep(30 * time.Millisecond)
			return ctx.Err()
		},
	}); err != nil {
		t.Fatal(err)
	}
	const shutdownTimeout = 100 * time.Millisecond
	runner := newTestRunner(t, queue, registry, func(config *RunnerConfig) {
		config.OperationTimeout = time.Second
		config.ShutdownTimeout = shutdownTimeout
	})

	cancel, result := startTestRunner(t, runner)
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	canceledAt := time.Now()
	cancel()
	var runErr error
	select {
	case runErr = <-result:
	case <-time.After(3 * shutdownTimeout):
		t.Fatal("runner did not return within the shutdown budget")
	}
	if !errors.Is(runErr, ErrShutdownTimeout) {
		t.Fatalf("runner error = %v, want ErrShutdownTimeout", runErr)
	}
	if elapsed := time.Since(canceledAt); elapsed > 2*shutdownTimeout {
		t.Fatalf("shutdown elapsed = %s, want <= %s", elapsed, 2*shutdownTimeout)
	}
	select {
	case <-queue.failFinished:
	default:
		t.Fatal("Run returned while Queue.Fail was still running")
	}
}

func TestRunnerCancellationReportsHandlerThatIgnoresStop(t *testing.T) {
	job := testJob("job-slow-handler")
	queue := newFakeQueue(job)
	registry := module.NewRegistry()
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	defer close(releaseHandler)
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    job.Type,
		Version: job.Version,
		Handle: func(context.Context, json.RawMessage) error {
			close(handlerStarted)
			<-releaseHandler
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t, queue, registry, func(config *RunnerConfig) {
		config.ShutdownTimeout = 80 * time.Millisecond
	})

	cancel, result := startTestRunner(t, runner)
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	var runErr error
	select {
	case runErr = <-result:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("runner did not report the non-cooperative handler")
	}
	if !errors.Is(runErr, ErrShutdownTimeout) {
		t.Fatalf("runner error = %v, want ErrShutdownTimeout", runErr)
	}
	if queue.failureCount() != 1 {
		t.Fatalf("failure settlements = %d, want 1", queue.failureCount())
	}
}

func TestRunnerRejectsAClaimLeasedToAnotherWorker(t *testing.T) {
	job := testJob("job-wrong-owner")
	job.LockedBy = "worker-other"
	queue := newFakeQueue(job)
	registry := module.NewRegistry()
	var called atomic.Bool
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    job.Type,
		Version: job.Version,
		Handle: func(context.Context, json.RawMessage) error {
			called.Store(true)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t, queue, registry, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := runner.Run(ctx)
	if !errors.Is(err, ErrQueueProtocol) {
		t.Fatalf("runner error = %v, want ErrQueueProtocol", err)
	}
	if called.Load() {
		t.Fatal("handler ran without owning the queue lease")
	}
	if queue.succeededCount() != 0 || queue.failureCount() != 0 {
		t.Fatal("runner attempted to settle another worker's lease")
	}
}

func testJob(id string) Job {
	return Job{
		ID:          id,
		Type:        "storage.cleanup",
		Version:     1,
		Payload:     json.RawMessage(`{"fileId":"file-1"}`),
		State:       StateRunning,
		Attempts:    1,
		MaxAttempts: 3,
		LockedBy:    "worker-test",
		CreatedBy:   authz.NewSystemActor("api"),
	}
}

func newTestRunner(
	t *testing.T,
	queue Queue,
	registry *module.Registry,
	mutate func(*RunnerConfig),
) *Runner {
	t.Helper()
	config := RunnerConfig{
		WorkerID:          "worker-test",
		Concurrency:       1,
		PollInterval:      2 * time.Millisecond,
		LeaseDuration:     100 * time.Millisecond,
		HeartbeatInterval: 20 * time.Millisecond,
		OperationTimeout:  100 * time.Millisecond,
		ShutdownTimeout:   time.Second,
	}
	if mutate != nil {
		mutate(&config)
	}
	runner, err := NewRunner(queue, registry, config)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func startTestRunner(t *testing.T, runner *Runner) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runner.Run(ctx)
	}()
	return cancel, result
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not satisfied before timeout")
		}
		time.Sleep(time.Millisecond)
	}
}

type fakeFailure struct {
	id    string
	err   error
	state State
}

type fakeQueue struct {
	mu                sync.Mutex
	pending           []Job
	jobs              map[string]Job
	heartbeats        []string
	succeeded         []string
	failures          []fakeFailure
	onSecondHeartbeat func()
}

type deadlineFailQueue struct {
	*fakeQueue
	failFinished chan struct{}
}

func newDeadlineFailQueue(queue *fakeQueue) *deadlineFailQueue {
	return &deadlineFailQueue{
		fakeQueue:    queue,
		failFinished: make(chan struct{}),
	}
}

func (queue *deadlineFailQueue) Fail(
	ctx context.Context,
	_,
	_ string,
	_ error,
) (State, error) {
	defer close(queue.failFinished)
	<-ctx.Done()
	return "", ctx.Err()
}

func newFakeQueue(pending ...Job) *fakeQueue {
	jobsByID := make(map[string]Job, len(pending))
	for _, job := range pending {
		jobsByID[job.ID] = job
	}
	return &fakeQueue{
		pending: append([]Job(nil), pending...),
		jobs:    jobsByID,
	}
}

func (queue *fakeQueue) Enqueue(context.Context, EnqueueRequest) (EnqueueResult, error) {
	return EnqueueResult{}, errors.New("enqueue is not used by runner tests")
}

func (queue *fakeQueue) Claim(ctx context.Context, request ClaimRequest) ([]Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	limit := request.Limit
	if limit > len(queue.pending) {
		limit = len(queue.pending)
	}
	claimed := append([]Job(nil), queue.pending[:limit]...)
	queue.pending = queue.pending[limit:]
	return claimed, nil
}

func (queue *fakeQueue) Heartbeat(ctx context.Context, id, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	queue.heartbeats = append(queue.heartbeats, id)
	count := len(queue.heartbeats)
	callback := queue.onSecondHeartbeat
	queue.mu.Unlock()
	if count == 2 && callback != nil {
		callback()
	}
	return nil
}

func (queue *fakeQueue) Succeed(ctx context.Context, id, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.succeeded = append(queue.succeeded, id)
	return nil
}

func (queue *fakeQueue) Fail(ctx context.Context, id, _ string, jobError error) (State, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job := queue.jobs[id]
	state := StateFailed
	if job.Attempts >= job.MaxAttempts {
		state = StateDead
	}
	queue.failures = append(queue.failures, fakeFailure{id: id, err: jobError, state: state})
	return state, nil
}

func (queue *fakeQueue) RetryDead(context.Context, string) error {
	return errors.New("retry dead is not used by runner tests")
}

func (queue *fakeQueue) heartbeatCount() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.heartbeats)
}

func (queue *fakeQueue) succeededCount() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.succeeded)
}

func (queue *fakeQueue) failureCount() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.failures)
}

func (queue *fakeQueue) failuresSnapshot() []fakeFailure {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]fakeFailure(nil), queue.failures...)
}
