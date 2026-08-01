package jobs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/xgtian-root/aginex/framework/observability"
	"gorm.io/gorm"
)

var errObservedJobOperation = errors.New("jobs operation failed")

// ObserveQueue decorates enqueue operations with a producer span and durable
// W3C trace propagation. Worker-side observations belong to Runner so they are
// emitted even when the selected queue provider is not decorated.
func ObserveQueue(
	queue Queue,
	recorder *observability.Recorder,
) (Queue, error) {
	if nilJobInterface(queue) {
		return nil, fmt.Errorf("%w: queue is required", ErrInvalid)
	}
	return &observedQueue{
		queue:    queue,
		recorder: jobRecorder(recorder),
	}, nil
}

// ObserveTransactionalQueue preserves Bind while adding enqueue propagation
// to both the root queue and every transaction-bound queue.
func ObserveTransactionalQueue(
	queue TransactionalQueue,
	recorder *observability.Recorder,
) (TransactionalQueue, error) {
	if nilJobInterface(queue) {
		return nil, fmt.Errorf("%w: transactional queue is required", ErrInvalid)
	}
	current := jobRecorder(recorder)
	return &observedTransactionalQueue{
		observedQueue: &observedQueue{queue: queue, recorder: current},
		transactional: queue,
	}, nil
}

// ObserveAdministrativeQueue additionally preserves the operator List API.
func ObserveAdministrativeQueue(
	queue AdministrativeQueue,
	recorder *observability.Recorder,
) (AdministrativeQueue, error) {
	if nilJobInterface(queue) {
		return nil, fmt.Errorf("%w: administrative queue is required", ErrInvalid)
	}
	current := jobRecorder(recorder)
	return &observedAdministrativeQueue{
		observedTransactionalQueue: &observedTransactionalQueue{
			observedQueue: &observedQueue{queue: queue, recorder: current},
			transactional: queue,
		},
		inspector: queue,
	}, nil
}

type observedQueue struct {
	queue    Queue
	recorder *observability.Recorder
}

func (queue *observedQueue) Enqueue(
	ctx context.Context,
	request EnqueueRequest,
) (EnqueueResult, error) {
	attributes := jobAttributes(request.Type, request.Version, "")
	spanContext, span := queue.recorder.Start(ctx, observability.SpanStart{
		Name:       "jobs.enqueue",
		Kind:       observability.SpanKindProducer,
		Attributes: attributes,
	})
	request.Trace.TraceParent = observability.TraceParentFromContext(spanContext)
	startedAt := time.Now()
	result, err := queue.queue.Enqueue(spanContext, request)
	outcome := "created"
	if err != nil {
		outcome = "error"
		span.End(observability.SpanEnd{
			Outcome: observability.OutcomeError,
			Err:     errObservedJobOperation,
		})
	} else {
		if !result.Created {
			outcome = "existing"
		}
		span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	}
	recordJobMetric(queue.recorder, observability.Metric{
		Name:       "jobs.enqueue.requests",
		Kind:       observability.MetricCounter,
		Value:      1,
		Unit:       "request",
		Attributes: jobAttributes(request.Type, request.Version, outcome),
	})
	recordJobMetric(queue.recorder, observability.Metric{
		Name:       "jobs.enqueue.duration",
		Kind:       observability.MetricHistogram,
		Value:      float64(time.Since(startedAt)) / float64(time.Millisecond),
		Unit:       "ms",
		Attributes: jobAttributes(request.Type, request.Version, outcome),
	})
	return result, err
}

func (queue *observedQueue) Claim(
	ctx context.Context,
	request ClaimRequest,
) ([]Job, error) {
	return queue.queue.Claim(ctx, request)
}

func (queue *observedQueue) Heartbeat(
	ctx context.Context,
	id,
	workerID string,
) error {
	return queue.queue.Heartbeat(ctx, id, workerID)
}

func (queue *observedQueue) Succeed(
	ctx context.Context,
	id,
	workerID string,
) error {
	return queue.queue.Succeed(ctx, id, workerID)
}

func (queue *observedQueue) Fail(
	ctx context.Context,
	id,
	workerID string,
	jobError error,
) (State, error) {
	return queue.queue.Fail(ctx, id, workerID, jobError)
}

func (queue *observedQueue) RetryDead(ctx context.Context, id string) error {
	return queue.queue.RetryDead(ctx, id)
}

type observedTransactionalQueue struct {
	*observedQueue
	transactional TransactionalQueue
}

func (queue *observedTransactionalQueue) Bind(db *gorm.DB) (Queue, error) {
	bound, err := queue.transactional.Bind(db)
	if err != nil {
		return nil, err
	}
	if nilJobInterface(bound) {
		return nil, fmt.Errorf(
			"%w: transaction-bound queue is required",
			ErrInvalid,
		)
	}
	return &observedQueue{queue: bound, recorder: queue.recorder}, nil
}

type observedAdministrativeQueue struct {
	*observedTransactionalQueue
	inspector Inspector
}

func (queue *observedAdministrativeQueue) List(
	ctx context.Context,
	request ListRequest,
) (ListResult, error) {
	return queue.inspector.List(ctx, request)
}

func jobRecorder(recorder *observability.Recorder) *observability.Recorder {
	if recorder != nil {
		return recorder
	}
	recorder, _ = observability.NewRecorder(nil)
	return recorder
}

func jobAttributes(
	jobType string,
	version uint,
	outcome string,
) observability.Attributes {
	if !typePattern.MatchString(jobType) || len(jobType) > 120 {
		jobType = "invalid"
	}
	versionValue := "invalid"
	if version > 0 {
		versionValue = strconv.FormatUint(uint64(version), 10)
	}
	values := map[string]string{
		"job.type":    jobType,
		"job.version": versionValue,
	}
	if outcome != "" {
		values["outcome"] = outcome
	}
	attributes, _ := observability.NewAttributes(values)
	return attributes
}

func operationAttributes(
	operation,
	outcome string,
) observability.Attributes {
	values := map[string]string{"operation": operation}
	if outcome != "" {
		values["outcome"] = outcome
	}
	attributes, _ := observability.NewAttributes(values)
	return attributes
}

func settlementAttributes(
	job Job,
	operation,
	outcome string,
) observability.Attributes {
	values := jobAttributes(job.Type, job.Version, outcome).Values()
	values["operation"] = operation
	attributes, _ := observability.NewAttributes(values)
	return attributes
}

func (runner *Runner) recordOperation(
	operation string,
	attributes observability.Attributes,
	startedAt time.Time,
) {
	recordJobMetric(runner.recorder, observability.Metric{
		Name:       "jobs.worker." + operation + ".requests",
		Kind:       observability.MetricCounter,
		Value:      1,
		Unit:       "request",
		Attributes: attributes,
	})
	recordJobMetric(runner.recorder, observability.Metric{
		Name:       "jobs.worker." + operation + ".duration",
		Kind:       observability.MetricHistogram,
		Value:      float64(time.Since(startedAt)) / float64(time.Millisecond),
		Unit:       "ms",
		Attributes: attributes,
	})
}

func (runner *Runner) recordSettlement(
	job Job,
	operation,
	outcome string,
	startedAt time.Time,
) {
	attributes := settlementAttributes(job, operation, outcome)
	recordJobMetric(runner.recorder, observability.Metric{
		Name:       "jobs.worker.settlement.requests",
		Kind:       observability.MetricCounter,
		Value:      1,
		Unit:       "request",
		Attributes: attributes,
	})
	recordJobMetric(runner.recorder, observability.Metric{
		Name:       "jobs.worker.settlement.duration",
		Kind:       observability.MetricHistogram,
		Value:      float64(time.Since(startedAt)) / float64(time.Millisecond),
		Unit:       "ms",
		Attributes: attributes,
	})
}

func recordJobMetric(
	recorder *observability.Recorder,
	metric observability.Metric,
) {
	_ = recorder.RecordMetric(metric)
}

func nilJobInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
