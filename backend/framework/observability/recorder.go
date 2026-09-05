package observability

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxSpanName  = 160
	MaxSpanError = 2048
)

var ErrInvalidOption = errors.New("observability: invalid recorder option")

var fallbackEntropySequence atomic.Uint64

// Sink receives completed immutable observations. Its methods may be invoked
// concurrently and implementations must be concurrency-safe. Export buffering,
// sampling, retries, and provider-specific translation belong in the
// deployment adapter.
type Sink interface {
	RecordSpan(SpanRecord)
	RecordMetric(Metric)
}

type SpanKind string

const (
	SpanKindServer   SpanKind = "server"
	SpanKindClient   SpanKind = "client"
	SpanKindProducer SpanKind = "producer"
	SpanKindConsumer SpanKind = "consumer"
	SpanKindInternal SpanKind = "internal"
)

type Outcome string

const (
	OutcomeOK    Outcome = "ok"
	OutcomeError Outcome = "error"
)

// SpanStart describes a span. Attributes must be created with NewAttributes.
type SpanStart struct {
	Name       string
	Kind       SpanKind
	Attributes Attributes
}

// SpanEnd describes the final result. Err is converted immediately to a
// bounded string; callers should avoid errors containing credentials or user
// content.
type SpanEnd struct {
	Outcome Outcome
	Err     error
}

// SpanRecord is the immutable-by-value result delivered to a Sink.
type SpanRecord struct {
	Name       string
	Kind       SpanKind
	Context    SpanContext
	Parent     SpanContext
	HasParent  bool
	StartedAt  time.Time
	EndedAt    time.Time
	Duration   time.Duration
	Outcome    Outcome
	Error      string
	Attributes Attributes
}

// Span ends at most once, even when multiple goroutines call End.
type Span struct {
	recorder   *Recorder
	context    SpanContext
	parent     SpanContext
	hasParent  bool
	mu         sync.Mutex
	ended      bool
	name       string
	kind       SpanKind
	startedAt  time.Time
	attributes Attributes
	endOnce    sync.Once
}

// Context returns this span's immutable W3C identity.
func (s *Span) Context() SpanContext {
	if s == nil {
		return SpanContext{}
	}
	return s.context
}

// SetName replaces the span name until End takes its final snapshot. This is
// useful for HTTP middleware that learns the matched route template only after
// the handler chain runs. Calls racing with End are safe; calls after End are
// ignored.
func (s *Span) SetName(name string) {
	if s == nil {
		return
	}
	name = normalizeSpanName(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.name = name
}

// SetAttributes merges immutable attributes into the span until End takes its
// final snapshot. Later values replace matching keys. The merged set retains
// the same cardinality and value bounds as NewAttributes. Calls racing with End
// are safe; calls after End are ignored.
func (s *Span) SetAttributes(attributes Attributes) error {
	if s == nil {
		return nil
	}
	if err := attributes.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return nil
	}
	merged, err := s.attributes.merge(attributes)
	if err != nil {
		return err
	}
	s.attributes = merged
	return nil
}

// End records the first supplied result and ignores subsequent calls.
func (s *Span) End(end SpanEnd) {
	if s == nil || s.recorder == nil {
		return
	}
	s.endOnce.Do(func() {
		endedAt := s.recorder.now()
		duration := endedAt.Sub(s.startedAt)
		if duration < 0 {
			duration = 0
		}
		s.mu.Lock()
		s.ended = true
		name := s.name
		attributes := s.attributes.clone()
		s.mu.Unlock()
		outcome := normalizeOutcome(end.Outcome, end.Err)
		record := SpanRecord{
			Name:       name,
			Kind:       s.kind,
			Context:    s.context,
			Parent:     s.parent,
			HasParent:  s.hasParent,
			StartedAt:  s.startedAt,
			EndedAt:    endedAt,
			Duration:   duration,
			Outcome:    outcome,
			Error:      normalizeError(end.Err),
			Attributes: attributes,
		}
		s.recorder.recordSpan(record)
	})
}

// Recorder creates W3C trace context and forwards completed observations to an
// optional Sink.
type Recorder struct {
	sink Sink

	clock   func() time.Time
	clockMu sync.Mutex

	entropy   io.Reader
	entropyMu sync.Mutex
}

type Option func(*Recorder) error

// WithClock injects a deterministic clock. Recorder serializes calls, so the
// function does not need to be concurrency-safe.
func WithClock(clock func() time.Time) Option {
	return func(recorder *Recorder) error {
		if clock == nil {
			return fmt.Errorf("%w: clock is nil", ErrInvalidOption)
		}
		recorder.clock = clock
		return nil
	}
}

// WithEntropy injects the identifier source. Recorder serializes reads, so the
// reader does not need to be concurrency-safe. Trace identifiers are not
// security tokens; a valid fallback is used if the reader fails.
func WithEntropy(entropy io.Reader) Option {
	return func(recorder *Recorder) error {
		if entropy == nil {
			return fmt.Errorf("%w: entropy reader is nil", ErrInvalidOption)
		}
		recorder.entropy = entropy
		return nil
	}
}

// NewRecorder constructs a provider-neutral Recorder. A nil Sink disables
// export while retaining complete trace-context creation and propagation.
func NewRecorder(sink Sink, options ...Option) (*Recorder, error) {
	if sink != nil {
		value := reflect.ValueOf(sink)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface,
			reflect.Map, reflect.Pointer, reflect.Slice:
			if value.IsNil() {
				return nil, fmt.Errorf(
					"%w: sink is typed nil",
					ErrInvalidOption,
				)
			}
		}
	}
	recorder := &Recorder{
		sink:    sink,
		clock:   time.Now,
		entropy: rand.Reader,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: option is nil", ErrInvalidOption)
		}
		if err := option(recorder); err != nil {
			return nil, err
		}
	}
	return recorder, nil
}

// Start creates a root span or a child of the current/extracted span and
// returns a context ready for downstream propagation.
func (r *Recorder) Start(
	ctx context.Context,
	start SpanStart,
) (context.Context, *Span) {
	if r == nil {
		r, _ = NewRecorder(nil)
	}
	parent, hasParent := SpanContextFromContext(ctx)
	current := SpanContext{
		traceFlags: 0x01,
		remote:     false,
		valid:      true,
	}
	if hasParent {
		current.traceID = parent.traceID
		current.traceFlags = parent.traceFlags
	} else {
		copy(current.traceID[:], r.nextIdentifier(len(current.traceID)))
	}
	copy(current.spanID[:], r.nextIdentifier(len(current.spanID)))
	startedAt := r.now()
	span := &Span{
		recorder:   r,
		context:    current,
		parent:     parent,
		hasParent:  hasParent,
		name:       normalizeSpanName(start.Name),
		kind:       normalizeSpanKind(start.Kind),
		startedAt:  startedAt,
		attributes: start.Attributes.clone(),
	}
	return contextWithSpanContext(ctx, current), span
}

// RecordMetric validates, timestamps, copies, and forwards a metric point. Nil
// Sink recorders still validate the contract.
func (r *Recorder) RecordMetric(metric Metric) error {
	if r == nil {
		return fmt.Errorf("%w: recorder is nil", ErrInvalidOption)
	}
	if err := metric.Validate(); err != nil {
		return err
	}
	metric = metric.clone()
	if metric.Timestamp.IsZero() {
		metric.Timestamp = r.now()
	} else {
		metric.Timestamp = metric.Timestamp.UTC()
	}
	if r.sink != nil {
		r.recordMetric(metric)
	}
	return nil
}

func (r *Recorder) recordSpan(record SpanRecord) {
	if r == nil || r.sink == nil {
		return
	}
	defer func() {
		// Observability is never a business consistency boundary. A broken
		// deployment adapter may drop telemetry, but it must not turn a
		// successful request or worker settlement into a process panic.
		_ = recover()
	}()
	r.sink.RecordSpan(record)
}

func (r *Recorder) recordMetric(metric Metric) {
	if r == nil || r.sink == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	r.sink.RecordMetric(metric)
}

func (r *Recorder) now() time.Time {
	r.clockMu.Lock()
	defer r.clockMu.Unlock()
	return r.clock().UTC()
}

func (r *Recorder) nextIdentifier(size int) []byte {
	identifier := make([]byte, size)
	r.entropyMu.Lock()
	_, err := io.ReadFull(r.entropy, identifier)
	r.entropyMu.Unlock()
	if err == nil && !allZero(identifier) {
		return identifier
	}

	sequence := fallbackEntropySequence.Add(1)
	var sequenceBytes [8]byte
	binary.BigEndian.PutUint64(sequenceBytes[:], sequence)
	sum := sha256.Sum256(append(
		[]byte(fmt.Sprintf("%d/observability/", r.now().UnixNano())),
		sequenceBytes[:]...,
	))
	copy(identifier, sum[:])
	if allZero(identifier) {
		identifier[len(identifier)-1] = 1
	}
	return identifier
}

func normalizeSpanKind(kind SpanKind) SpanKind {
	switch kind {
	case SpanKindServer,
		SpanKindClient,
		SpanKindProducer,
		SpanKindConsumer,
		SpanKindInternal:
		return kind
	default:
		return SpanKindInternal
	}
}

func normalizeOutcome(outcome Outcome, err error) Outcome {
	if err != nil || outcome == OutcomeError {
		return OutcomeError
	}
	return OutcomeOK
}

func normalizeSpanName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unnamed"
	}
	var normalized strings.Builder
	for _, character := range name {
		if unicode.IsControl(character) {
			character = '_'
		}
		if normalized.Len()+utf8.RuneLen(character) > MaxSpanName {
			break
		}
		normalized.WriteRune(character)
	}
	if normalized.Len() == 0 {
		return "unnamed"
	}
	return normalized.String()
}

func normalizeError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	var normalized strings.Builder
	for _, character := range message {
		if unicode.IsControl(character) {
			character = ' '
		}
		if normalized.Len()+utf8.RuneLen(character) > MaxSpanError {
			break
		}
		normalized.WriteRune(character)
	}
	return normalized.String()
}
