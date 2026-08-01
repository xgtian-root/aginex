package storage

import (
	"context"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xgtian-root/aginex/framework/observability"
)

const maxStorageUnwrapDepth = 16

type storageUnwrapper interface {
	Unwrap() Storage
}

type observedStorage struct {
	next     Storage
	provider string
	recorder *observability.Recorder
}

type observedReadyStorage struct {
	*observedStorage
	checker ReadinessChecker
}

type storageObservation struct {
	operation string
	provider  string
	recorder  *observability.Recorder
	startedAt time.Time
	span      *observability.Span
	once      sync.Once
}

type observedReadCloser struct {
	next        io.ReadCloser
	observation *storageObservation
	bytesRead   atomic.Int64
}

// Observe decorates an object store with provider-neutral spans and metrics.
// It deliberately records no bucket, key, URL, filename, MIME type, or error
// text. Unknown provider names collapse to "custom" to bound cardinality.
func Observe(
	store Storage,
	provider string,
	recorder *observability.Recorder,
) Storage {
	if nilStorage(store) || recorder == nil {
		return store
	}
	switch store.(type) {
	case *observedStorage, *observedReadyStorage:
		return store
	}
	observed := &observedStorage{
		next:     store,
		provider: normalizeStorageProvider(provider, store),
		recorder: recorder,
	}
	if checker, ok := store.(ReadinessChecker); ok {
		return &observedReadyStorage{
			observedStorage: observed,
			checker:         checker,
		}
	}
	return observed
}

// AsLocal unwraps transparent storage decorators and returns the Local
// provider needed by the local upload/content HTTP adapters.
func AsLocal(store Storage) (*Local, bool) {
	for range maxStorageUnwrapDepth {
		if nilStorage(store) {
			return nil, false
		}
		if local, ok := store.(*Local); ok {
			return local, local != nil
		}
		unwrapper, ok := store.(storageUnwrapper)
		if !ok {
			return nil, false
		}
		store = unwrapper.Unwrap()
	}
	return nil, false
}

func (storage *observedStorage) Unwrap() Storage {
	if storage == nil {
		return nil
	}
	return storage.next
}

func (storage *observedStorage) CreateUpload(
	ctx context.Context,
	request UploadRequest,
) (SignedRequest, error) {
	ctx, observation := storage.begin(ctx, "create_upload")
	result, err := storage.next.CreateUpload(ctx, request)
	bytes := int64(-1)
	if err == nil {
		bytes = request.Size
	}
	observation.finish(err, bytes)
	return result, err
}

func (storage *observedStorage) SignRead(
	ctx context.Context,
	key string,
	expires time.Duration,
) (SignedRequest, error) {
	ctx, observation := storage.begin(ctx, "sign_read")
	result, err := storage.next.SignRead(ctx, key, expires)
	observation.finish(err, -1)
	return result, err
}

func (storage *observedStorage) Stat(
	ctx context.Context,
	key string,
) (ObjectInfo, error) {
	ctx, observation := storage.begin(ctx, "stat")
	result, err := storage.next.Stat(ctx, key)
	bytes := int64(-1)
	if err == nil {
		bytes = result.Size
	}
	observation.finish(err, bytes)
	return result, err
}

func (storage *observedStorage) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, error) {
	ctx, observation := storage.begin(ctx, "open")
	reader, err := storage.next.Open(ctx, key)
	if err != nil || reader == nil {
		observation.finish(err, -1)
		return reader, err
	}
	return &observedReadCloser{
		next:        reader,
		observation: observation,
	}, nil
}

func (storage *observedStorage) Delete(
	ctx context.Context,
	key string,
) error {
	ctx, observation := storage.begin(ctx, "delete")
	err := storage.next.Delete(ctx, key)
	observation.finish(err, -1)
	return err
}

func (storage *observedReadyStorage) CheckReadiness(
	ctx context.Context,
) error {
	ctx, observation := storage.begin(ctx, "readiness")
	err := storage.checker.CheckReadiness(ctx)
	observation.finish(err, -1)
	return err
}

func (storage *observedStorage) begin(
	ctx context.Context,
	operation string,
) (context.Context, *storageObservation) {
	attributes := storageAttributes(storage.provider, operation, "")
	nextContext, span := storage.recorder.Start(
		ctx,
		observability.SpanStart{
			Name:       "storage " + operation,
			Kind:       observability.SpanKindClient,
			Attributes: attributes,
		},
	)
	return nextContext, &storageObservation{
		operation: operation,
		provider:  storage.provider,
		recorder:  storage.recorder,
		startedAt: time.Now(),
		span:      span,
	}
}

func (observation *storageObservation) finish(err error, bytes int64) {
	if observation == nil {
		return
	}
	observation.once.Do(func() {
		outcome := observability.OutcomeOK
		if err != nil {
			outcome = observability.OutcomeError
		}
		attributes := storageAttributes(
			observation.provider,
			observation.operation,
			string(outcome),
		)
		_ = observation.span.SetAttributes(attributes)
		// Cloud SDK errors commonly contain bucket, key, or signed URL data.
		// Preserve them for the caller but never copy them into telemetry.
		observation.span.End(observability.SpanEnd{Outcome: outcome})
		durationMilliseconds := float64(time.Since(observation.startedAt)) /
			float64(time.Millisecond)
		_ = observation.recorder.RecordMetric(observability.Metric{
			Name:       "storage.client.operations",
			Kind:       observability.MetricCounter,
			Value:      1,
			Unit:       "1",
			Attributes: attributes,
		})
		_ = observation.recorder.RecordMetric(observability.Metric{
			Name:       "storage.client.duration",
			Kind:       observability.MetricHistogram,
			Value:      durationMilliseconds,
			Unit:       "ms",
			Attributes: attributes,
		})
		if bytes >= 0 {
			_ = observation.recorder.RecordMetric(observability.Metric{
				Name:       "storage.client.bytes",
				Kind:       observability.MetricHistogram,
				Value:      float64(bytes),
				Unit:       "By",
				Attributes: attributes,
			})
		}
	})
}

func (reader *observedReadCloser) Read(buffer []byte) (int, error) {
	read, err := reader.next.Read(buffer)
	reader.bytesRead.Add(int64(read))
	switch {
	case err == nil:
	case err == io.EOF:
		reader.observation.finish(nil, reader.bytesRead.Load())
	default:
		reader.observation.finish(err, reader.bytesRead.Load())
	}
	return read, err
}

func (reader *observedReadCloser) Close() error {
	err := reader.next.Close()
	reader.observation.finish(err, reader.bytesRead.Load())
	return err
}

func normalizeStorageProvider(candidate string, store Storage) string {
	provider := strings.ToLower(strings.TrimSpace(candidate))
	if provider == "" {
		switch store.(type) {
		case *Local:
			provider = "local"
		case *S3:
			provider = "s3"
		case *OSS:
			provider = "oss"
		}
	}
	switch provider {
	case "local", "s3", "oss":
		return provider
	default:
		return "custom"
	}
}

func storageAttributes(
	provider string,
	operation string,
	outcome string,
) observability.Attributes {
	values := map[string]string{
		"storage.provider":  provider,
		"storage.operation": operation,
	}
	if outcome != "" {
		values["outcome"] = outcome
	}
	attributes, _ := observability.NewAttributes(values)
	return attributes
}

func nilStorage(store Storage) bool {
	if store == nil {
		return true
	}
	value := reflect.ValueOf(store)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
