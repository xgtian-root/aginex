package devtools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	frameworkaudit "github.com/xgtian-root/aginex/server/framework/audit"
	"github.com/xgtian-root/aginex/server/framework/observability"
	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
	"github.com/xgtian-root/aginex/server/framework/uow"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/auditlog"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	platformstorage "github.com/xgtian-root/aginex/server/internal/platform/storage"
	"gorm.io/gorm"
)

type reconcileStorageDependencies struct {
	loadConfig func() (config.Config, error)
	openDB     func(context.Context, config.Database) (*gorm.DB, error)
	registry   func(context.Context, config.Config) (reconcileStorageResolver, error)
	now        func() time.Time
}

type reconcileStorageResolver interface {
	Resolve(string) (platformstorage.Storage, error)
	ResolveLegacy(string, string) (string, platformstorage.Storage, error)
}

func newReconcileStoragePresentationCommand(
	dependencies reconcileStorageDependencies,
) *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile-storage-presentation",
		Short: "Reconcile verified OSS object presentation metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return reconcileStoragePresentations(cmd.Context(), cmd.OutOrStdout(), dependencies)
		},
	}
}

func reconcileStoragePresentations(
	ctx context.Context,
	output io.Writer,
	dependencies reconcileStorageDependencies,
) error {
	loadConfig := dependencies.loadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	openDB := dependencies.openDB
	if openDB == nil {
		openDB = database.OpenContext
	}
	now := dependencies.now
	if now == nil {
		now = time.Now
	}
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load configured application: %w", err)
	}
	db, err := openDB(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("open configured database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("access configured database: %w", err)
	}
	defer sqlDB.Close()

	registryFactory := dependencies.registry
	if registryFactory == nil {
		registryFactory = func(ctx context.Context, cfg config.Config) (reconcileStorageResolver, error) {
			recorder, recorderErr := observability.NewRecorder(nil)
			if recorderErr != nil {
				return nil, recorderErr
			}
			policy, policyErr := frameworkstorage.NewFilePolicy(
				cfg.FileUploadRuntime().MaxUploadBytes,
			)
			if policyErr != nil {
				return nil, policyErr
			}
			return platformstorage.NewRegistry(ctx, cfg, recorder, policy)
		}
	}
	registry, err := registryFactory(ctx, cfg)
	if err != nil {
		return fmt.Errorf("configure storage registry: %w", err)
	}

	var files []domain.FileObject
	if err := db.WithContext(ctx).
		Where("provider = ? AND status = ?", "oss", "ready").
		Order("id ASC").
		Find(&files).Error; err != nil {
		return fmt.Errorf("list ready OSS files: %w", err)
	}
	writes, err := uow.New(db, auditlog.Recorder{})
	if err != nil {
		return err
	}
	changed := 0
	current := 0
	for _, file := range files {
		store, err := reconcileStoreForFile(registry, file)
		if err != nil {
			return fmt.Errorf("resolve storage for file %s: %w", file.ID, err)
		}
		finalizer, ok := platformstorage.AsVerifiedPresentationFinalizer(store)
		if !ok {
			return fmt.Errorf("storage profile for file %s cannot finalize OSS presentation", file.ID)
		}
		info, err := store.Stat(ctx, file.ObjectKey)
		if err != nil {
			return fmt.Errorf("read storage metadata for file %s: %w", file.ID, err)
		}
		desiredContentType := reconciledContentType(file.ContentType)
		providerCurrent := info.ContentType == desiredContentType &&
			strings.TrimSpace(info.ContentDisposition) == ""
		if providerCurrent && info.ETag == file.ETag {
			current++
			continue
		}
		beforeContentType := info.ContentType
		if !providerCurrent {
			info, err = finalizer.FinalizeVerifiedPresentation(ctx, frameworkstorage.VerifiedPresentationRequest{
				Key: file.ObjectKey, ContentType: desiredContentType, ExpectedETag: info.ETag,
			})
			if err != nil {
				return fmt.Errorf("finalize storage metadata for file %s: %w", file.ID, err)
			}
			if info.Size != file.Size || info.Key != file.ObjectKey {
				return fmt.Errorf("finalized storage metadata for file %s does not match the database record", file.ID)
			}
		}
		err = writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
			result := tx.Model(&domain.FileObject{}).
				Where("id = ? AND status = ? AND etag = ?", file.ID, "ready", file.ETag).
				Updates(map[string]any{"etag": info.ETag, "updated_at": now().UTC()})
			if result.Error != nil {
				return frameworkaudit.Event{}, result.Error
			}
			if result.RowsAffected != 1 {
				return frameworkaudit.Event{}, errors.New("file changed during presentation reconciliation")
			}
			return frameworkaudit.Event{
				ActorKind: frameworkaudit.ActorSystem,
				Action:    "files:reconcile-presentation", Resource: "file", ResourceID: file.ID,
				Result: frameworkaudit.ResultSuccess, Source: frameworkaudit.SourceCLI,
				Summary: "Reconciled verified storage presentation",
				Before:  frameworkaudit.SanitizedFields{"providerContentType": beforeContentType},
				After:   frameworkaudit.SanitizedFields{"providerContentType": desiredContentType},
			}, nil
		})
		if err != nil {
			return fmt.Errorf("commit presentation reconciliation for file %s: %w", file.ID, err)
		}
		changed++
	}
	_, err = fmt.Fprintf(output, "Reconciled %d OSS file presentations; %d already current.\n", changed, current)
	return err
}

func reconcileStoreForFile(
	registry reconcileStorageResolver,
	file domain.FileObject,
) (platformstorage.Storage, error) {
	if file.StorageProfileID != nil && strings.TrimSpace(*file.StorageProfileID) != "" {
		return registry.Resolve(*file.StorageProfileID)
	}
	_, store, err := registry.ResolveLegacy(file.Provider, file.Bucket)
	return store, err
}

func reconciledContentType(verified string) string {
	switch strings.ToLower(strings.TrimSpace(verified)) {
	case frameworkstorage.MIMEJPEG, frameworkstorage.MIMEPNG,
		frameworkstorage.MIMEWebP, frameworkstorage.MIMEGIF,
		frameworkstorage.MIMEPDF:
		return strings.ToLower(strings.TrimSpace(verified))
	default:
		return frameworkstorage.StoredContentType
	}
}
