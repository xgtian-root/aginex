package database

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xgtian-root/aginex/backend/framework/observability"
	"github.com/xgtian-root/aginex/backend/internal/config"
)

type databaseObservationSink struct {
	mu      sync.Mutex
	spans   []observability.SpanRecord
	metrics []observability.Metric
}

func (sink *databaseObservationSink) RecordSpan(
	record observability.SpanRecord,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.spans = append(sink.spans, record)
}

func (sink *databaseObservationSink) RecordMetric(
	metric observability.Metric,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.metrics = append(sink.metrics, metric)
}

func (sink *databaseObservationSink) snapshot() (
	[]observability.SpanRecord,
	[]observability.Metric,
) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]observability.SpanRecord(nil), sink.spans...),
		append([]observability.Metric(nil), sink.metrics...)
}

type observedDatabaseRecord struct {
	ID     int64
	Secret string
}

func (observedDatabaseRecord) TableName() string {
	return "observed_database_records"
}

func TestInstallObservabilityRecordsBoundedDatabaseOperations(t *testing.T) {
	db, err := Open(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "observability.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if err := db.Exec(`
		CREATE TABLE observed_database_records (
			id INTEGER PRIMARY KEY,
			secret TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}

	sink := &databaseObservationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallObservability(db, recorder, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := InstallObservability(db, recorder, "sqlite"); err != nil {
		t.Fatalf("repeated installation failed: %v", err)
	}

	ctx, root := recorder.Start(
		context.Background(),
		observability.SpanStart{
			Name: "test request",
			Kind: observability.SpanKindServer,
		},
	)
	secret := "TOP-SECRET-bound-value"
	record := observedDatabaseRecord{ID: 1, Secret: secret}
	if err := db.WithContext(ctx).Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	var loaded observedDatabaseRecord
	if err := db.WithContext(ctx).
		Where("id = ?", record.ID).
		First(&loaded).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).
		Model(&observedDatabaseRecord{}).
		Where("id = ?", record.ID).
		Update("secret", secret+"-updated").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).
		Exec(
			"UPDATE observed_database_records SET secret = ? WHERE id = ?",
			secret,
			record.ID,
		).Error; err != nil {
		t.Fatal(err)
	}
	var selected string
	if err := db.WithContext(ctx).
		Raw(
			"SELECT secret FROM observed_database_records WHERE id = ?",
			record.ID,
		).
		Row().
		Scan(&selected); err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).
		Delete(&observedDatabaseRecord{}, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := db.WithContext(canceled).
		Where("secret = ?", secret).
		Find(&loaded).Error; err == nil {
		t.Fatal("canceled query unexpectedly succeeded")
	}

	spans, metrics := sink.snapshot()
	operationCounts := make(map[string]int)
	errorOutcomes := 0
	for _, span := range spans {
		if span.Kind != observability.SpanKindClient {
			t.Fatalf("database span kind = %q", span.Kind)
		}
		if !span.HasParent || span.Parent.SpanID() != root.Context().SpanID() {
			t.Fatal("database span did not inherit the request span")
		}
		values := span.Attributes.Values()
		assertDatabaseAttributeKeys(t, values)
		operationCounts[values["db.operation"]]++
		if values["outcome"] == string(observability.OutcomeError) {
			errorOutcomes++
		}
		if span.Error != "" {
			t.Fatalf("database error text escaped into telemetry: %q", span.Error)
		}
	}
	wantCounts := map[string]int{
		"create": 1,
		"query":  2,
		"update": 1,
		"delete": 1,
		"raw":    1,
		"row":    1,
	}
	if fmt.Sprint(operationCounts) != fmt.Sprint(wantCounts) {
		t.Fatalf("database operation counts = %v, want %v", operationCounts, wantCounts)
	}
	if errorOutcomes != 1 {
		t.Fatalf("error outcomes = %d, want 1", errorOutcomes)
	}
	if len(metrics) != len(spans)*2 {
		t.Fatalf("database metrics = %d, want %d", len(metrics), len(spans)*2)
	}
	for _, metric := range metrics {
		if metric.Name != "db.client.operations" &&
			metric.Name != "db.client.duration" {
			t.Fatalf("unexpected database metric %q", metric.Name)
		}
		assertDatabaseAttributeKeys(t, metric.Attributes.Values())
	}
	assertDatabaseTelemetryExcludes(
		t,
		spans,
		metrics,
		secret,
		"observed_database_records",
		"SELECT secret",
		"UPDATE observed",
	)
	root.End(observability.SpanEnd{Outcome: observability.OutcomeOK})
}

func TestRepeatedInstallMovesFutureOperationsToLatestRecorder(t *testing.T) {
	db, err := Open(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "recorder-swap.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	firstSink := &databaseObservationSink{}
	first, err := observability.NewRecorder(firstSink)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallObservability(db, first, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"CREATE TABLE recorder_swap_records (id INTEGER PRIMARY KEY)",
	).Error; err != nil {
		t.Fatal(err)
	}
	firstSpans, _ := firstSink.snapshot()
	if len(firstSpans) == 0 {
		t.Fatal("first recorder received no database span")
	}

	secondSink := &databaseObservationSink{}
	second, err := observability.NewRecorder(secondSink)
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallObservability(db, second, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO recorder_swap_records (id) VALUES (?)",
		1,
	).Error; err != nil {
		t.Fatal(err)
	}
	firstAfter, _ := firstSink.snapshot()
	secondSpans, _ := secondSink.snapshot()
	if len(firstAfter) != len(firstSpans) {
		t.Fatalf(
			"first recorder spans after replacement = %d, want %d",
			len(firstAfter),
			len(firstSpans),
		)
	}
	if len(secondSpans) == 0 {
		t.Fatal("latest recorder received no database span")
	}
	if err := InstallObservability(db, second, "postgres"); err == nil {
		t.Fatal("reinstall with a different database system succeeded")
	}
}

func TestRecordPoolStatsUsesGaugesAndBoundedAttributes(t *testing.T) {
	db, err := Open(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "pool.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	sink := &databaseObservationSink{}
	recorder, err := observability.NewRecorder(sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordPoolStats(recorder, sqlDB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	_, metrics := sink.snapshot()
	if len(metrics) != 6 {
		t.Fatalf("pool metrics = %d, want 6", len(metrics))
	}
	for _, metric := range metrics {
		if metric.Kind != observability.MetricGauge {
			t.Fatalf("pool metric %q kind = %q", metric.Name, metric.Kind)
		}
		values := metric.Attributes.Values()
		assertDatabaseAttributeKeys(t, values)
		if values["db.operation"] != "pool" || values["outcome"] != "ok" {
			t.Fatalf("pool attributes = %v", values)
		}
	}
}

func assertDatabaseAttributeKeys(
	t *testing.T,
	values map[string]string,
) {
	t.Helper()
	if len(values) != 3 ||
		values["db.system"] != "sqlite" ||
		values["db.operation"] == "" ||
		values["outcome"] == "" {
		t.Fatalf("database attributes = %v", values)
	}
	for key := range values {
		switch key {
		case "db.system", "db.operation", "outcome":
		default:
			t.Fatalf("unexpected database attribute %q", key)
		}
	}
}

func assertDatabaseTelemetryExcludes(
	t *testing.T,
	spans []observability.SpanRecord,
	metrics []observability.Metric,
	forbidden ...string,
) {
	t.Helper()
	var rendered strings.Builder
	for _, span := range spans {
		fmt.Fprintf(
			&rendered,
			"%s %s %s %v",
			span.Name,
			span.Error,
			span.Outcome,
			span.Attributes.Values(),
		)
	}
	for _, metric := range metrics {
		fmt.Fprintf(
			&rendered,
			"%s %v",
			metric.Name,
			metric.Attributes.Values(),
		)
	}
	output := rendered.String()
	for _, value := range forbidden {
		if strings.Contains(output, value) {
			t.Fatalf("telemetry contains forbidden value %q: %s", value, output)
		}
	}
}
