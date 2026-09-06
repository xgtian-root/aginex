package app

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/server/framework/audit"
	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	"github.com/xgtian-root/aginex/server/framework/jobs"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/framework/services"
	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/filecleanup"
	"github.com/xgtian-root/aginex/server/internal/platform/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	filesResource       = "files"
	fileTransferTimeout = 60 * time.Minute
	// A request may begin immediately before its upload authorization expires,
	// consume the full transfer window, and then consume one full verification
	// window. Cleanup starts only after both bounded windows plus safety margin.
	pendingUploadCleanupGrace = 2*fileTransferTimeout + 5*time.Minute
)

var filesAuthorizer = newFilesAuthorizer()

var errUploadIntentChanged = errors.New("upload intent changed during confirmation")

var (
	errFileCleanupScheduled = errors.New("file cleanup is already scheduled")
	errFileAlreadyDeleted   = errors.New("file is already deleted")
	errFileUploadInProgress = errors.New("file has a non-terminal upload session")
)

type fileCleanupCause uint8

const (
	fileCleanupExplicitDelete fileCleanupCause = iota + 1
	fileCleanupInvalidUpload
	fileCleanupPendingExpiry
)

func (a *App) createUploadIntent(c *gin.Context) {
	input, ok := validatedRequestDTO[UploadIntentRequest](c)
	if !ok {
		return
	}
	principal := currentPrincipal(c)
	if !authorizeFile(c, "files:create", domain.FileObject{
		ID: "upload-intent", OwnerID: principal.User.ID,
	}) {
		return
	}
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(input.ContentType))
	if err != nil || contentType == "" || strings.ContainsAny(contentType, "*\r\n") || strings.Count(contentType, "/") != 1 {
		httpx.WriteProblem(
			c,
			http.StatusUnsupportedMediaType,
			"INVALID_CONTENT_TYPE",
			"Content type is invalid",
			"Use a syntactically valid MIME type, or application/octet-stream when the browser does not provide one.",
		)
		return
	}
	input.ContentType = strings.ToLower(contentType)
	runtimePolicy := a.cfg.FileUploadRuntime()
	if input.Size > runtimePolicy.MaxUploadBytes {
		httpx.WriteProblem(
			c,
			http.StatusRequestEntityTooLarge,
			"FILE_TOO_LARGE",
			"File is too large",
			"Choose a file within the currently effective upload limit.",
		)
		return
	}
	strategy := input.Strategy
	if strategy == "" {
		strategy = "single"
	}
	if strategy == "resumable" {
		a.createResumableUploadIntent(c, input)
		return
	}
	a.createSingleUploadIntent(c, input)
}

func (a *App) createSingleUploadIntent(c *gin.Context, input UploadIntentRequest) {
	now := time.Now().UTC()
	key := "uploads/" + now.Format("2006/01") + "/" + uuid.NewString()
	principal := currentPrincipal(c)
	activeProfile, ok := a.storageRegistry.ActiveProfile()
	if !ok {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The active storage profile is unavailable.")
		return
	}
	profileID := activeProfile.ID
	activeStorage := activeProfile.StorageConfig()
	file := domain.FileObject{
		ID: uuid.NewString(), StorageProfileID: &profileID,
		Provider: activeStorage.Driver, Bucket: activeStorage.Bucket,
		ObjectKey: key, OriginalName: safeOriginalFilename(input.Filename), ContentType: input.ContentType,
		Size: input.Size, OwnerID: principal.User.ID, Visibility: input.Visibility, Status: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	expires := singleUploadCredentialTTL(input.Size)
	signed, err := a.store.CreateUpload(c.Request.Context(), storage.UploadRequest{
		Key: key, ContentType: frameworkstorage.StoredContentType, Size: input.Size, Expires: expires,
	})
	if err != nil {
		logRequestFailure(c, "storage_create_upload", err)
		writeProblem(c, http.StatusBadRequest, "Upload could not be prepared", "The storage provider could not prepare the upload.")
		return
	}
	signed, err = a.protectLocalSingleUpload(a.store, file, signed)
	if err != nil {
		logRequestFailure(c, "sign_local_upload", err)
		writeProblem(c, http.StatusInternalServerError, "Upload could not be prepared", "The local upload authorization could not be created.")
		return
	}
	uploadExpiresAt := signed.ExpiresAt.UTC()
	file.UploadExpiresAt = &uploadExpiresAt
	if err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := tx.Create(&file).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if a.jobs != nil {
			if err := a.enqueueFileCleanup(
				c,
				tx,
				file,
				fileCleanupPendingExpiry,
				uploadExpiresAt.Add(pendingUploadCleanupGrace),
			); err != nil {
				return frameworkaudit.Event{}, err
			}
		}
		upload := signedRequestResponse(signed)
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusCreated,
			UploadIntentResponse{
				Strategy: "single",
				File:     a.fileResponse(file),
				Upload:   &upload,
			},
			nil,
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"files:create-intent",
			"file",
			file.ID,
			"Prepared "+file.OriginalName,
			nil,
			fileAuditFields(file),
		), nil
	}); err != nil {
		logRequestFailure(c, "create_upload_intent", err)
		writeProblem(c, http.StatusInternalServerError, "Upload could not be prepared", "The upload intent could not be committed.")
		return
	}
	upload := signedRequestResponse(signed)
	c.JSON(http.StatusCreated, UploadIntentResponse{
		Strategy: "single",
		File:     a.fileResponse(file),
		Upload:   &upload,
	})
}

func (a *App) localUpload(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("key"), "/")
	file, found := a.findAuthorizedFile(
		c,
		"files:create",
		"object_key = ? AND status = ?",
		key,
		"pending",
	)
	if !found {
		return
	}
	fileStore, err := a.storeForFile(file)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The file's storage profile is unavailable.")
		return
	}
	local, ok := storage.AsLocal(fileStore)
	if !ok {
		writeProblem(c, http.StatusNotFound, "Local upload is unavailable", "The active storage provider uses direct cloud uploads.")
		return
	}
	if !a.verifyLocalSingleUpload(c, file) {
		return
	}
	contentType, _, parseErr := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if parseErr != nil || contentType != frameworkstorage.StoredContentType {
		writeProblem(c, http.StatusUnsupportedMediaType, "Content type does not match", "Use the content type declared by the upload intent.")
		return
	}
	if c.Request.ContentLength != file.Size {
		httpx.WriteProblem(
			c,
			http.StatusBadRequest,
			"FILE_SIZE_MISMATCH",
			"File size does not match",
			"Upload the exact byte length declared by the upload intent.",
		)
		return
	}
	cancelTransfer := setFileTransferDeadline(c)
	defer cancelTransfer()
	body := http.MaxBytesReader(c.Writer, c.Request.Body, file.Size+1)
	if _, err := local.Put(c.Request.Context(), key, body, file.Size); err != nil {
		logRequestFailure(c, "local_upload_store", err)
		if errors.Is(err, frameworkstorage.ErrFileSizeMismatch) ||
			errors.Is(err, frameworkstorage.ErrContentTooLarge) {
			httpx.WriteProblem(c, http.StatusBadRequest, "FILE_SIZE_MISMATCH", "File size does not match", "Upload the exact byte length declared by the upload intent.")
			return
		}
		writeProblem(c, http.StatusBadRequest, "Upload could not be stored", "The local storage provider could not persist the upload.")
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) confirmUpload(c *gin.Context) {
	cancelTransfer := setFileTransferDeadline(c)
	defer cancelTransfer()
	file, found := a.findAuthorizedFile(
		c,
		"files:create",
		"id = ?",
		c.Param("id"),
	)
	if !found {
		return
	}
	if file.Status != "pending" {
		writeProblem(c, http.StatusConflict, "Upload intent changed", "Create a new upload intent and upload the object again.")
		return
	}
	if file.UploadExpiresAt == nil || time.Now().UTC().After(file.UploadExpiresAt.UTC().Add(fileTransferTimeout)) {
		httpx.WriteProblem(
			c,
			http.StatusConflict,
			"UPLOAD_INTENT_EXPIRED",
			"Upload intent has expired",
			"Create a new upload intent and upload the file again.",
		)
		return
	}
	principal := currentPrincipal(c)
	verifiedIntent := file
	fileStore, err := a.storeForFile(verifiedIntent)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The file's storage profile is unavailable.")
		return
	}
	info, err := fileStore.Stat(c.Request.Context(), verifiedIntent.ObjectKey)
	if err != nil {
		logRequestFailure(c, "storage_stat_upload", err)
		writeProblem(c, http.StatusConflict, "Uploaded object was not found", "Finish uploading the object, then confirm it again.")
		return
	}
	if info.Size != verifiedIntent.Size {
		writeProblem(c, http.StatusConflict, "Uploaded object does not match", "The stored size differs from the upload intent.")
		return
	}
	object, err := fileStore.Open(c.Request.Context(), verifiedIntent.ObjectKey)
	if err != nil {
		logRequestFailure(c, "storage_open_upload", err)
		writeProblem(c, http.StatusConflict, "Uploaded object could not be read", "Finish uploading the object, then confirm it again.")
		return
	}
	verifier, verifierErr := a.fileVerifierForIntent(verifiedIntent.Size)
	if verifierErr != nil {
		_ = object.Close()
		logRequestFailure(c, "configure_file_verifier", verifierErr)
		writeProblem(c, http.StatusInternalServerError, "Uploaded object could not be verified", "The upload policy snapshot could not be restored.")
		return
	}
	verified, verificationErr := verifier.Verify(
		c.Request.Context(),
		object,
		verifiedIntent.Size,
	)
	closeErr := object.Close()
	if verificationErr == nil && closeErr != nil {
		logRequestFailure(c, "storage_close_upload", closeErr)
		writeProblem(c, http.StatusInternalServerError, "Uploaded object could not be verified", "The object stream could not be closed safely.")
		return
	}
	if verificationErr != nil {
		if writeFileVerificationInterrupted(c, verificationErr) {
			return
		}
		if !isUnsafeFileError(verificationErr) {
			logRequestFailure(
				c,
				"verify_uploaded_file",
				verificationErr,
			)
			writeProblem(c, http.StatusInternalServerError, "Uploaded object could not be verified", "The object content could not be checked.")
			return
		}
		if err := a.markInvalidUpload(c, verifiedIntent); err != nil {
			logRequestFailure(c, "quarantine_invalid_upload", err)
			writeProblem(c, http.StatusInternalServerError, "Invalid upload could not be quarantined", "The file state could not be committed.")
			return
		}
		writeFileVerificationProblem(c, verificationErr)
		return
	}
	info, err = finalizeVerifiedPresentation(
		c.Request.Context(), fileStore, info, verified.MIMEType,
	)
	if err != nil {
		logRequestFailure(c, "storage_finalize_verified_presentation", err)
		writeProblem(c, http.StatusServiceUnavailable, "Uploaded object could not be finalized", "Retry confirmation after the storage provider becomes available.")
		return
	}
	err = a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&file, "id = ?", verifiedIntent.ID).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", file); err != nil {
			return frameworkaudit.Event{}, err
		}
		if file.Status != "pending" ||
			file.ObjectKey != verifiedIntent.ObjectKey ||
			file.Size != verifiedIntent.Size ||
			file.ContentType != verifiedIntent.ContentType {
			return frameworkaudit.Event{}, errUploadIntentChanged
		}
		before := fileAuditFields(file)
		file.Status = "ready"
		file.ETag = info.ETag
		file.ContentType = verified.MIMEType
		file.Size = verified.Size
		file.SHA256 = verified.SHA256
		file.Width = verified.Width
		file.Height = verified.Height
		file.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&file).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusOK,
			a.fileResponse(file),
			nil,
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"files:confirm",
			"file",
			file.ID,
			"Confirmed "+file.OriginalName,
			before,
			fileAuditFields(file),
		), nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFoundOrInternal(c, "File", err)
		return
	}
	if isFileAuthorizationError(err) {
		writeFileAuthorizationProblem(c, err)
		return
	}
	if errors.Is(err, errUploadIntentChanged) {
		writeProblem(c, http.StatusConflict, "Upload intent changed", "Create a new upload intent and upload the object again.")
		return
	}
	if err != nil {
		logRequestFailure(c, "confirm_upload", err)
		writeProblem(c, http.StatusInternalServerError, "File could not be confirmed", "The file confirmation could not be committed.")
		return
	}
	c.JSON(http.StatusOK, a.fileResponse(file))
}

func (a *App) listFiles(c *gin.Context) {
	page, pageSize := pagination(c)
	runtime, runtimeOK := services.RuntimeFromContext(
		c.Request.Context(),
	)
	if !runtimeOK {
		writeProblem(
			c,
			http.StatusInternalServerError,
			"Files unavailable",
			"The module query service is unavailable.",
		)
		return
	}
	query, ok := scopeFileQuery(
		c,
		"files:read",
		runtime.Database.Model(&domain.FileObject{}),
	)
	if !ok {
		return
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		logRequestFailure(c, "list_files_count", err)
		writeProblem(c, http.StatusInternalServerError, "Files unavailable", "The file list could not be loaded.")
		return
	}
	var files []domain.FileObject
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&files).Error; err != nil {
		logRequestFailure(c, "list_files_query", err)
		writeProblem(c, http.StatusInternalServerError, "Files unavailable", "The file list could not be loaded.")
		return
	}
	items := make([]FileResponse, 0, len(files))
	for _, file := range files {
		items = append(items, a.fileResponse(file))
	}
	c.JSON(http.StatusOK, Page[FileResponse]{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	})
}

func (a *App) fileURL(c *gin.Context) {
	file, found := a.findAuthorizedFile(
		c,
		"files:read",
		"id = ? AND status = ?",
		c.Param("id"),
		"ready",
	)
	if !found {
		return
	}
	fileStore, err := a.storeForFile(file)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The file's storage profile is unavailable.")
		return
	}
	disposition := frameworkstorage.ReadDispositionAttachment
	if c.Query("purpose") == "" || c.Query("purpose") == "preview" {
		if filePreviewKind(file.ContentType) != string(frameworkstorage.PreviewNone) {
			disposition = frameworkstorage.ReadDispositionInline
		}
	} else if c.Query("purpose") != "download" {
		httpx.WriteProblem(c, http.StatusBadRequest, "INVALID_FILE_PURPOSE", "File purpose is invalid", "Use preview or download.")
		return
	}
	var signed storage.SignedRequest
	if controlled, ok := storage.AsControlledRead(fileStore); ok {
		signed, err = controlled.SignControlledRead(c.Request.Context(), frameworkstorage.ControlledReadRequest{
			Key: file.ObjectKey, Expires: 5 * time.Minute,
			ContentType: file.ContentType, Disposition: disposition,
			Filename: file.OriginalName,
		})
	} else {
		signed, err = fileStore.SignRead(c.Request.Context(), file.ObjectKey, 5*time.Minute)
	}
	if err != nil {
		logRequestFailure(c, "storage_sign_read", err)
		writeProblem(c, http.StatusInternalServerError, "File URL unavailable", "A temporary file URL could not be created.")
		return
	}
	c.JSON(http.StatusOK, signedRequestResponse(signed))
}

func finalizeVerifiedPresentation(
	ctx context.Context,
	fileStore storage.Storage,
	info storage.ObjectInfo,
	verifiedContentType string,
) (storage.ObjectInfo, error) {
	finalizer, ok := storage.AsVerifiedPresentationFinalizer(fileStore)
	if !ok {
		return info, nil
	}
	contentType := frameworkstorage.StoredContentType
	if filePreviewKind(verifiedContentType) != string(frameworkstorage.PreviewNone) {
		contentType = verifiedContentType
	}
	finalized, err := finalizer.FinalizeVerifiedPresentation(
		ctx,
		frameworkstorage.VerifiedPresentationRequest{
			Key: info.Key, ContentType: contentType, ExpectedETag: info.ETag,
		},
	)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	if finalized.Key != info.Key || finalized.Size != info.Size {
		return storage.ObjectInfo{}, errors.New("finalized object does not match verified object")
	}
	return finalized, nil
}

func (a *App) localContent(c *gin.Context) {
	cancelTransfer := setFileTransferDeadline(c)
	defer cancelTransfer()
	key := strings.TrimPrefix(c.Param("key"), "/")
	metadata, found := a.findAuthorizedFile(
		c,
		"files:read",
		"object_key = ? AND status = ?",
		key,
		"ready",
	)
	if !found {
		return
	}
	fileStore, err := a.storeForFile(metadata)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The file's storage profile is unavailable.")
		return
	}
	local, ok := storage.AsLocal(fileStore)
	if !ok {
		writeProblem(c, http.StatusNotFound, "Local content is unavailable", "The active provider uses signed cloud URLs.")
		return
	}
	file, err := local.Open(c.Request.Context(), key)
	if err != nil {
		logRequestFailure(c, "local_content_open", err)
		writeProblem(c, http.StatusNotFound, "File content not found", "The stored file content is unavailable.")
		return
	}
	defer file.Close()
	disposition := "attachment"
	contentType := frameworkstorage.StoredContentType
	if (c.Query("purpose") == "" || c.Query("purpose") == "preview") &&
		filePreviewKind(metadata.ContentType) != string(frameworkstorage.PreviewNone) {
		disposition = "inline"
		contentType = metadata.ContentType
	}
	if c.Query("purpose") != "" && c.Query("purpose") != "preview" && c.Query("purpose") != "download" {
		httpx.WriteProblem(c, http.StatusBadRequest, "INVALID_FILE_PURPOSE", "File purpose is invalid", "Use preview or download.")
		return
	}
	contentDisposition, dispositionErr := storage.FormatContentDisposition(
		frameworkstorage.ReadDisposition(disposition),
		metadata.OriginalName,
	)
	if dispositionErr != nil {
		contentDisposition = "attachment"
		contentType = frameworkstorage.StoredContentType
	}
	c.Header("Content-Disposition", contentDisposition)
	c.Header("X-Content-Type-Options", "nosniff")
	if contentType == frameworkstorage.MIMEPDF {
		c.Header("Content-Security-Policy", "sandbox")
	}
	c.DataFromReader(http.StatusOK, metadata.Size, contentType, file, nil)
}

func (a *App) deleteFile(c *gin.Context) {
	file, found := a.findAuthorizedFile(
		c,
		"files:delete",
		"id = ?",
		c.Param("id"),
	)
	if !found {
		return
	}
	var incompleteSessions int64
	if err := a.db.WithContext(c.Request.Context()).Model(&domain.FileUploadSession{}).
		Where("file_id = ? AND status IN ?", file.ID, incompleteUploadSessionStatuses()).
		Count(&incompleteSessions).Error; err != nil {
		logRequestFailure(c, "check_file_upload_session_before_delete", err)
		writeProblem(c, http.StatusInternalServerError, "File deletion could not be prepared", "The file's upload state could not be checked.")
		return
	}
	if incompleteSessions > 0 {
		writeFileUploadInProgressProblem(c)
		return
	}
	if a.jobs == nil {
		httpx.WriteProblem(
			c,
			http.StatusServiceUnavailable,
			"FILE_CLEANUP_UNAVAILABLE",
			"File cleanup is unavailable",
			"Configure a durable job provider before deleting stored objects.",
		)
		return
	}
	principal := currentPrincipal(c)
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		// Multipart transitions lock session then file. Use the same order so
		// deletion cannot race completion and resurrect a deleted file.
		var uploadSession domain.FileUploadSession
		sessionErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("file_id = ? AND status IN ?", file.ID, incompleteUploadSessionStatuses()).
			First(&uploadSession).Error
		if sessionErr == nil {
			return frameworkaudit.Event{}, errFileUploadInProgress
		}
		if !errors.Is(sessionErr, gorm.ErrRecordNotFound) {
			return frameworkaudit.Event{}, sessionErr
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&file, "id = ?", file.ID).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:delete", file); err != nil {
			return frameworkaudit.Event{}, err
		}
		switch file.Status {
		case "deleted":
			return frameworkaudit.Event{}, errFileAlreadyDeleted
		case "deleting":
			return frameworkaudit.Event{}, errFileCleanupScheduled
		}
		cleanupAt := time.Time{}
		if file.Status == "pending" && file.UploadExpiresAt != nil {
			cleanupAt = file.UploadExpiresAt.UTC().Add(pendingUploadCleanupGrace)
		}
		before := fileAuditFields(file)
		file.Status = "deleting"
		file.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&file).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.enqueueFileCleanup(
			c,
			tx,
			file,
			fileCleanupExplicitDelete,
			cleanupAt,
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusAccepted,
			nil,
			nil,
		); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"files:delete-request",
			"file",
			file.ID,
			"Scheduled deletion of "+file.OriginalName,
			before,
			fileAuditFields(file),
		), nil
	})
	if errors.Is(err, errFileAlreadyDeleted) {
		c.Status(http.StatusNoContent)
		return
	}
	if errors.Is(err, errFileCleanupScheduled) {
		c.Status(http.StatusAccepted)
		return
	}
	if errors.Is(err, errFileUploadInProgress) {
		writeFileUploadInProgressProblem(c)
		return
	}
	if isFileAuthorizationError(err) {
		writeFileAuthorizationProblem(c, err)
		return
	}
	if err != nil {
		logRequestFailure(c, "schedule_file_deletion", err)
		writeProblem(c, http.StatusInternalServerError, "File deletion could not be scheduled", "The cleanup task could not be committed.")
		return
	}
	c.Status(http.StatusAccepted)
}

func writeFileUploadInProgressProblem(c *gin.Context) {
	httpx.WriteProblem(
		c,
		http.StatusConflict,
		"FILE_UPLOAD_IN_PROGRESS",
		"File upload is still in progress",
		"Cancel the resumable upload session before deleting this file.",
	)
}

func newFilesAuthorizer() *frameworkauthz.Authorizer {
	policy, err := frameworkauthz.NewOwnerColumnPolicy("owner_id")
	if err != nil {
		panic(err)
	}
	authorizer := frameworkauthz.NewAuthorizer()
	if err := authorizer.Register(filesResource, policy); err != nil {
		panic(err)
	}
	return authorizer
}

func authorizeFile(c *gin.Context, permission string, file domain.FileObject) bool {
	err := checkFileAuthorization(c, permission, file)
	if err != nil {
		writeFileAuthorizationProblem(c, err)
		return false
	}
	return true
}

func checkFileAuthorization(c *gin.Context, permission string, file domain.FileObject) error {
	if authorization, ok := module.RequestAuthorizationFromContext(
		c.Request.Context(),
	); ok && authorization.Permission() == permission {
		return authorization.Check(
			c.Request.Context(),
			frameworkauthz.ResourceRef{
				Resource: filesResource,
				ID:       file.ID,
				OwnerID:  file.OwnerID,
			},
		)
	}
	principal := currentPrincipal(c)
	return filesAuthorizer.Check(
		c.Request.Context(),
		principal.Actor(),
		permission,
		frameworkauthz.ResourceRef{
			Resource: filesResource,
			ID:       file.ID,
			OwnerID:  file.OwnerID,
		},
	)
}

func (a *App) findAuthorizedFile(
	c *gin.Context,
	permission string,
	predicate any,
	args ...any,
) (domain.FileObject, bool) {
	runtime, ok := services.RuntimeFromContext(c.Request.Context())
	if !ok {
		writeProblem(
			c,
			http.StatusInternalServerError,
			"File unavailable",
			"The module query service is unavailable.",
		)
		return domain.FileObject{}, false
	}
	query, ok := scopeFileQuery(
		c,
		permission,
		runtime.Database.Model(&domain.FileObject{}),
	)
	if !ok {
		return domain.FileObject{}, false
	}
	var file domain.FileObject
	result := query.Where(predicate, args...).Take(&file)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		writeProblem(
			c,
			http.StatusNotFound,
			"File not found",
			"The requested file does not exist.",
		)
		return domain.FileObject{}, false
	}
	if result.Error != nil {
		logRequestFailure(c, "load_authorized_file", result.Error)
		writeProblem(
			c,
			http.StatusInternalServerError,
			"File unavailable",
			"The requested file could not be loaded.",
		)
		return domain.FileObject{}, false
	}
	return file, true
}

func scopeFileQuery(
	c *gin.Context,
	permission string,
	query module.Query,
) (module.Query, bool) {
	if authorization, ok := module.RequestAuthorizationFromContext(
		c.Request.Context(),
	); ok && authorization.Permission() == permission {
		scoped, err := authorization.Scope(c.Request.Context(), query)
		if err != nil {
			writeFileAuthorizationProblem(c, err)
			return module.Query{}, false
		}
		return scoped, true
	}
	writeProblem(
		c,
		http.StatusInternalServerError,
		"Authorization unavailable",
		"The admitted file query authorization is missing.",
	)
	return module.Query{}, false
}

func writeFileAuthorizationProblem(c *gin.Context, err error) {
	if errors.Is(err, frameworkauthz.ErrUnauthenticated) {
		writeProblem(c, http.StatusUnauthorized, "Authentication required", "Sign in to continue.")
		return
	}
	writeProblem(c, http.StatusForbidden, "Permission denied", "You do not have access to this file.")
}

func isFileAuthorizationError(err error) bool {
	return errors.Is(err, frameworkauthz.ErrUnauthenticated) ||
		errors.Is(err, frameworkauthz.ErrForbidden) ||
		errors.Is(err, frameworkauthz.ErrInvalidPolicy)
}

func (a *App) fileResponse(file domain.FileObject) FileResponse {
	previewKind := string(frameworkstorage.PreviewNone)
	if file.Status == "ready" {
		previewKind = filePreviewKind(file.ContentType)
	}
	response := FileResponse{
		ID:           file.ID,
		Provider:     file.Provider,
		OriginalName: file.OriginalName,
		ContentType:  file.ContentType,
		Size:         file.Size,
		SHA256:       file.SHA256,
		Width:        file.Width,
		Height:       file.Height,
		Visibility:   file.Visibility,
		Status:       file.Status,
		CreatedAt:    file.CreatedAt,
		UpdatedAt:    file.UpdatedAt,
		PreviewKind:  previewKind,
	}
	if file.StorageProfileID != nil {
		response.StorageProfileID = *file.StorageProfileID
		if profile, ok := a.storageRegistry.Profile(*file.StorageProfileID); ok {
			response.StorageProfileName = profile.Name
			response.StorageProvider = string(profile.Provider)
		}
	}
	return response
}

func (a *App) storeForFile(file domain.FileObject) (storage.Storage, error) {
	if file.StorageProfileID != nil && strings.TrimSpace(*file.StorageProfileID) != "" {
		if active, ok := a.storageRegistry.ActiveProfile(); ok && active.ID == *file.StorageProfileID {
			return a.store, nil
		}
		return a.storageRegistry.Resolve(*file.StorageProfileID)
	}
	_, store, err := a.storageRegistry.ResolveLegacy(file.Provider, file.Bucket)
	return store, err
}

func signedRequestResponse(signed storage.SignedRequest) SignedRequestResponse {
	headers := make(map[string]string, len(signed.Headers))
	for name, value := range signed.Headers {
		headers[name] = value
	}
	return SignedRequestResponse{
		URL:       signed.URL,
		Method:    signed.Method,
		Headers:   headers,
		ExpiresAt: signed.ExpiresAt,
	}
}

func fileAuditFields(file domain.FileObject) map[string]any {
	return map[string]any{
		"id":           file.ID,
		"originalName": file.OriginalName,
		"contentType":  file.ContentType,
		"size":         file.Size,
		"sha256":       file.SHA256,
		"width":        file.Width,
		"height":       file.Height,
		"visibility":   file.Visibility,
		"status":       file.Status,
	}
}

func (a *App) markInvalidUpload(c *gin.Context, expected domain.FileObject) error {
	principal := currentPrincipal(c)
	return a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var file domain.FileObject
		if err := tx.First(&file, "id = ? AND status = ?", expected.ID, "pending").Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", file); err != nil {
			return frameworkaudit.Event{}, err
		}
		if file.ObjectKey != expected.ObjectKey {
			return frameworkaudit.Event{}, errUploadIntentChanged
		}
		before := fileAuditFields(file)
		file.Status = "invalid"
		file.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&file).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if a.jobs != nil {
			if err := a.enqueueFileCleanup(
				c,
				tx,
				file,
				fileCleanupInvalidUpload,
				time.Time{},
			); err != nil {
				return frameworkaudit.Event{}, err
			}
		}
		return successfulAuditEvent(
			c,
			&principal.User.ID,
			"files:reject",
			"file",
			file.ID,
			"Rejected invalid "+file.OriginalName,
			before,
			fileAuditFields(file),
		), nil
	})
}

func (a *App) enqueueFileCleanup(
	c *gin.Context,
	tx *gorm.DB,
	file domain.FileObject,
	cause fileCleanupCause,
	scheduledAt time.Time,
) error {
	queue, err := a.jobs.Bind(tx)
	if err != nil {
		return err
	}
	var mode filecleanup.Mode
	var idempotencySuffix string
	switch cause {
	case fileCleanupExplicitDelete:
		mode = filecleanup.ModeExplicitDelete
		idempotencySuffix = "explicit-delete"
	case fileCleanupInvalidUpload:
		mode = filecleanup.ModeExplicitDelete
		idempotencySuffix = "invalid-delete"
	case fileCleanupPendingExpiry:
		mode = filecleanup.ModePendingExpiry
		idempotencySuffix = "pending-expiry"
		if scheduledAt.IsZero() {
			return errors.New("pending upload cleanup schedule is required")
		}
	default:
		return errors.New("file cleanup cause is invalid")
	}
	version := filecleanup.PayloadVersion2
	payloadValue := any(filecleanup.PayloadV2{
		FileID: file.ID, Provider: file.Provider, ObjectKey: file.ObjectKey, Mode: mode,
	})
	if file.StorageProfileID != nil {
		version = filecleanup.PayloadVersion3
		payloadValue = filecleanup.PayloadV3{
			FileID: file.ID, ProfileID: *file.StorageProfileID,
			Provider: file.Provider, Bucket: file.Bucket, ObjectKey: file.ObjectKey, Mode: mode,
		}
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	principal := currentPrincipal(c)
	_, err = queue.Enqueue(c.Request.Context(), jobs.EnqueueRequest{
		Type:           filecleanup.JobType,
		Version:        version,
		Payload:        payload,
		IdempotencyKey: "file:" + file.ID + ":" + idempotencySuffix,
		ScheduledAt:    scheduledAt,
		MaxAttempts:    10,
		CreatedBy:      principal.Actor(),
		Trace: jobs.TraceContext{
			RequestID:   c.Writer.Header().Get("X-Request-ID"),
			TraceParent: c.Writer.Header().Get(httpx.TraceParentHeader),
		},
	})
	return err
}

func isUnsafeFileError(err error) bool {
	return errors.Is(err, frameworkstorage.ErrEmptyContent) ||
		errors.Is(err, frameworkstorage.ErrContentTooLarge) ||
		errors.Is(err, frameworkstorage.ErrFileSizeMismatch)
}

func writeFileVerificationInterrupted(c *gin.Context, err error) bool {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	writeProblem(c, http.StatusRequestTimeout, "Upload verification was interrupted", "Retry the confirmation request.")
	return true
}

func writeFileVerificationProblem(c *gin.Context, err error) {
	if errors.Is(err, frameworkstorage.ErrContentTooLarge) {
		httpx.WriteProblem(
			c,
			http.StatusRequestEntityTooLarge,
			"FILE_TOO_LARGE",
			"Uploaded file is too large",
			"Upload a file within the configured byte limit.",
		)
		return
	}
	httpx.WriteProblem(
		c,
		http.StatusUnprocessableEntity,
		"FILE_CONTENT_INVALID",
		"Uploaded file is invalid",
		"The object must be non-empty and match the exact byte length declared by the upload intent.",
	)
}

func filePreviewKind(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case frameworkstorage.MIMEJPEG,
		frameworkstorage.MIMEPNG,
		frameworkstorage.MIMEWebP,
		frameworkstorage.MIMEGIF:
		return string(frameworkstorage.PreviewImage)
	case frameworkstorage.MIMEPDF:
		return string(frameworkstorage.PreviewPDF)
	default:
		return string(frameworkstorage.PreviewNone)
	}
}

func singleUploadCredentialTTL(size int64) time.Duration {
	if size > multipartThresholdBytes {
		return 60 * time.Minute
	}
	return 10 * time.Minute
}

func setFileTransferDeadline(c *gin.Context) context.CancelFunc {
	if c == nil {
		return func() {}
	}
	deadline := time.Now().Add(fileTransferTimeout)
	requestContext, cancel := context.WithDeadline(c.Request.Context(), deadline)
	c.Request = c.Request.WithContext(requestContext)
	controller := http.NewResponseController(c.Writer)
	if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		logRequestFailure(c, "file_transfer_read_deadline", err)
	}
	if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		logRequestFailure(c, "file_transfer_write_deadline", err)
	}
	return cancel
}

func (a *App) fileVerifierForIntent(size int64) (*frameworkstorage.FileVerifier, error) {
	if size <= a.cfg.FileUploadRuntime().MaxUploadBytes {
		return a.fileVerifier, nil
	}
	policy, err := frameworkstorage.NewFilePolicy(size)
	if err != nil {
		return nil, err
	}
	return frameworkstorage.NewFileVerifier(policy)
}
