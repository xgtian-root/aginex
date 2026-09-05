package multipartcleanup

import (
	"context"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/backend/internal/domain"
)

func TestScannerBoundsPassAndRepairsCancellingSession(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	firstFile, first := seedUploadSession(
		t, db, registry, profileID,
		domain.FileUploadSessionStatusCancelling,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
	)
	_, second := seedUploadSession(
		t, db, registry, profileID,
		domain.FileUploadSessionStatusActive,
		cleanupTestNow.Add(-time.Minute),
		"pending",
		true,
	)
	if err := db.Model(&domain.FileUploadSession{}).
		Where("id = ?", first.ID).
		Update("updated_at", cleanupTestNow.Add(-2*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	handler := cleanupTestHandler(t, db, registry)
	scanner, err := NewScanner(db, handler, ScannerConfig{Interval: time.Hour, Batch: 1})
	if err != nil {
		t.Fatal(err)
	}

	selected, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if selected != 1 {
		t.Fatalf("selected = %d, want 1", selected)
	}
	assertCleanupTerminalState(
		t, db, firstFile.ID, first.ID,
		"deleted", domain.FileUploadSessionStatusCancelled,
	)
	var pending domain.FileUploadSession
	if err := db.First(&pending, "id = ?", second.ID).Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != domain.FileUploadSessionStatusActive {
		t.Fatalf("second session status = %q", pending.Status)
	}
}

func TestScannerLifecycleDetachesAndStops(t *testing.T) {
	db, registry, _ := cleanupTestRuntime(t)
	handler := cleanupTestHandler(t, db, registry)
	scanner, err := NewScanner(db, handler, ScannerConfig{Interval: time.Millisecond, Batch: 1})
	if err != nil {
		t.Fatal(err)
	}
	startContext, cancel := context.WithCancel(context.Background())
	if err := scanner.Start(startContext); err != nil {
		t.Fatal(err)
	}
	cancel()
	stopContext, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := scanner.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	if err := scanner.Stop(stopContext); err != nil {
		t.Fatalf("repeated stop = %v", err)
	}
}

func TestScannerSelectsOnlyCompletionStatesOutsideRecoveryWindow(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	_, fresh := seedUploadSession(
		t, db, registry, profileID,
		domain.FileUploadSessionStatusCompleting,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
	)
	_, stale := seedUploadSession(
		t, db, registry, profileID,
		domain.FileUploadSessionStatusCompleting,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
	)
	makeRecoveryStale(t, db, stale.ID)
	handler := cleanupTestHandler(t, db, registry)
	scanner, err := NewScanner(db, handler, ScannerConfig{Interval: time.Hour, Batch: 2})
	if err != nil {
		t.Fatal(err)
	}

	selected, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if selected != 1 {
		t.Fatalf("selected = %d, want only stale completion", selected)
	}
	var freshStored domain.FileUploadSession
	if err := db.First(&freshStored, "id = ?", fresh.ID).Error; err != nil {
		t.Fatal(err)
	}
	if freshStored.Status != domain.FileUploadSessionStatusCompleting {
		t.Fatalf("fresh session status = %q", freshStored.Status)
	}
	var staleStored domain.FileUploadSession
	if err := db.First(&staleStored, "id = ?", stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if staleStored.Status != domain.FileUploadSessionStatusActive {
		t.Fatalf("stale session status = %q, want active after safe reconciliation", staleStored.Status)
	}
}

func TestScannerRejectsUnboundedConfiguration(t *testing.T) {
	db, registry, _ := cleanupTestRuntime(t)
	handler := cleanupTestHandler(t, db, registry)
	if _, err := NewScanner(db, handler, ScannerConfig{Batch: DefaultScanBatch + 1}); err == nil {
		t.Fatal("scanner accepted an unbounded batch")
	}
}
