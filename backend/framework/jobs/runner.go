package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/framework/observability"
)

var (
	// ErrHandlerNotFound means no handler was registered for the exact job type
	// and payload version. The runner fails the job rather than guessing a
	// compatible version.
	ErrHandlerNotFound = errors.New("jobs: handler not found")
	// ErrHandlerPanic marks a panic recovered at the job-handler boundary.
	ErrHandlerPanic = errors.New("jobs: handler panic")
	// ErrRunnerActive prevents the same Runner value from being started twice.
	ErrRunnerActive = errors.New("jobs: runner is already active")
	// ErrQueueProtocol marks an invalid result returned by a Queue
	// implementation.
	ErrQueueProtocol = errors.New("jobs: queue protocol violation")
	// ErrShutdownTimeout means at least one handler did not honor cancellation
	// before the configured graceful-shutdown deadline.
	ErrShutdownTimeout = errors.New("jobs: graceful shutdown timed out")
)

// ExecutionMetadata describes the durable delivery currently being handled.
//
// Attempt starts at one and may increase because delivery is at least once.
// Handlers must use their own idempotency boundary when producing side
// effects. Actor and trace values originate from the enqueue request.
type ExecutionMetadata struct {
	JobID          string
	Type           string
	Version        uint
	Attempt        int
	MaxAttempts    int
	IdempotencyKey string
	Actor          authz.Actor
	Trace          TraceContext
}

type executionContextKey struct{}

// ExecutionFromContext returns a copy of the current durable-delivery
// metadata. It is available only while a registered job handler is running.
func ExecutionFromContext(ctx context.Context) (ExecutionMetadata, bool) {
	if ctx == nil {
		return ExecutionMetadata{}, false
	}
	metadata, ok := ctx.Value(executionContextKey{}).(ExecutionMetadata)
	if !ok {
		return ExecutionMetadata{}, false
	}
	metadata.Actor = cloneActor(metadata.Actor)
	return metadata, true
}

// ActorFromContext returns the actor that enqueued the current durable job.
func ActorFromContext(ctx context.Context) (authz.Actor, bool) {
	metadata, ok := ExecutionFromContext(ctx)
	if !ok {
		return authz.Actor{}, false
	}
	return metadata.Actor, true
}

// TraceFromContext returns the request and distributed-trace metadata carried
// by the current durable job.
func TraceFromContext(ctx context.Context) (TraceContext, bool) {
	metadata, ok := ExecutionFromContext(ctx)
	if !ok {
		return TraceContext{}, false
	}
	return metadata.Trace, true
}

// RequestIDFromContext returns the originating request ID. The boolean reports
// whether the handler is running inside a durable job, not whether the request
// ID string is non-empty.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	metadata, ok := ExecutionFromContext(ctx)
	if !ok {
		return "", false
	}
	return metadata.Trace.RequestID, true
}

// Dispatcher takes an immutable snapshot of module job handlers and performs
// exact type-and-version dispatch. The dependency points from this runtime
// package to module metadata; module.JobHandler itself remains independent of
// jobs, preventing a package cycle.
type Dispatcher struct {
	handlers map[string]module.JobHandler
}

// NewDispatcher validates a module registry and snapshots its versioned job
// handlers. Later registry changes do not affect the dispatcher.
func NewDispatcher(registry *module.Registry) (*Dispatcher, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: module registry is required", ErrInvalid)
	}
	if err := registry.Validate(); err != nil {
		return nil, fmt.Errorf("%w: validate module registry: %v", ErrInvalid, err)
	}
	definitions := registry.JobHandlers()
	handlers := make(map[string]module.JobHandler, len(definitions))
	for _, definition := range definitions {
		jobType := strings.TrimSpace(definition.Type)
		if !typePattern.MatchString(jobType) ||
			len(jobType) > 120 ||
			definition.Version == 0 ||
			definition.Handle == nil {
			return nil, fmt.Errorf(
				"%w: invalid handler %q version %d",
				ErrInvalid,
				jobType,
				definition.Version,
			)
		}
		key := dispatcherKey(jobType, definition.Version)
		if _, exists := handlers[key]; exists {
			return nil, fmt.Errorf("%w: duplicate handler %s", ErrInvalid, key)
		}
		handlers[key] = definition.Handle
	}
	return &Dispatcher{handlers: handlers}, nil
}

// Dispatch invokes the handler registered for the job's exact type and
// version. Unknown versions and malformed claimed jobs fail closed.
func (dispatcher *Dispatcher) Dispatch(ctx context.Context, job Job) (err error) {
	if dispatcher == nil {
		return fmt.Errorf("%w: dispatcher is required", ErrInvalid)
	}
	if ctx == nil {
		return fmt.Errorf("%w: handler context is required", ErrInvalid)
	}
	if err := validateClaimedJob(job); err != nil {
		return err
	}
	handler, ok := dispatcher.handlers[dispatcherKey(job.Type, job.Version)]
	if !ok {
		return fmt.Errorf(
			"%w: %s version %d",
			ErrHandlerNotFound,
			job.Type,
			job.Version,
		)
	}
	metadata := ExecutionMetadata{
		JobID:          job.ID,
		Type:           job.Type,
		Version:        job.Version,
		Attempt:        job.Attempts,
		MaxAttempts:    job.MaxAttempts,
		IdempotencyKey: job.IdempotencyKey,
		Actor:          cloneActor(job.CreatedBy),
		Trace:          job.Trace,
	}
	handlerContext := context.WithValue(ctx, executionContextKey{}, metadata)
	payload := append(json.RawMessage(nil), job.Payload...)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(
				"%w: %s version %d: %v",
				ErrHandlerPanic,
				job.Type,
				job.Version,
				recovered,
			)
		}
	}()
	return handler(handlerContext, payload)
}

// RunnerConfig controls queue polling and lease maintenance.
//
// LeaseDuration must match the selected queue provider's lease setting.
// HeartbeatInterval is required to be shorter than that lease. Queue.Fail owns
// retry delay and terminal dead-state policy.
type RunnerConfig struct {
	WorkerID          string
	Concurrency       int
	PollInterval      time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	OperationTimeout  time.Duration
	ShutdownTimeout   time.Duration
	// Recorder is optional. A no-export recorder is used when nil so valid
	// traceparent values still propagate into job handlers and settlement.
	Recorder *observability.Recorder
}

// Runner claims and executes durable jobs with bounded concurrency.
type Runner struct {
	queue      Queue
	dispatcher *Dispatcher
	config     RunnerConfig
	recorder   *observability.Recorder
	running    atomic.Bool
	active     atomic.Int64
}

// NewRunner constructs a runner from a provider-neutral queue and an immutable
// snapshot of registered module handlers.
func NewRunner(
	queue Queue,
	registry *module.Registry,
	config RunnerConfig,
) (*Runner, error) {
	if nilJobInterface(queue) {
		return nil, fmt.Errorf("%w: queue is required", ErrInvalid)
	}
	normalized, err := normalizeRunnerConfig(config)
	if err != nil {
		return nil, err
	}
	dispatcher, err := NewDispatcher(registry)
	if err != nil {
		return nil, err
	}
	return &Runner{
		queue:      queue,
		dispatcher: dispatcher,
		config:     normalized,
		recorder:   jobRecorder(normalized.Recorder),
	}, nil
}

// Run polls until ctx is canceled or an operational queue/settlement error
// occurs. Caller cancellation is a normal graceful stop and returns nil after
// active handlers settle as failed. A handler that returns nil after its
// context was canceled is never marked successful.
func (runner *Runner) Run(ctx context.Context) error {
	if runner == nil {
		return fmt.Errorf("%w: runner is required", ErrInvalid)
	}
	if ctx == nil {
		return fmt.Errorf("%w: runner context is required", ErrInvalid)
	}
	if !runner.running.CompareAndSwap(false, true) {
		return ErrRunnerActive
	}
	defer runner.running.Store(false)

	workerContext, stopWorker := context.WithCancelCause(ctx)
	defer stopWorker(context.Canceled)
	shutdown := newShutdownBudget(runner.config.ShutdownTimeout)
	active := make(map[string]struct{}, runner.config.Concurrency)
	completed := make(chan executionResult, runner.config.Concurrency)

	for {
		if cause := context.Cause(workerContext); cause != nil {
			return runner.drain(active, completed, shutdown)
		}

		select {
		case result := <-completed:
			delete(active, result.jobID)
			if result.err != nil {
				stopWorker(result.err)
				return errors.Join(
					result.err,
					runner.drain(active, completed, shutdown),
				)
			}
			continue
		default:
		}

		available := runner.config.Concurrency - len(active)
		if available > 0 {
			claimed, err := runner.claim(workerContext, available)
			if err != nil {
				if context.Cause(workerContext) != nil {
					return runner.drain(active, completed, shutdown)
				}
				wrapped := fmt.Errorf("claim durable jobs: %w", err)
				stopWorker(wrapped)
				return errors.Join(
					wrapped,
					runner.drain(active, completed, shutdown),
				)
			}
			if cause := context.Cause(workerContext); cause != nil {
				settlementErr := runner.failClaimedAfterCancellation(
					workerContext,
					claimed,
					cause,
					shutdown,
				)
				return errors.Join(
					settlementErr,
					runner.drain(active, completed, shutdown),
				)
			}

			launched, launchErr := runner.launch(
				workerContext,
				claimed,
				active,
				completed,
				shutdown,
			)
			if launchErr != nil {
				stopWorker(launchErr)
				return errors.Join(
					launchErr,
					runner.drain(active, completed, shutdown),
				)
			}
			if launched > 0 {
				continue
			}
		}

		timer := time.NewTimer(runner.config.PollInterval)
		select {
		case result := <-completed:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			delete(active, result.jobID)
			if result.err != nil {
				stopWorker(result.err)
				return errors.Join(
					result.err,
					runner.drain(active, completed, shutdown),
				)
			}
		case <-workerContext.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return runner.drain(active, completed, shutdown)
		case <-timer.C:
		}
	}
}

type executionResult struct {
	jobID string
	err   error
}

func (runner *Runner) launch(
	ctx context.Context,
	claimed []Job,
	active map[string]struct{},
	completed chan<- executionResult,
	shutdown *shutdownBudget,
) (int, error) {
	launched := 0
	for _, job := range claimed {
		job.ID = strings.TrimSpace(job.ID)
		if job.ID == "" {
			return launched, fmt.Errorf("%w: claimed job has no ID", ErrQueueProtocol)
		}
		if strings.TrimSpace(job.LockedBy) != runner.config.WorkerID {
			return launched, fmt.Errorf(
				"%w: job %q is leased by %q instead of %q",
				ErrQueueProtocol,
				job.ID,
				job.LockedBy,
				runner.config.WorkerID,
			)
		}
		if _, duplicate := active[job.ID]; duplicate {
			continue
		}
		if len(active) >= runner.config.Concurrency {
			failure := fmt.Errorf(
				"%w: claim exceeded requested concurrency",
				ErrQueueProtocol,
			)
			jobContext := runner.recorder.Extract(ctx, job.Trace.TraceParent)
			if _, err := runner.fail(jobContext, job, failure); err != nil {
				return launched, errors.Join(failure, err)
			}
			return launched, failure
		}
		active[job.ID] = struct{}{}
		launched++
		go func(claimedJob Job) {
			completed <- executionResult{
				jobID: claimedJob.ID,
				err:   runner.executeWithShutdown(ctx, claimedJob, shutdown),
			}
		}(job)
	}
	return launched, nil
}

func (runner *Runner) execute(
	parent context.Context,
	job Job,
) error {
	return runner.executeWithShutdown(
		parent,
		job,
		newShutdownBudget(runner.config.ShutdownTimeout),
	)
}

func (runner *Runner) executeWithShutdown(
	parent context.Context,
	job Job,
	shutdown *shutdownBudget,
) (resultErr error) {
	extracted := runner.recorder.Extract(parent, job.Trace.TraceParent)
	handlerContext, processSpan := runner.recorder.Start(
		extracted,
		observability.SpanStart{
			Name:       "jobs.process",
			Kind:       observability.SpanKindConsumer,
			Attributes: jobAttributes(job.Type, job.Version, ""),
		},
	)
	var processFailure bool
	var processState State
	defer func() {
		outcome := string(processState)
		if outcome == "" {
			if processFailure || resultErr != nil {
				outcome = "error"
			} else {
				outcome = "succeeded"
			}
		}
		_ = processSpan.SetAttributes(
			jobAttributes(job.Type, job.Version, outcome),
		)
		if processFailure || resultErr != nil {
			processSpan.End(observability.SpanEnd{
				Outcome: observability.OutcomeError,
				Err:     errObservedJobOperation,
			})
			return
		}
		processSpan.End(observability.SpanEnd{
			Outcome: observability.OutcomeOK,
		})
	}()

	currentActive := runner.active.Add(1)
	recordJobMetric(runner.recorder, observability.Metric{
		Name:  "jobs.worker.active",
		Kind:  observability.MetricGauge,
		Value: float64(currentActive),
		Unit:  "job",
	})
	defer func() {
		current := runner.active.Add(-1)
		recordJobMetric(runner.recorder, observability.Metric{
			Name:  "jobs.worker.active",
			Kind:  observability.MetricGauge,
			Value: float64(current),
			Unit:  "job",
		})
	}()

	handlerContext, cancelHandler := context.WithCancelCause(handlerContext)
	defer cancelHandler(context.Canceled)
	handlerDone := make(chan error, 1)
	go func() {
		startedAt := time.Now()
		handlerErr := runner.dispatcher.Dispatch(handlerContext, job)
		outcome := "succeeded"
		if handlerErr != nil {
			outcome = "error"
		}
		recordJobMetric(runner.recorder, observability.Metric{
			Name:       "jobs.handler.executions",
			Kind:       observability.MetricCounter,
			Value:      1,
			Unit:       "job",
			Attributes: jobAttributes(job.Type, job.Version, outcome),
		})
		recordJobMetric(runner.recorder, observability.Metric{
			Name:       "jobs.handler.duration",
			Kind:       observability.MetricHistogram,
			Value:      float64(time.Since(startedAt)) / float64(time.Millisecond),
			Unit:       "ms",
			Attributes: jobAttributes(job.Type, job.Version, outcome),
		})
		handlerDone <- handlerErr
	}()

	ticker := time.NewTicker(runner.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case handlerErr := <-handlerDone:
			var shutdownDeadline time.Time
			if cause := context.Cause(handlerContext); cause != nil {
				handlerErr = cause
				if context.Cause(parent) != nil {
					shutdownDeadline = shutdown.Start()
				}
			}
			processFailure = handlerErr != nil
			processState, resultErr = runner.settleBefore(
				handlerContext,
				job,
				handlerErr,
				shutdownDeadline,
			)
			return resultErr
		case <-handlerContext.Done():
			cause := context.Cause(handlerContext)
			if cause == nil {
				cause = context.Canceled
			}
			processFailure = true
			shutdownDeadline := shutdown.Start()
			if !waitForHandler(
				handlerDone,
				runner.handlerStopTimeout(shutdownDeadline),
			) {
				failure := errors.Join(cause, ErrShutdownTimeout)
				processState, resultErr = runner.failBefore(
					handlerContext,
					job,
					failure,
					shutdownDeadline,
				)
				failErr := resultErr
				return errors.Join(failure, failErr)
			}
			processState, resultErr = runner.settleBefore(
				handlerContext,
				job,
				cause,
				shutdownDeadline,
			)
			return resultErr
		case <-ticker.C:
			if err := runner.heartbeat(handlerContext, job); err != nil {
				failure := fmt.Errorf("heartbeat job %q: %w", job.ID, err)
				cancelHandler(failure)
				processFailure = true
				if !waitForHandler(
					handlerDone,
					runner.handlerStopTimeout(time.Time{}),
				) {
					failure = errors.Join(failure, ErrShutdownTimeout)
					processState, resultErr = runner.fail(
						handlerContext,
						job,
						failure,
					)
					failErr := resultErr
					return errors.Join(failure, failErr)
				}
				processState, resultErr = runner.settle(
					handlerContext,
					job,
					failure,
				)
				return resultErr
			}
		}
	}
}

func (runner *Runner) settle(
	ctx context.Context,
	job Job,
	handlerErr error,
) (State, error) {
	return runner.settleBefore(ctx, job, handlerErr, time.Time{})
}

func (runner *Runner) settleBefore(
	ctx context.Context,
	job Job,
	handlerErr error,
	shutdownDeadline time.Time,
) (State, error) {
	if handlerErr != nil {
		return runner.failBefore(ctx, job, handlerErr, shutdownDeadline)
	}
	if cause := context.Cause(ctx); cause != nil {
		return runner.failBefore(ctx, job, cause, shutdownDeadline)
	}
	operationContext, cancel := runner.detachedOperationContext(
		ctx,
		shutdownDeadline,
	)
	defer cancel()
	operationContext, span := runner.recorder.Start(
		operationContext,
		observability.SpanStart{
			Name:       "jobs.settle",
			Kind:       observability.SpanKindInternal,
			Attributes: settlementAttributes(job, "succeed", ""),
		},
	)
	startedAt := time.Now()
	err := runner.queue.Succeed(
		operationContext,
		job.ID,
		runner.config.WorkerID,
	)
	outcome := "succeeded"
	if err != nil {
		outcome = "error"
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedJobOperation,
		})
	} else {
		span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	}
	runner.recordSettlement(job, "succeed", outcome, startedAt)
	if err != nil {
		return "", fmt.Errorf("mark job %q succeeded: %w", job.ID, err)
	}
	return StateSucceeded, nil
}

func (runner *Runner) fail(
	ctx context.Context,
	job Job,
	failure error,
) (State, error) {
	return runner.failBefore(ctx, job, failure, time.Time{})
}

func (runner *Runner) failBefore(
	ctx context.Context,
	job Job,
	failure error,
	shutdownDeadline time.Time,
) (State, error) {
	operationContext, cancel := runner.detachedOperationContext(
		ctx,
		shutdownDeadline,
	)
	defer cancel()
	operationContext, span := runner.recorder.Start(
		operationContext,
		observability.SpanStart{
			Name:       "jobs.settle",
			Kind:       observability.SpanKindInternal,
			Attributes: settlementAttributes(job, "fail", ""),
		},
	)
	startedAt := time.Now()
	state, err := runner.queue.Fail(
		operationContext,
		job.ID,
		runner.config.WorkerID,
		failure,
	)
	if !shutdownDeadline.IsZero() &&
		errors.Is(operationContext.Err(), context.DeadlineExceeded) {
		err = errors.Join(err, ErrShutdownTimeout)
	}
	if err != nil {
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedJobOperation,
		})
		runner.recordSettlement(job, "fail", "error", startedAt)
		return "", fmt.Errorf("mark job %q failed: %w", job.ID, err)
	}
	if state != StateFailed && state != StateDead {
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedJobOperation,
		})
		runner.recordSettlement(job, "fail", "protocol_error", startedAt)
		return state, fmt.Errorf(
			"%w: failing job %q returned state %q",
			ErrQueueProtocol,
			job.ID,
			state,
		)
	}
	span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	runner.recordSettlement(job, "fail", string(state), startedAt)
	return state, nil
}

func (runner *Runner) heartbeat(ctx context.Context, job Job) error {
	operationContext, cancel := context.WithTimeout(ctx, runner.config.OperationTimeout)
	defer cancel()
	operationContext, span := runner.recorder.Start(
		operationContext,
		observability.SpanStart{
			Name:       "jobs.heartbeat",
			Kind:       observability.SpanKindInternal,
			Attributes: jobAttributes(job.Type, job.Version, ""),
		},
	)
	startedAt := time.Now()
	err := runner.queue.Heartbeat(
		operationContext,
		job.ID,
		runner.config.WorkerID,
	)
	outcome := "succeeded"
	if err != nil {
		outcome = "error"
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedJobOperation,
		})
	} else {
		span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	}
	runner.recordOperation(
		"heartbeat",
		jobAttributes(job.Type, job.Version, outcome),
		startedAt,
	)
	return err
}

func (runner *Runner) claim(ctx context.Context, limit int) ([]Job, error) {
	operationContext, cancel := context.WithTimeout(ctx, runner.config.OperationTimeout)
	defer cancel()
	operationContext, span := runner.recorder.Start(
		operationContext,
		observability.SpanStart{
			Name: "jobs.claim",
			Kind: observability.SpanKindInternal,
		},
	)
	startedAt := time.Now()
	claimed, err := runner.queue.Claim(operationContext, ClaimRequest{
		WorkerID: runner.config.WorkerID,
		Limit:    limit,
	})
	outcome := "succeeded"
	if err != nil {
		outcome = "error"
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedJobOperation,
		})
	} else {
		span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	}
	attributes := operationAttributes("claim", outcome)
	runner.recordOperation("claim", attributes, startedAt)
	recordJobMetric(runner.recorder, observability.Metric{
		Name:       "jobs.worker.claimed",
		Kind:       observability.MetricHistogram,
		Value:      float64(len(claimed)),
		Unit:       "job",
		Attributes: attributes,
	})
	return claimed, err
}

func (runner *Runner) failClaimedAfterCancellation(
	ctx context.Context,
	claimed []Job,
	cause error,
	shutdown *shutdownBudget,
) error {
	shutdownDeadline := shutdown.Start()
	var result error
	seen := make(map[string]struct{}, len(claimed))
	for _, job := range claimed {
		job.ID = strings.TrimSpace(job.ID)
		if job.ID == "" {
			result = errors.Join(
				result,
				fmt.Errorf("%w: claimed job has no ID", ErrQueueProtocol),
			)
			continue
		}
		if _, duplicate := seen[job.ID]; duplicate {
			continue
		}
		seen[job.ID] = struct{}{}
		jobContext := runner.recorder.Extract(ctx, job.Trace.TraceParent)
		if _, err := runner.failBefore(
			jobContext,
			job,
			cause,
			shutdownDeadline,
		); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (runner *Runner) drain(
	active map[string]struct{},
	completed <-chan executionResult,
	shutdown *shutdownBudget,
) error {
	if len(active) == 0 {
		return nil
	}
	// Every active execution receives the same shutdown deadline and performs
	// settlement synchronously before publishing its result. Waiting for those
	// results here prevents Run from returning while Queue.Fail is still in
	// flight. Queue implementations must honor their context deadline.
	shutdown.Start()
	var result error
	for len(active) > 0 {
		execution := <-completed
		delete(active, execution.jobID)
		result = errors.Join(result, execution.err)
	}
	return result
}

func (runner *Runner) detachedOperationContext(
	ctx context.Context,
	shutdownDeadline time.Time,
) (context.Context, context.CancelFunc) {
	parent := context.WithoutCancel(ctx)
	operationDeadline := time.Now().Add(runner.config.OperationTimeout)
	if !shutdownDeadline.IsZero() &&
		shutdownDeadline.Before(operationDeadline) {
		operationDeadline = shutdownDeadline
	}
	return context.WithDeadline(parent, operationDeadline)
}

func (runner *Runner) handlerStopTimeout(
	shutdownDeadline time.Time,
) time.Duration {
	timeout := runner.config.ShutdownTimeout / 2
	if timeout <= 0 {
		timeout = runner.config.ShutdownTimeout
	}
	if !shutdownDeadline.IsZero() {
		remaining := time.Until(shutdownDeadline)
		if remaining <= 0 {
			return time.Nanosecond
		}
		if timeout > remaining {
			timeout = remaining
		}
	}
	return timeout
}

type shutdownBudget struct {
	timeout  time.Duration
	once     sync.Once
	deadline time.Time
}

func newShutdownBudget(timeout time.Duration) *shutdownBudget {
	return &shutdownBudget{timeout: timeout}
}

func (budget *shutdownBudget) Start() time.Time {
	budget.once.Do(func() {
		budget.deadline = time.Now().Add(budget.timeout)
	})
	return budget.deadline
}

func normalizeRunnerConfig(config RunnerConfig) (RunnerConfig, error) {
	claim, err := NormalizeClaim(ClaimRequest{
		WorkerID: config.WorkerID,
		Limit:    config.Concurrency,
	})
	if err != nil {
		return RunnerConfig{}, err
	}
	config.WorkerID = claim.WorkerID
	config.Concurrency = claim.Limit

	switch {
	case config.PollInterval < 0:
		return RunnerConfig{}, fmt.Errorf("%w: poll interval must be positive", ErrInvalid)
	case config.PollInterval == 0:
		config.PollInterval = time.Second
	}
	switch {
	case config.LeaseDuration < 0:
		return RunnerConfig{}, fmt.Errorf("%w: lease duration must be positive", ErrInvalid)
	case config.LeaseDuration == 0:
		config.LeaseDuration = time.Minute
	}
	switch {
	case config.HeartbeatInterval < 0:
		return RunnerConfig{}, fmt.Errorf("%w: heartbeat interval must be positive", ErrInvalid)
	case config.HeartbeatInterval == 0:
		config.HeartbeatInterval = config.LeaseDuration / 3
	}
	if config.HeartbeatInterval <= 0 ||
		config.HeartbeatInterval >= config.LeaseDuration {
		return RunnerConfig{}, fmt.Errorf(
			"%w: heartbeat interval must be shorter than the lease",
			ErrInvalid,
		)
	}
	switch {
	case config.OperationTimeout < 0:
		return RunnerConfig{}, fmt.Errorf("%w: operation timeout must be positive", ErrInvalid)
	case config.OperationTimeout == 0:
		config.OperationTimeout = config.HeartbeatInterval
	}
	switch {
	case config.ShutdownTimeout < 0:
		return RunnerConfig{}, fmt.Errorf("%w: shutdown timeout must be positive", ErrInvalid)
	case config.ShutdownTimeout == 0:
		config.ShutdownTimeout = 30 * time.Second
	}
	return config, nil
}

func validateClaimedJob(job Job) error {
	job.ID = strings.TrimSpace(job.ID)
	job.Type = strings.TrimSpace(job.Type)
	if job.ID == "" || len(job.ID) > 200 {
		return fmt.Errorf("%w: claimed job has an invalid ID", ErrQueueProtocol)
	}
	if !typePattern.MatchString(job.Type) || len(job.Type) > 120 {
		return fmt.Errorf("%w: claimed job has an invalid type", ErrQueueProtocol)
	}
	if job.Version == 0 {
		return fmt.Errorf("%w: claimed job has an invalid version", ErrQueueProtocol)
	}
	if len(job.Payload) == 0 || !json.Valid(job.Payload) {
		return fmt.Errorf("%w: claimed job has an invalid payload", ErrQueueProtocol)
	}
	if job.State != StateRunning ||
		job.Attempts < 1 ||
		job.MaxAttempts < job.Attempts {
		return fmt.Errorf("%w: claimed job has invalid delivery state", ErrQueueProtocol)
	}
	if !validActor(job.CreatedBy) {
		return fmt.Errorf("%w: claimed job has an invalid actor", ErrQueueProtocol)
	}
	if len(job.Trace.RequestID) > 64 || len(job.Trace.TraceParent) > 512 {
		return fmt.Errorf("%w: claimed job has invalid trace metadata", ErrQueueProtocol)
	}
	return nil
}

func waitForHandler(done <-chan error, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func cloneActor(actor authz.Actor) authz.Actor {
	actor.Grants = append([]authz.Grant(nil), actor.Grants...)
	return actor
}

func dispatcherKey(jobType string, version uint) string {
	return fmt.Sprintf("%s@%d", jobType, version)
}
