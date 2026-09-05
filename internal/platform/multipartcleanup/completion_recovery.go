package multipartcleanup

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	frameworkaudit "github.com/xgtian-root/aginex/framework/audit"
	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
	"github.com/xgtian-root/aginex/internal/domain"
	"gorm.io/gorm"
)

const (
	// CompletionRecoveryGrace exceeds the longest HTTP transfer deadline. A
	// scanner therefore cannot take work from an API request that is still
	// allowed to complete or verify the final object.
	CompletionRecoveryGrace = 75 * time.Minute
	completionRecoveryLimit = 60 * time.Minute
	recoveryPartSize        = int64(32 << 20)
	recoveryMaxParts        = 32
)

var (
	ErrRecoveryUnavailable = errors.New("multipart recovery: provider operation unavailable")
	errManifestMismatch    = errors.New("multipart recovery: persisted manifest does not match provider inventory")
)

// Recover repairs a completing or verifying session that has not changed for
// CompletionRecoveryGrace. It never aborts provider state. A conditional
// updated_at write is the cross-process lease, and every lease or state write
// is committed with a sanitized system audit event.
func (handler *Handler) Recover(ctx context.Context, sessionID string) error {
	if handler == nil {
		return errors.New("multipart cleanup handler is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return ErrUnsafeState
	}
	if ctx == nil {
		ctx = context.Background()
	}
	recoveryContext, cancel := context.WithTimeout(ctx, completionRecoveryLimit)
	defer cancel()

	file, session, err := handler.acquireRecoveryLease(recoveryContext, sessionID)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errAlreadyDone) || errors.Is(err, errStateChanged) {
		return nil
	}
	if err != nil {
		return err
	}

	switch session.Status {
	case domain.FileUploadSessionStatusCompleting:
		file, session, err = handler.recoverCompleting(recoveryContext, file, session)
		if errors.Is(err, errManifestMismatch) || errors.Is(err, errAlreadyDone) || errors.Is(err, errStateChanged) {
			return nil
		}
		if err != nil {
			return err
		}
		if session.Status != domain.FileUploadSessionStatusVerifying {
			return nil
		}
	case domain.FileUploadSessionStatusVerifying:
	default:
		return nil
	}

	err = handler.recoverVerifying(recoveryContext, file, session)
	if errors.Is(err, errAlreadyDone) || errors.Is(err, errStateChanged) {
		return nil
	}
	return err
}

func (handler *Handler) acquireRecoveryLease(
	ctx context.Context,
	sessionID string,
) (domain.FileObject, domain.FileUploadSession, error) {
	now := handler.clock().UTC()
	cutoff := now.Add(-CompletionRecoveryGrace)
	var leasedFile domain.FileObject
	var leasedSession domain.FileUploadSession
	err := handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		file, session, err := loadRecoverySession(tx, sessionID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if file.Status == "ready" || session.Status == domain.FileUploadSessionStatusCompleted {
			return frameworkaudit.Event{}, errAlreadyDone
		}
		if file.Status != "pending" ||
			(session.Status != domain.FileUploadSessionStatusCompleting && session.Status != domain.FileUploadSessionStatusVerifying) {
			return frameworkaudit.Event{}, ErrUnsafeState
		}
		if session.UpdatedAt.After(cutoff) {
			return frameworkaudit.Event{}, ErrUnsafeState
		}
		before := recoveryAuditFields(file, session, 0, "lease")
		result := tx.Model(&domain.FileUploadSession{}).
			Where("id = ? AND status = ? AND updated_at <= ?", session.ID, session.Status, cutoff).
			UpdateColumns(map[string]any{"updated_at": now})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errStateChanged
		}
		session.UpdatedAt = now
		leasedFile = file
		leasedSession = session
		return handler.recoveryAuditEvent(
			ctx,
			session,
			"files:recover-resumable-start",
			"Claimed abandoned resumable upload recovery",
			before,
			recoveryAuditFields(file, session, 0, "lease"),
		), nil
	})
	if err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}

	// Reload the database-normalized timestamp (notably DATETIME(6) on MySQL)
	// so later CAS operations compare the exact persisted lease value.
	file, session, err := handler.load(ctx, sessionID)
	if err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}
	if session.Status != leasedSession.Status || file.ID != leasedFile.ID {
		return domain.FileObject{}, domain.FileUploadSession{}, errStateChanged
	}
	return file, session, nil
}

func (handler *Handler) recoverCompleting(
	ctx context.Context,
	file domain.FileObject,
	session domain.FileUploadSession,
) (domain.FileObject, domain.FileUploadSession, error) {
	store, multipartStore, upload, err := handler.recoveryStores(file, session)
	if err != nil {
		return file, session, err
	}
	persisted, err := handler.loadRecoveryParts(ctx, session.ID)
	if err != nil {
		return file, session, err
	}
	manifest, manifestComplete, err := recoveryManifest(file, session, persisted)
	if err != nil {
		return file, session, err
	}

	// CompleteMultipart may already have succeeded before the process lost its
	// database write. Stat is the authoritative recovery check in that case.
	if info, statErr := store.Stat(ctx, file.ObjectKey); statErr == nil {
		if info.Size != file.Size {
			return file, session, ErrObjectChanged
		}
		return handler.markRecoveryVerifying(ctx, file, session)
	}

	inventory, listErr := multipartStore.ListUploadedParts(ctx, upload)
	if listErr != nil {
		return file, session, recoveryProviderError(ctx)
	}
	matching, stale := recoveryInventoryMatches(file, session, persisted, inventory)
	if !manifestComplete || !matching {
		if err := handler.reopenRecoveryAfterMismatch(ctx, file, session, len(persisted), stale); err != nil {
			return file, session, err
		}
		return file, session, errManifestMismatch
	}

	info, completeErr := multipartStore.CompleteMultipart(ctx, frameworkstorage.MultipartCompleteRequest{
		Upload: upload,
		Parts:  manifest,
	})
	if completeErr == nil && info.Size == file.Size {
		return handler.markRecoveryVerifying(ctx, file, session)
	}
	// Provider completion responses are not always conclusive. Stat the final
	// object before deciding whether the persisted state still needs recovery.
	if finalInfo, statErr := store.Stat(ctx, file.ObjectKey); statErr == nil {
		if finalInfo.Size != file.Size {
			return file, session, ErrObjectChanged
		}
		return handler.markRecoveryVerifying(ctx, file, session)
	}
	if completeErr != nil {
		// A part can be replaced between the preflight inventory and the
		// provider adapter's own completion inventory check. Reconcile only
		// when List still succeeds; NoSuchUpload plus failed Stat remains in
		// completing for a later conclusive retry.
		latest, latestErr := multipartStore.ListUploadedParts(ctx, upload)
		if latestErr == nil {
			latestMatches, latestStale := recoveryInventoryMatches(file, session, persisted, latest)
			if !latestMatches {
				if err := handler.reopenRecoveryAfterMismatch(ctx, file, session, len(persisted), latestStale); err != nil {
					return file, session, err
				}
				return file, session, errManifestMismatch
			}
		}
		return file, session, recoveryProviderError(ctx)
	}
	return file, session, ErrObjectChanged
}

func (handler *Handler) recoverVerifying(
	ctx context.Context,
	file domain.FileObject,
	session domain.FileUploadSession,
) error {
	store, _, _, err := handler.recoveryStores(file, session)
	if err != nil {
		return err
	}
	info, err := store.Stat(ctx, file.ObjectKey)
	if err != nil {
		return recoveryProviderError(ctx)
	}
	if info.Size != file.Size {
		return ErrObjectChanged
	}
	reader, err := store.Open(ctx, file.ObjectKey)
	if err != nil {
		return recoveryProviderError(ctx)
	}
	policy, err := frameworkstorage.NewFilePolicy(file.Size)
	if err != nil {
		_ = reader.Close()
		return err
	}
	verifier, err := frameworkstorage.NewFileVerifier(policy)
	if err != nil {
		_ = reader.Close()
		return err
	}
	verified, verifyErr := verifier.Verify(ctx, reader, file.Size)
	closeErr := reader.Close()
	if verifyErr != nil || closeErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(verifyErr, frameworkstorage.ErrEmptyContent) ||
			errors.Is(verifyErr, frameworkstorage.ErrFileSizeMismatch) ||
			errors.Is(verifyErr, frameworkstorage.ErrContentTooLarge) {
			return errors.Join(verifyErr, closeErr)
		}
		return ErrRecoveryUnavailable
	}
	return handler.finishRecoveryVerification(ctx, file, session, info, verified)
}

func (handler *Handler) markRecoveryVerifying(
	ctx context.Context,
	file domain.FileObject,
	session domain.FileUploadSession,
) (domain.FileObject, domain.FileUploadSession, error) {
	now := handler.clock().UTC()
	err := handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		currentFile, current, err := loadRecoverySession(tx, session.ID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if current.Status == domain.FileUploadSessionStatusCompleted && currentFile.Status == "ready" {
			return frameworkaudit.Event{}, errAlreadyDone
		}
		if currentFile.ID != file.ID || currentFile.ObjectKey != file.ObjectKey || currentFile.Status != "pending" {
			return frameworkaudit.Event{}, errStateChanged
		}
		ownsCompletingLease := current.Status == domain.FileUploadSessionStatusCompleting &&
			current.UpdatedAt.Equal(session.UpdatedAt)
		// An HTTP completion retry can reconcile completing -> active after its
		// inventory read while this recovery attempt conclusively publishes or
		// Stats the final object. Full stream verification is safer than leaving
		// that final object stranded behind an active session.
		finalObjectWonActiveReset := current.Status == domain.FileUploadSessionStatusActive
		if !ownsCompletingLease && !finalObjectWonActiveReset {
			return frameworkaudit.Event{}, errStateChanged
		}
		before := recoveryAuditFields(currentFile, current, 0, "merge")
		result := tx.Model(&domain.FileUploadSession{}).
			Where("id = ? AND status = ? AND updated_at = ?", current.ID, current.Status, current.UpdatedAt).
			UpdateColumns(map[string]any{"status": domain.FileUploadSessionStatusVerifying, "updated_at": now})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errStateChanged
		}
		current.Status = domain.FileUploadSessionStatusVerifying
		current.UpdatedAt = now
		return handler.recoveryAuditEvent(
			ctx,
			current,
			"files:recover-resumable-verify",
			"Recovered merged upload for final verification",
			before,
			recoveryAuditFields(currentFile, current, 0, "verify"),
		), nil
	})
	if err != nil {
		return file, session, err
	}
	return handler.load(ctx, session.ID)
}

func (handler *Handler) reopenRecoveryAfterMismatch(
	ctx context.Context,
	file domain.FileObject,
	session domain.FileUploadSession,
	persistedCount int,
	stale []int,
) error {
	now := handler.clock().UTC()
	return handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		currentFile, current, err := loadRecoverySession(tx, session.ID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if currentFile.ID != file.ID || currentFile.ObjectKey != file.ObjectKey ||
			currentFile.Status != "pending" || current.Status != domain.FileUploadSessionStatusCompleting ||
			!current.UpdatedAt.Equal(session.UpdatedAt) {
			return frameworkaudit.Event{}, errStateChanged
		}
		before := recoveryAuditFields(currentFile, current, persistedCount, "inventory-mismatch")
		if len(stale) > 0 {
			if err := tx.Where("session_id = ? AND part_number IN ?", current.ID, stale).
				Delete(&domain.FileUploadPart{}).Error; err != nil {
				return frameworkaudit.Event{}, err
			}
		}
		result := tx.Model(&domain.FileUploadSession{}).
			Where("id = ? AND status = ? AND updated_at = ?", current.ID, current.Status, current.UpdatedAt).
			UpdateColumns(map[string]any{"status": domain.FileUploadSessionStatusActive, "updated_at": now})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errStateChanged
		}
		current.Status = domain.FileUploadSessionStatusActive
		current.UpdatedAt = now
		return handler.recoveryAuditEvent(
			ctx,
			current,
			"files:recover-resumable-reconcile",
			"Reopened resumable upload after provider inventory changed",
			before,
			recoveryAuditFields(currentFile, current, persistedCount-len(stale), "reupload"),
		), nil
	})
}

func (handler *Handler) finishRecoveryVerification(
	ctx context.Context,
	file domain.FileObject,
	session domain.FileUploadSession,
	info frameworkstorage.ObjectInfo,
	verified frameworkstorage.VerifiedFile,
) error {
	now := handler.clock().UTC()
	return handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		currentFile, current, err := loadRecoverySession(tx, session.ID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if current.Status == domain.FileUploadSessionStatusCompleted && currentFile.Status == "ready" {
			return frameworkaudit.Event{}, errAlreadyDone
		}
		if currentFile.ID != file.ID || currentFile.ObjectKey != file.ObjectKey ||
			currentFile.Status != "pending" || current.Status != domain.FileUploadSessionStatusVerifying ||
			!current.UpdatedAt.Equal(session.UpdatedAt) {
			return frameworkaudit.Event{}, errStateChanged
		}
		var completedParts int64
		if err := tx.Model(&domain.FileUploadPart{}).Where("session_id = ?", current.ID).Count(&completedParts).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		before := recoveryAuditFields(currentFile, current, int(completedParts), "verify")
		result := tx.Model(&domain.FileUploadSession{}).
			Where("id = ? AND status = ? AND updated_at = ?", current.ID, current.Status, current.UpdatedAt).
			UpdateColumns(map[string]any{"status": domain.FileUploadSessionStatusCompleted, "updated_at": now})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errStateChanged
		}
		result = tx.Model(&domain.FileObject{}).
			Where("id = ? AND status = ? AND object_key = ?", currentFile.ID, "pending", currentFile.ObjectKey).
			UpdateColumns(map[string]any{
				"status":       "ready",
				"etag":         info.ETag,
				"content_type": verified.MIMEType,
				"size":         verified.Size,
				"sha256":       verified.SHA256,
				"width":        verified.Width,
				"height":       verified.Height,
				"updated_at":   now,
			})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errStateChanged
		}
		current.Status = domain.FileUploadSessionStatusCompleted
		current.UpdatedAt = now
		currentFile.Status = "ready"
		currentFile.ContentType = verified.MIMEType
		currentFile.Size = verified.Size
		currentFile.SHA256 = verified.SHA256
		currentFile.Width = verified.Width
		currentFile.Height = verified.Height
		currentFile.UpdatedAt = now
		return handler.recoveryAuditEvent(
			ctx,
			current,
			"files:recover-resumable-complete",
			"Recovered and verified resumable upload",
			before,
			recoveryAuditFields(currentFile, current, int(completedParts), "complete"),
		), nil
	})
}

func (handler *Handler) recoveryStores(
	file domain.FileObject,
	session domain.FileUploadSession,
) (frameworkstorage.ObjectStore, frameworkstorage.MultipartObjectStore, frameworkstorage.MultipartUpload, error) {
	multipartStore, upload, err := handler.multipartStore(file, session)
	if err != nil {
		return nil, nil, frameworkstorage.MultipartUpload{}, err
	}
	store, err := handler.registry.Resolve(*file.StorageProfileID)
	if err != nil {
		return nil, nil, frameworkstorage.MultipartUpload{}, err
	}
	return store, multipartStore, upload, nil
}

func (handler *Handler) loadRecoveryParts(ctx context.Context, sessionID string) ([]domain.FileUploadPart, error) {
	var persisted []domain.FileUploadPart
	if err := handler.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("part_number ASC").
		Find(&persisted).Error; err != nil {
		return nil, err
	}
	return persisted, nil
}

func recoveryManifest(
	file domain.FileObject,
	session domain.FileUploadSession,
	persisted []domain.FileUploadPart,
) ([]frameworkstorage.UploadedPart, bool, error) {
	if err := validateRecoveryShape(file, session); err != nil {
		return nil, false, err
	}
	manifest := make([]frameworkstorage.UploadedPart, 0, len(persisted))
	complete := len(persisted) == session.PartCount
	for index, part := range persisted {
		expected, err := expectedRecoveryPartSize(file.Size, session, part.PartNumber)
		if err != nil || part.Size != expected || strings.TrimSpace(part.ETag) == "" {
			complete = false
			continue
		}
		if index >= session.PartCount || part.PartNumber != index+1 {
			complete = false
		}
		manifest = append(manifest, frameworkstorage.UploadedPart{
			PartNumber: int32(part.PartNumber),
			Size:       part.Size,
			ETag:       part.ETag,
		})
	}
	if len(manifest) != session.PartCount {
		complete = false
	}
	return manifest, complete, nil
}

func recoveryInventoryMatches(
	file domain.FileObject,
	session domain.FileUploadSession,
	persisted []domain.FileUploadPart,
	inventory []frameworkstorage.UploadedPart,
) (bool, []int) {
	provider := make(map[int32]frameworkstorage.UploadedPart, len(inventory))
	providerValid := true
	for _, part := range inventory {
		if part.PartNumber < 1 || int(part.PartNumber) > session.PartCount || part.Size < 1 || strings.TrimSpace(part.ETag) == "" {
			providerValid = false
			continue
		}
		if _, duplicate := provider[part.PartNumber]; duplicate {
			providerValid = false
		}
		provider[part.PartNumber] = part
	}
	stale := make([]int, 0)
	for _, part := range persisted {
		expectedSize, expectedErr := expectedRecoveryPartSize(file.Size, session, part.PartNumber)
		actual, ok := provider[int32(part.PartNumber)]
		if expectedErr != nil || part.Size != expectedSize || strings.TrimSpace(part.ETag) == "" ||
			!ok || actual.Size != part.Size || normalizeRecoveryETag(actual.ETag) != normalizeRecoveryETag(part.ETag) {
			stale = append(stale, part.PartNumber)
		}
	}
	sort.Ints(stale)
	return providerValid && len(stale) == 0 && len(persisted) == session.PartCount && len(inventory) == session.PartCount, stale
}

func validateRecoveryShape(file domain.FileObject, session domain.FileUploadSession) error {
	if file.Size <= recoveryPartSize || file.Size > frameworkstorage.AbsoluteMaxFileBytes ||
		session.PartSize != recoveryPartSize || session.PartCount < 2 || session.PartCount > recoveryMaxParts {
		return ErrObjectChanged
	}
	wantParts := int((file.Size + recoveryPartSize - 1) / recoveryPartSize)
	if wantParts != session.PartCount {
		return ErrObjectChanged
	}
	return nil
}

func expectedRecoveryPartSize(fileSize int64, session domain.FileUploadSession, partNumber int) (int64, error) {
	if err := validateRecoveryShape(domain.FileObject{Size: fileSize}, session); err != nil {
		return 0, err
	}
	if partNumber < 1 || partNumber > session.PartCount {
		return 0, ErrObjectChanged
	}
	if partNumber < session.PartCount {
		return recoveryPartSize, nil
	}
	last := fileSize - int64(partNumber-1)*recoveryPartSize
	if last < 1 || last > recoveryPartSize {
		return 0, ErrObjectChanged
	}
	return last, nil
}

func normalizeRecoveryETag(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "W/"))
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	return value
}

func loadRecoverySession(tx *gorm.DB, sessionID string) (domain.FileObject, domain.FileUploadSession, error) {
	var session domain.FileUploadSession
	if err := tx.First(&session, "id = ?", sessionID).Error; err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}
	var file domain.FileObject
	if err := tx.First(&file, "id = ?", session.FileID).Error; err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}
	return file, session, nil
}

func recoveryProviderError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrRecoveryUnavailable
}

func (handler *Handler) recoveryAuditEvent(
	ctx context.Context,
	session domain.FileUploadSession,
	action string,
	summary string,
	before frameworkaudit.SanitizedFields,
	after frameworkaudit.SanitizedFields,
) frameworkaudit.Event {
	actorID, actorKind, requestID, source := handler.auditActor(ctx)
	return frameworkaudit.Event{
		ActorID: &actorID, ActorKind: actorKind,
		Action: action, Resource: "file-upload-session", ResourceID: session.ID,
		Result: frameworkaudit.ResultSuccess, RequestID: requestID, Source: source,
		Summary: summary, Before: before, After: after,
	}
}

func recoveryAuditFields(
	file domain.FileObject,
	session domain.FileUploadSession,
	completedParts int,
	phase string,
) frameworkaudit.SanitizedFields {
	return frameworkaudit.SanitizedFields{
		"id": session.ID, "fileId": file.ID,
		"status": session.Status, "fileStatus": file.Status,
		"partSize": session.PartSize, "partCount": session.PartCount,
		"completedParts": completedParts, "phase": phase,
	}
}
