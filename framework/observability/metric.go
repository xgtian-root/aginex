package observability

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	MaxMetricName = 160
	MaxMetricUnit = 32
)

var (
	ErrInvalidMetric  = errors.New("observability: invalid metric")
	metricNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)
	metricUnitPattern = regexp.MustCompile(`^[A-Za-z0-9%/._-]*$`)
)

type MetricKind string

const (
	MetricCounter   MetricKind = "counter"
	MetricHistogram MetricKind = "histogram"
	MetricGauge     MetricKind = "gauge"
)

// Metric is an immutable-by-value metric point. A zero Timestamp is populated
// by Recorder.RecordMetric. Attributes must be created with NewAttributes.
type Metric struct {
	Name       string
	Kind       MetricKind
	Value      float64
	Unit       string
	Timestamp  time.Time
	Attributes Attributes
}

// Validate checks the bounded provider-neutral metric contract.
func (m Metric) Validate() error {
	if len(m.Name) == 0 ||
		len(m.Name) > MaxMetricName ||
		!metricNamePattern.MatchString(m.Name) {
		return fmt.Errorf("%w: invalid name", ErrInvalidMetric)
	}
	switch m.Kind {
	case MetricCounter, MetricHistogram, MetricGauge:
	default:
		return fmt.Errorf("%w: invalid kind %q", ErrInvalidMetric, m.Kind)
	}
	if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
		return fmt.Errorf("%w: value must be finite", ErrInvalidMetric)
	}
	if m.Kind == MetricCounter && m.Value < 0 {
		return fmt.Errorf(
			"%w: counter increments cannot be negative",
			ErrInvalidMetric,
		)
	}
	if len(m.Unit) > MaxMetricUnit || !metricUnitPattern.MatchString(m.Unit) {
		return fmt.Errorf("%w: invalid unit", ErrInvalidMetric)
	}
	if m.Unit != strings.TrimSpace(m.Unit) {
		return fmt.Errorf("%w: invalid unit", ErrInvalidMetric)
	}
	if err := m.Attributes.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMetric, err)
	}
	return nil
}

func (m Metric) clone() Metric {
	m.Attributes = m.Attributes.clone()
	return m
}
