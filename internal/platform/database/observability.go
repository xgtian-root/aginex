package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/xgtian-root/aginex/framework/observability"
	"gorm.io/gorm"
)

const (
	observabilityPluginName = "aginex:observability"
	observationStateKey     = "aginex:observability:state"
)

type observabilityPlugin struct {
	mu       sync.RWMutex
	recorder *observability.Recorder
	system   string
}

type operationObservation struct {
	operation string
	startedAt time.Time
	span      *observability.Span
	recorder  *observability.Recorder
	system    string
}

// InstallObservability installs one provider-neutral observation callback
// around each GORM operation. Repeated installation on the same GORM root is
// harmless; future operations use the most recently supplied recorder. An
// operation already in flight retains the recorder with which it started.
func InstallObservability(
	db *gorm.DB,
	recorder *observability.Recorder,
	system string,
) error {
	if db == nil {
		return errors.New("database observability requires a GORM database")
	}
	if recorder == nil {
		return errors.New("database observability requires a recorder")
	}
	normalizedSystem, err := normalizeDatabaseSystem(system, db.Dialector.Name())
	if err != nil {
		return err
	}
	if registered, exists := db.Config.Plugins[observabilityPluginName]; exists {
		plugin, ok := registered.(*observabilityPlugin)
		if !ok {
			return errors.New(
				"database observability plugin name is owned by an incompatible implementation",
			)
		}
		plugin.mu.Lock()
		defer plugin.mu.Unlock()
		if plugin.system != normalizedSystem {
			return fmt.Errorf(
				"database observability system already configured as %q",
				plugin.system,
			)
		}
		plugin.recorder = recorder
		return nil
	}
	err = db.Use(&observabilityPlugin{
		recorder: recorder,
		system:   normalizedSystem,
	})
	if errors.Is(err, gorm.ErrRegistered) {
		return nil
	}
	return err
}

func (plugin *observabilityPlugin) Name() string {
	return observabilityPluginName
}

func (plugin *observabilityPlugin) Initialize(db *gorm.DB) error {
	type callbackPair struct {
		before func(string, func(*gorm.DB)) error
		after  func(string, func(*gorm.DB)) error
		name   string
	}
	callbacks := []callbackPair{
		{
			before: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Create().Before("*").Register(name, callback)
			},
			after: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Create().After("*").Register(name, callback)
			},
			name: "create",
		},
		{
			before: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Query().Before("*").Register(name, callback)
			},
			after: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Query().After("*").Register(name, callback)
			},
			name: "query",
		},
		{
			before: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Update().Before("*").Register(name, callback)
			},
			after: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Update().After("*").Register(name, callback)
			},
			name: "update",
		},
		{
			before: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Delete().Before("*").Register(name, callback)
			},
			after: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Delete().After("*").Register(name, callback)
			},
			name: "delete",
		},
		{
			before: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Raw().Before("*").Register(name, callback)
			},
			after: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Raw().After("*").Register(name, callback)
			},
			name: "raw",
		},
		{
			before: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Row().Before("*").Register(name, callback)
			},
			after: func(name string, callback func(*gorm.DB)) error {
				return db.Callback().Row().After("*").Register(name, callback)
			},
			name: "row",
		},
	}
	for _, current := range callbacks {
		operation := current.name
		if err := current.before(
			observabilityPluginName+":before:"+operation,
			plugin.start(operation),
		); err != nil {
			return fmt.Errorf("register %s observation start: %w", operation, err)
		}
		if err := current.after(
			observabilityPluginName+":after:"+operation,
			plugin.finish,
		); err != nil {
			return fmt.Errorf("register %s observation finish: %w", operation, err)
		}
	}
	return nil
}

func (plugin *observabilityPlugin) start(
	operation string,
) func(*gorm.DB) {
	return func(db *gorm.DB) {
		plugin.mu.RLock()
		recorder := plugin.recorder
		system := plugin.system
		plugin.mu.RUnlock()
		attributes := databaseAttributes(
			system,
			operation,
			"",
		)
		ctx, span := recorder.Start(
			db.Statement.Context,
			observability.SpanStart{
				Name:       "db " + operation,
				Kind:       observability.SpanKindClient,
				Attributes: attributes,
			},
		)
		db.Statement.Context = ctx
		db.Statement.Settings.Store(
			observationStateKey,
			operationObservation{
				operation: operation,
				startedAt: time.Now(),
				span:      span,
				recorder:  recorder,
				system:    system,
			},
		)
	}
}

func (plugin *observabilityPlugin) finish(db *gorm.DB) {
	value, ok := db.Statement.Settings.LoadAndDelete(observationStateKey)
	if !ok {
		return
	}
	observation, ok := value.(operationObservation)
	if !ok {
		return
	}
	outcome := observability.OutcomeOK
	if db.Error != nil {
		outcome = observability.OutcomeError
	}
	attributes := databaseAttributes(
		observation.system,
		observation.operation,
		string(outcome),
	)
	_ = observation.span.SetAttributes(attributes)
	// Database errors can contain SQL or bound values. The caller still
	// receives db.Error, while telemetry records only the bounded outcome.
	observation.span.End(observability.SpanEnd{Outcome: outcome})
	durationMilliseconds := float64(time.Since(observation.startedAt)) /
		float64(time.Millisecond)
	_ = observation.recorder.RecordMetric(observability.Metric{
		Name:       "db.client.operations",
		Kind:       observability.MetricCounter,
		Value:      1,
		Unit:       "1",
		Attributes: attributes,
	})
	_ = observation.recorder.RecordMetric(observability.Metric{
		Name:       "db.client.duration",
		Kind:       observability.MetricHistogram,
		Value:      durationMilliseconds,
		Unit:       "ms",
		Attributes: attributes,
	})
}

// RecordPoolStats exports an instantaneous, low-cardinality snapshot of a
// database/sql connection pool. Cumulative wait values are gauges because this
// helper may be invoked repeatedly.
func RecordPoolStats(
	recorder *observability.Recorder,
	pool *sql.DB,
	system string,
) error {
	if recorder == nil {
		return errors.New("database pool observation requires a recorder")
	}
	if pool == nil {
		return errors.New("database pool observation requires a SQL database")
	}
	normalizedSystem, err := normalizeDatabaseSystem(system, system)
	if err != nil {
		return err
	}
	attributes := databaseAttributes(normalizedSystem, "pool", "ok")
	stats := pool.Stats()
	points := []observability.Metric{
		{
			Name:  "db.pool.connections.max_open",
			Kind:  observability.MetricGauge,
			Value: float64(stats.MaxOpenConnections),
			Unit:  "connections",
		},
		{
			Name:  "db.pool.connections.open",
			Kind:  observability.MetricGauge,
			Value: float64(stats.OpenConnections),
			Unit:  "connections",
		},
		{
			Name:  "db.pool.connections.in_use",
			Kind:  observability.MetricGauge,
			Value: float64(stats.InUse),
			Unit:  "connections",
		},
		{
			Name:  "db.pool.connections.idle",
			Kind:  observability.MetricGauge,
			Value: float64(stats.Idle),
			Unit:  "connections",
		},
		{
			Name:  "db.pool.wait.count",
			Kind:  observability.MetricGauge,
			Value: float64(stats.WaitCount),
			Unit:  "1",
		},
		{
			Name:  "db.pool.wait.duration",
			Kind:  observability.MetricGauge,
			Value: float64(stats.WaitDuration) / float64(time.Millisecond),
			Unit:  "ms",
		},
	}
	var recordErrors []error
	for _, point := range points {
		point.Attributes = attributes
		if err := recorder.RecordMetric(point); err != nil {
			recordErrors = append(recordErrors, err)
		}
	}
	return errors.Join(recordErrors...)
}

func normalizeDatabaseSystem(candidate, fallback string) (string, error) {
	system := strings.ToLower(strings.TrimSpace(candidate))
	if system == "" {
		system = strings.ToLower(strings.TrimSpace(fallback))
	}
	switch system {
	case "sqlite":
		return "sqlite", nil
	case "postgres", "postgresql":
		return "postgresql", nil
	case "mysql":
		return "mysql", nil
	default:
		return "", fmt.Errorf("unsupported observable database system %q", system)
	}
}

func databaseAttributes(
	system string,
	operation string,
	outcome string,
) observability.Attributes {
	values := map[string]string{
		"db.system":    system,
		"db.operation": operation,
	}
	if outcome != "" {
		values["outcome"] = outcome
	}
	attributes, _ := observability.NewAttributes(values)
	return attributes
}
