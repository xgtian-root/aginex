package observability_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/framework/observability"
)

var traceParentPattern = regexp.MustCompile(
	`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`,
)

type collectingSink struct {
	mu      sync.Mutex
	spans   []observability.SpanRecord
	metrics []observability.Metric
}

func (s *collectingSink) RecordSpan(record observability.SpanRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, record)
}

func (s *collectingSink) RecordMetric(metric observability.Metric) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = append(s.metrics, metric)
}

func (s *collectingSink) snapshot() (
	[]observability.SpanRecord,
	[]observability.Metric,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	spans := append([]observability.SpanRecord(nil), s.spans...)
	metrics := append([]observability.Metric(nil), s.metrics...)
	return spans, metrics
}

func TestRemoteParentExtractionAndPropagation(t *testing.T) {
	sink := &collectingSink{}
	clock := newSequenceClock(
		time.Date(
			2026,
			7,
			31,
			8,
			0,
			0,
			0,
			time.FixedZone("CST", 8*60*60),
		),
		time.Date(
			2026,
			7,
			31,
			8,
			0,
			2,
			0,
			time.FixedZone("CST", 8*60*60),
		),
	)
	recorder := mustRecorder(
		t,
		sink,
		observability.WithClock(clock.Now),
		observability.WithEntropy(
			bytes.NewReader(bytes.Repeat([]byte{0xab}, 8)),
		),
	)
	inbound := "00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-00"
	extracted := recorder.Extract(context.Background(), inbound)

	parent, ok := observability.SpanContextFromContext(extracted)
	if !ok || !parent.IsRemote() {
		t.Fatal("valid inbound traceparent was not extracted as a remote parent")
	}
	if got := parent.TraceParent(); got != strings.ToLower(inbound) {
		t.Fatalf("extracted traceparent = %q", got)
	}

	ctx, span := recorder.Start(extracted, observability.SpanStart{
		Name: "http GET /api/v1/files",
		Kind: observability.SpanKindServer,
	})
	current, ok := observability.SpanContextFromContext(ctx)
	if !ok || current.IsRemote() {
		t.Fatal("Start did not install a valid local span context")
	}
	if current.TraceID() != parent.TraceID() {
		t.Fatalf("trace ID = %q, want %q", current.TraceID(), parent.TraceID())
	}
	if current.SpanID() == parent.SpanID() {
		t.Fatal("child span reused the remote parent span ID")
	}
	if current.TraceFlags() != 0 {
		t.Fatalf("trace flags = %02x, want 00", current.TraceFlags())
	}
	if got := observability.TraceParentFromContext(ctx); got != current.TraceParent() {
		t.Fatalf(
			"downstream traceparent = %q, want %q",
			got,
			current.TraceParent(),
		)
	}

	span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	spans, _ := sink.snapshot()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	record := spans[0]
	if !record.HasParent || !record.Parent.IsRemote() {
		t.Fatal("record does not retain its remote parent")
	}
	if record.Duration != 2*time.Second {
		t.Fatalf("duration = %s, want 2s", record.Duration)
	}
	if !record.StartedAt.Equal(clock.values[0].UTC()) ||
		!record.EndedAt.Equal(clock.values[1].UTC()) {
		t.Fatal("recorder did not normalize deterministic clock values to UTC")
	}
}

func TestInvalidTraceParentStartsFreshTraceWithoutSink(t *testing.T) {
	entropy := make([]byte, 24)
	for index := range entropy {
		entropy[index] = byte(index + 1)
	}
	recorder := mustRecorder(
		t,
		nil,
		observability.WithEntropy(bytes.NewReader(entropy)),
	)
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	inherited := recorder.Extract(context.Background(), valid)
	extracted := recorder.Extract(inherited, "00-not-a-valid-traceparent")
	if _, ok := observability.SpanContextFromContext(extracted); ok {
		t.Fatal("invalid header did not clear the inherited remote parent")
	}

	ctx, span := recorder.Start(extracted, observability.SpanStart{Name: "root"})
	current, ok := observability.SpanContextFromContext(ctx)
	if !ok || !current.IsValid() || current.IsRemote() {
		t.Fatal("nil-sink recorder did not create a local W3C context")
	}
	if current.TraceID() == "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatal("invalid header retained the inbound trace ID")
	}
	if got := observability.TraceParentFromContext(ctx); !traceParentPattern.MatchString(got) {
		t.Fatalf("generated traceparent = %q", got)
	}
	span.End(observability.SpanEnd{})

	if err := recorder.RecordMetric(observability.Metric{
		Name:  "process.alive",
		Kind:  observability.MetricGauge,
		Value: 1,
	}); err != nil {
		t.Fatalf("nil-sink RecordMetric() error = %v", err)
	}
}

func TestProducerSpanKindIsPreserved(t *testing.T) {
	sink := &collectingSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	_, span := recorder.Start(context.Background(), observability.SpanStart{
		Name: "jobs.enqueue",
		Kind: observability.SpanKindProducer,
	})
	span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})

	spans, _ := sink.snapshot()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Kind != observability.SpanKindProducer {
		t.Fatalf("span kind = %q, want producer", spans[0].Kind)
	}
}

func TestChildSpanUsesCurrentLocalSpan(t *testing.T) {
	sink := &collectingSink{}
	recorder := mustRecorder(
		t,
		sink,
		observability.WithEntropy(
			bytes.NewReader(bytes.Repeat([]byte{0x31}, 32)),
		),
	)
	rootCtx, root := recorder.Start(context.Background(), observability.SpanStart{
		Name: "request",
		Kind: observability.SpanKindServer,
	})
	childCtx, child := recorder.Start(rootCtx, observability.SpanStart{
		Name: "database",
		Kind: observability.SpanKindClient,
	})
	rootContext := root.Context()
	childContext := child.Context()
	if childContext.TraceID() != rootContext.TraceID() {
		t.Fatal("child span started a new trace")
	}
	if got := observability.TraceParentFromContext(childCtx); got != childContext.TraceParent() {
		t.Fatalf("child propagation value = %q", got)
	}
	child.End(observability.SpanEnd{})
	root.End(observability.SpanEnd{})

	spans, _ := sink.snapshot()
	if len(spans) != 2 {
		t.Fatalf("recorded spans = %d, want 2", len(spans))
	}
	childRecord := spans[0]
	if !childRecord.HasParent ||
		childRecord.Parent.SpanID() != rootContext.SpanID() ||
		childRecord.Parent.IsRemote() {
		t.Fatal("child record does not reference the local root span")
	}
}

func TestSpanEndRecordsOnlyFirstResult(t *testing.T) {
	sink := &collectingSink{}
	started := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	clock := newSequenceClock(started, started.Add(125*time.Millisecond))
	recorder := mustRecorder(
		t,
		sink,
		observability.WithClock(clock.Now),
		observability.WithEntropy(
			bytes.NewReader(bytes.Repeat([]byte{0x41}, 24)),
		),
	)
	_, span := recorder.Start(context.Background(), observability.SpanStart{
		Name: "worker job",
		Kind: observability.SpanKindConsumer,
	})
	span.End(observability.SpanEnd{Err: errors.New("first failure")})
	span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})

	spans, _ := sink.snapshot()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	if spans[0].Outcome != observability.OutcomeError ||
		spans[0].Error != "first failure" ||
		spans[0].Duration != 125*time.Millisecond {
		t.Fatalf("first span result was not retained: %+v", spans[0])
	}
}

func TestConcurrentSpanEndRecordsExactlyOnce(t *testing.T) {
	sink := &collectingSink{}
	recorder := mustRecorder(
		t,
		sink,
		observability.WithEntropy(
			bytes.NewReader(bytes.Repeat([]byte{0x45}, 24)),
		),
	)
	_, span := recorder.Start(
		context.Background(),
		observability.SpanStart{Name: "concurrent end"},
	)

	const callers = 64
	var group sync.WaitGroup
	group.Add(callers)
	for index := 0; index < callers; index++ {
		go func(index int) {
			defer group.Done()
			if index%2 == 0 {
				span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
				return
			}
			span.End(observability.SpanEnd{Err: errors.New("failed")})
		}(index)
	}
	group.Wait()

	spans, _ := sink.snapshot()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
}

func TestSpanSettersUpdateFinalLowCardinalityRecord(t *testing.T) {
	startAttributes, err := observability.NewAttributes(map[string]string{
		"http.method": "GET",
		"http.route":  "unmatched",
	})
	if err != nil {
		t.Fatal(err)
	}
	finalValues := map[string]string{
		"http.route":       "/api/v1/files/{id}",
		"http.status_code": "200",
	}
	finalAttributes, err := observability.NewAttributes(finalValues)
	if err != nil {
		t.Fatal(err)
	}
	sink := &collectingSink{}
	recorder := mustRecorder(
		t,
		sink,
		observability.WithEntropy(
			bytes.NewReader(bytes.Repeat([]byte{0x49}, 24)),
		),
	)
	_, span := recorder.Start(context.Background(), observability.SpanStart{
		Name:       "GET",
		Kind:       observability.SpanKindServer,
		Attributes: startAttributes,
	})
	span.SetName("GET /api/v1/files/{id}")
	if err := span.SetAttributes(finalAttributes); err != nil {
		t.Fatalf("SetAttributes() error = %v", err)
	}
	finalValues["http.route"] = "/mutated"
	span.End(observability.SpanEnd{})
	span.SetName("ignored after end")
	if err := span.SetAttributes(startAttributes); err != nil {
		t.Fatalf("post-End SetAttributes() error = %v", err)
	}

	spans, _ := sink.snapshot()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	record := spans[0]
	if record.Name != "GET /api/v1/files/{id}" {
		t.Fatalf("span name = %q", record.Name)
	}
	expected := map[string]string{
		"http.method":      "GET",
		"http.route":       "/api/v1/files/{id}",
		"http.status_code": "200",
	}
	for key, want := range expected {
		if got, ok := record.Attributes.Get(key); !ok || got != want {
			t.Fatalf("attribute %q = %q, %v; want %q", key, got, ok, want)
		}
	}
}

func TestSpanSetAttributesRejectsMergedCardinalityOverflow(t *testing.T) {
	baseValues := make(map[string]string, observability.MaxAttributes)
	for index := 0; index < observability.MaxAttributes; index++ {
		baseValues[fmt.Sprintf("base.%d", index)] = "x"
	}
	base, err := observability.NewAttributes(baseValues)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := observability.NewAttributes(map[string]string{"extra": "x"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := mustRecorder(t, nil)
	_, span := recorder.Start(context.Background(), observability.SpanStart{
		Name:       "bounded",
		Attributes: base,
	})
	if err := span.SetAttributes(extra); !errors.Is(
		err,
		observability.ErrInvalidAttributes,
	) {
		t.Fatalf("SetAttributes() error = %v", err)
	}
}

func TestAttributesAndDeliveredRecordsAreImmutableCopies(t *testing.T) {
	source := map[string]string{"route": "/api/v1/files", "method": "GET"}
	attributes, err := observability.NewAttributes(source)
	if err != nil {
		t.Fatal(err)
	}
	source["route"] = "/mutated"
	delete(source, "method")
	exposed := attributes.Values()
	exposed["route"] = "/also-mutated"

	sink := &collectingSink{}
	recorder := mustRecorder(
		t,
		sink,
		observability.WithEntropy(
			bytes.NewReader(bytes.Repeat([]byte{0x51}, 24)),
		),
	)
	_, span := recorder.Start(context.Background(), observability.SpanStart{
		Name:       "http request",
		Kind:       observability.SpanKindServer,
		Attributes: attributes,
	})
	span.End(observability.SpanEnd{})
	metric := observability.Metric{
		Name:       "http.requests",
		Kind:       observability.MetricCounter,
		Value:      1,
		Attributes: attributes,
	}
	if err := recorder.RecordMetric(metric); err != nil {
		t.Fatal(err)
	}
	if !metric.Timestamp.IsZero() {
		t.Fatal("RecordMetric mutated the caller's value")
	}

	spans, metrics := sink.snapshot()
	assertAttributesUnchanged(t, spans[0].Attributes)
	assertAttributesUnchanged(t, metrics[0].Attributes)

	sinkView := spans[0].Attributes.Values()
	sinkView["route"] = "/sink-mutated"
	assertAttributesUnchanged(t, spans[0].Attributes)
}

func TestMetricKindsAndValidationBoundaries(t *testing.T) {
	recorder := mustRecorder(t, nil)
	for _, kind := range []observability.MetricKind{
		observability.MetricCounter,
		observability.MetricHistogram,
		observability.MetricGauge,
	} {
		t.Run(string(kind), func(t *testing.T) {
			if err := recorder.RecordMetric(observability.Metric{
				Name:  "storage.operation.duration",
				Kind:  kind,
				Value: 1.25,
				Unit:  "ms",
			}); err != nil {
				t.Fatalf("valid metric rejected: %v", err)
			}
		})
	}

	cases := []observability.Metric{
		{Kind: observability.MetricCounter, Value: 1},
		{Name: "bad name", Kind: observability.MetricCounter, Value: 1},
		{Name: "metric", Kind: "summary", Value: 1},
		{Name: "metric", Kind: observability.MetricCounter, Value: -1},
		{Name: "metric", Kind: observability.MetricGauge, Value: math.NaN()},
		{Name: "metric", Kind: observability.MetricGauge, Value: math.Inf(1)},
		{
			Name:  "metric",
			Kind:  observability.MetricGauge,
			Value: 1,
			Unit:  "bad unit",
		},
	}
	for index, metric := range cases {
		t.Run(fmt.Sprintf("invalid-%d", index), func(t *testing.T) {
			if err := recorder.RecordMetric(metric); !errors.Is(
				err,
				observability.ErrInvalidMetric,
			) {
				t.Fatalf("RecordMetric() error = %v", err)
			}
		})
	}
}

func TestAttributeValidationBoundaries(t *testing.T) {
	exact := make(map[string]string, observability.MaxAttributes)
	for index := 0; index < observability.MaxAttributes; index++ {
		exact[fmt.Sprintf("key.%d", index)] = strings.Repeat(
			"x",
			observability.MaxAttributeValue,
		)
	}
	if _, err := observability.NewAttributes(exact); err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
	exact["one.too.many"] = "x"
	if _, err := observability.NewAttributes(exact); !errors.Is(
		err,
		observability.ErrInvalidAttributes,
	) {
		t.Fatalf("too many attributes error = %v", err)
	}

	for name, values := range map[string]map[string]string{
		"invalid key":   {"1bad": "value"},
		"control value": {"safe.key": "line\nbreak"},
		"oversized value": {
			"safe.key": strings.Repeat(
				"x",
				observability.MaxAttributeValue+1,
			),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := observability.NewAttributes(values); !errors.Is(
				err,
				observability.ErrInvalidAttributes,
			) {
				t.Fatalf("NewAttributes() error = %v", err)
			}
		})
	}
}

func TestFailedEntropyStillProducesValidContext(t *testing.T) {
	recorder := mustRecorder(
		t,
		nil,
		observability.WithEntropy(failingReader{}),
		observability.WithClock(func() time.Time {
			return time.Unix(123, 456).UTC()
		}),
	)
	ctx, _ := recorder.Start(
		context.Background(),
		observability.SpanStart{Name: "root"},
	)
	if got := observability.TraceParentFromContext(ctx); !traceParentPattern.MatchString(got) {
		t.Fatalf("fallback traceparent = %q", got)
	}
}

func TestRecorderIsRaceFriendlyUnderConcurrentUse(t *testing.T) {
	sink := &collectingSink{}
	clockTick := 0
	entropy := &incrementingReader{}
	recorder := mustRecorder(
		t,
		sink,
		observability.WithClock(func() time.Time {
			clockTick++
			return time.Unix(0, int64(clockTick)).UTC()
		}),
		observability.WithEntropy(entropy),
	)
	attributes, err := observability.NewAttributes(map[string]string{
		"component": "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 128
	var group sync.WaitGroup
	group.Add(goroutines)
	for index := 0; index < goroutines; index++ {
		go func(index int) {
			defer group.Done()
			ctx, span := recorder.Start(
				context.Background(),
				observability.SpanStart{
					Name:       fmt.Sprintf("span %d", index),
					Kind:       observability.SpanKindInternal,
					Attributes: attributes,
				},
			)
			if !traceParentPattern.MatchString(
				observability.TraceParentFromContext(ctx),
			) {
				t.Errorf("goroutine %d received invalid trace context", index)
			}
			var setters sync.WaitGroup
			setters.Add(2)
			go func() {
				defer setters.Done()
				span.SetName(fmt.Sprintf("final span %d", index))
			}()
			go func() {
				defer setters.Done()
				if err := span.SetAttributes(attributes); err != nil {
					t.Errorf(
						"goroutine %d SetAttributes() error = %v",
						index,
						err,
					)
				}
			}()
			span.End(observability.SpanEnd{})
			setters.Wait()
			if err := recorder.RecordMetric(observability.Metric{
				Name:       "concurrent.operations",
				Kind:       observability.MetricCounter,
				Value:      1,
				Attributes: attributes,
			}); err != nil {
				t.Errorf("goroutine %d RecordMetric() error = %v", index, err)
			}
		}(index)
	}
	group.Wait()

	spans, metrics := sink.snapshot()
	if len(spans) != goroutines || len(metrics) != goroutines {
		t.Fatalf(
			"record counts = (%d spans, %d metrics), want (%d, %d)",
			len(spans),
			len(metrics),
			goroutines,
			goroutines,
		)
	}
}

func TestRecorderRejectsInvalidOptions(t *testing.T) {
	var typedNilSink *collectingSink
	if _, err := observability.NewRecorder(typedNilSink); !errors.Is(
		err,
		observability.ErrInvalidOption,
	) {
		t.Fatalf("typed-nil sink error = %v", err)
	}
	if _, err := observability.NewRecorder(nil, nil); !errors.Is(
		err,
		observability.ErrInvalidOption,
	) {
		t.Fatalf("nil option error = %v", err)
	}
	if _, err := observability.NewRecorder(
		nil,
		observability.WithClock(nil),
	); !errors.Is(err, observability.ErrInvalidOption) {
		t.Fatalf("nil clock error = %v", err)
	}
	if _, err := observability.NewRecorder(
		nil,
		observability.WithEntropy(nil),
	); !errors.Is(err, observability.ErrInvalidOption) {
		t.Fatalf("nil entropy error = %v", err)
	}
}

func TestRecorderIsolatesDeploymentSinkPanics(t *testing.T) {
	recorder := mustRecorder(t, panickingSink{})
	ctx, span := recorder.Start(
		context.Background(),
		observability.SpanStart{
			Name: "panic isolation",
			Kind: observability.SpanKindServer,
		},
	)
	if observability.TraceParentFromContext(ctx) == "" {
		t.Fatal("span context was not created")
	}
	span.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
	if err := recorder.RecordMetric(observability.Metric{
		Name:  "test.panic_isolation",
		Kind:  observability.MetricCounter,
		Value: 1,
		Unit:  "1",
	}); err != nil {
		t.Fatalf("metric validation failed: %v", err)
	}
}

func assertAttributesUnchanged(
	t *testing.T,
	attributes observability.Attributes,
) {
	t.Helper()
	if route, ok := attributes.Get("route"); !ok || route != "/api/v1/files" {
		t.Fatalf("route attribute = %q, %v", route, ok)
	}
	if method, ok := attributes.Get("method"); !ok || method != "GET" {
		t.Fatalf("method attribute = %q, %v", method, ok)
	}
}

func mustRecorder(
	t *testing.T,
	sink observability.Sink,
	options ...observability.Option,
) *observability.Recorder {
	t.Helper()
	recorder, err := observability.NewRecorder(sink, options...)
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}

type sequenceClock struct {
	mu     sync.Mutex
	values []time.Time
	index  int
}

func newSequenceClock(values ...time.Time) *sequenceClock {
	return &sequenceClock{values: values}
}

func (c *sequenceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.values) == 0 {
		return time.Time{}
	}
	if c.index >= len(c.values) {
		return c.values[len(c.values)-1]
	}
	value := c.values[c.index]
	c.index++
	return value
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

type panickingSink struct{}

func (panickingSink) RecordSpan(observability.SpanRecord) {
	panic("span sink panic")
}

func (panickingSink) RecordMetric(observability.Metric) {
	panic("metric sink panic")
}

// incrementingReader is intentionally not synchronized. Recorder's serialized
// entropy contract keeps it race-free when the test runs with -race.
type incrementingReader struct {
	next byte
}

func (r *incrementingReader) Read(target []byte) (int, error) {
	for index := range target {
		r.next++
		if r.next == 0 {
			r.next++
		}
		target[index] = r.next
	}
	return len(target), nil
}
