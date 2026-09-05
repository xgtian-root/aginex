package multipartcleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/xgtian-root/aginex/backend/internal/domain"
	platformstorage "github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
)

const (
	DefaultScanInterval = 15 * time.Minute
	DefaultScanBatch    = 100
	localOrphanGrace    = 48 * time.Hour
)

type ScannerConfig struct {
	Interval time.Duration
	Batch    int
}

// Scanner provides an API-process safety net for deployments without durable
// jobs, deliveries interrupted around provider abort, and completion or final
// verification abandoned past the transfer deadline. Each pass is bounded;
// completion recovery has a separate CAS lease and never aborts provider state.
type Scanner struct {
	db       *gorm.DB
	handler  *Handler
	interval time.Duration
	batch    int

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewScanner(
	db *gorm.DB,
	handler *Handler,
	config ScannerConfig,
) (*Scanner, error) {
	if db == nil || handler == nil {
		return nil, errors.New("multipart cleanup scanner database and handler are required")
	}
	if config.Interval == 0 {
		config.Interval = DefaultScanInterval
	}
	if config.Batch == 0 {
		config.Batch = DefaultScanBatch
	}
	if config.Interval < 0 || config.Batch < 1 || config.Batch > DefaultScanBatch {
		return nil, errors.New("multipart cleanup scanner configuration is invalid")
	}
	return &Scanner{db: db, handler: handler, interval: config.Interval, batch: config.Batch}, nil
}

// Scan repairs at most Batch sessions. It processes every selected row even
// if another row fails, then returns the joined error to its caller.
func (scanner *Scanner) Scan(ctx context.Context) (int, error) {
	if scanner == nil {
		return 0, errors.New("multipart cleanup scanner is required")
	}
	now := scanner.handler.clock().UTC()
	var sessions []domain.FileUploadSession
	result := scanner.db.WithContext(ctx).
		Select("id", "status", "updated_at").
		Where(
			"(status = ? AND expires_at <= ?) OR status IN ? OR (status IN ? AND updated_at <= ?)",
			domain.FileUploadSessionStatusActive,
			now,
			[]domain.FileUploadSessionStatus{
				domain.FileUploadSessionStatusCancelling,
				domain.FileUploadSessionStatusExpiring,
			},
			[]domain.FileUploadSessionStatus{
				domain.FileUploadSessionStatusCompleting,
				domain.FileUploadSessionStatusVerifying,
			},
			now.Add(-CompletionRecoveryGrace),
		).
		Order("updated_at ASC").
		Limit(scanner.batch).
		Find(&sessions)
	if result.Error != nil {
		return 0, result.Error
	}
	var scanErr error
	for _, session := range sessions {
		if session.Status == domain.FileUploadSessionStatusCompleting ||
			session.Status == domain.FileUploadSessionStatusVerifying {
			if err := scanner.handler.Recover(ctx, session.ID); err != nil {
				scanErr = errors.Join(scanErr, fmt.Errorf("recover selected upload session: %w", err))
			}
			continue
		}
		cause := CauseExpiry
		if session.Status == domain.FileUploadSessionStatusCancelling {
			cause = CauseCancel
		}
		if err := scanner.handler.Cleanup(ctx, session.ID, cause); err != nil {
			scanErr = errors.Join(scanErr, fmt.Errorf("cleanup selected upload session: %w", err))
		}
	}
	if err := scanner.cleanLocalOrphans(ctx, now); err != nil {
		scanErr = errors.Join(scanErr, fmt.Errorf("clean local multipart orphans: %w", err))
	}
	return len(sessions), scanErr
}

func (scanner *Scanner) cleanLocalOrphans(ctx context.Context, now time.Time) error {
	remaining := scanner.batch
	seen := make(map[*platformstorage.Local]struct{})
	for _, profile := range scanner.handler.registry.Profiles() {
		if remaining == 0 {
			break
		}
		if profile.StorageConfig().Driver != "local" {
			continue
		}
		store, err := scanner.handler.registry.Resolve(profile.ID)
		if err != nil {
			return err
		}
		local, ok := platformstorage.AsLocal(store)
		if !ok {
			continue
		}
		if _, duplicate := seen[local]; duplicate {
			continue
		}
		seen[local] = struct{}{}
		staging, err := local.ListMultipartStaging(ctx, now.Add(-localOrphanGrace), remaining)
		if err != nil {
			return err
		}
		for _, candidate := range staging {
			var owner domain.FileUploadSession
			ownerErr := scanner.db.WithContext(ctx).
				Joins("JOIN file_objects ON file_objects.id = file_upload_sessions.file_id").
				Where("file_upload_sessions.provider_upload_id = ? AND file_objects.storage_profile_id = ?", candidate.ProviderUploadID, profile.ID).
				First(&owner).Error
			if ownerErr != nil && !errors.Is(ownerErr, gorm.ErrRecordNotFound) {
				return ownerErr
			}
			if errors.Is(ownerErr, gorm.ErrRecordNotFound) || multipartSessionTerminal(owner.Status) {
				if err := local.RemoveMultipartStaging(ctx, candidate.ProviderUploadID); err != nil {
					return err
				}
			}
			remaining--
			if remaining == 0 {
				break
			}
		}
	}
	return nil
}

func multipartSessionTerminal(status domain.FileUploadSessionStatus) bool {
	return status == domain.FileUploadSessionStatusCompleted ||
		status == domain.FileUploadSessionStatusCancelled ||
		status == domain.FileUploadSessionStatusExpired
}

// Start detaches the long-running loop from the call-scoped lifecycle context.
// The first bounded pass runs immediately in the background.
func (scanner *Scanner) Start(ctx context.Context) error {
	if scanner == nil {
		return errors.New("multipart cleanup scanner is required")
	}
	if ctx == nil {
		return errors.New("multipart cleanup scanner context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scanner.mu.Lock()
	defer scanner.mu.Unlock()
	if scanner.cancel != nil {
		return nil
	}
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	scanner.cancel = cancel
	scanner.done = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(scanner.interval)
		defer ticker.Stop()
		for {
			if _, err := scanner.Scan(runContext); err != nil && runContext.Err() == nil {
				// Do not attach the provider error: SDK errors may contain signed
				// request capabilities. The signal is intentionally sanitized.
				slog.Error("multipart upload repair scan failed", "component", "multipart-cleanup-scanner")
			}
			select {
			case <-runContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (scanner *Scanner) Stop(ctx context.Context) error {
	if scanner == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("multipart cleanup scanner context is required")
	}
	scanner.mu.Lock()
	cancel := scanner.cancel
	done := scanner.done
	scanner.cancel = nil
	scanner.done = nil
	scanner.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
