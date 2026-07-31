package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/framework/audit"
	frameworkauthz "github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/httpx"
	"github.com/xgtian-root/aginex/framework/jobs"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/framework/services"
	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/filecleanup"
	"github.com/xgtian-root/aginex/internal/platform/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	filesResource             = "files"
	pendingUploadCleanupGrace = 2 * time.Minute
)

var filesAuthorizer = newFilesAuthorizer()

var errUploadIntentChanged = errors.New("upload intent changed during confirmation")

var (
	errFileCleanupScheduled = errors.New("file cleanup is already scheduled")
	errFileAlreadyDeleted   = errors.New("file is already deleted")
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
	extension := extensionFor(input.ContentType)
	if extension == "" {
		writeProblem(c, http.StatusUnsupportedMediaType, "Image type is not allowed", "Use a JPEG, PNG, or WebP image.")
		return
	}
	now := time.Now().UTC()
	key := "uploads/" + now.Format("2006/01") + "/" + uuid.NewString() + extension
	principal := currentPrincipal(c)
	file := domain.FileObject{
		ID: uuid.NewString(), Provider: a.cfg.Storage.Driver, Bucket: a.cfg.Storage.Bucket,
		ObjectKey: key, OriginalName: path.Base(strings.ReplaceAll(input.Filename, "\\", "/")), ContentType: input.ContentType,
		Size: input.Size, OwnerID: principal.User.ID, Visibility: input.Visibility, Status: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	if !authorizeFile(c, "files:create", file) {
		return
	}
	signed, err := a.store.CreateUpload(c.Request.Context(), storage.UploadRequest{
		Key: key, ContentType: input.ContentType, Size: input.Size, Expires: 10 * time.Minute,
	})
	if err != nil {
		logRequestFailure(c, "storage_create_upload", err)
		writeProblem(c, http.StatusBadRequest, "Upload could not be prepared", "The storage provider could not prepare the upload.")
		return
	}
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
				signed.ExpiresAt.Add(pendingUploadCleanupGrace),
			); err != nil {
				return frameworkaudit.Event{}, err
			}
		}
		if err := a.completeIdempotentWrite(
			c,
			tx,
			http.StatusCreated,
			UploadIntentResponse{
				File:   fileResponse(file),
				Upload: signedRequestResponse(signed),
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
	c.JSON(http.StatusCreated, UploadIntentResponse{
		File:   fileResponse(file),
		Upload: signedRequestResponse(signed),
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
	local, ok := storage.AsLocal(a.store)
	if !ok {
		writeProblem(c, http.StatusNotFound, "Local upload is unavailable", "The active storage provider uses direct cloud uploads.")
		return
	}
	if c.GetHeader("Content-Type") != file.ContentType {
		writeProblem(c, http.StatusUnsupportedMediaType, "Content type does not match", "Use the content type declared by the upload intent.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, file.Size+1))
	if err != nil {
		logRequestFailure(c, "local_upload_read", err)
		writeProblem(c, http.StatusBadRequest, "Upload could not be read", "The upload body could not be read.")
		return
	}
	if int64(len(body)) != file.Size {
		writeProblem(c, http.StatusBadRequest, "File size does not match", "Upload the exact file declared by the upload intent.")
		return
	}
	if err := local.Put(key, body, file.ContentType); err != nil {
		logRequestFailure(c, "local_upload_store", err)
		writeProblem(c, http.StatusBadRequest, "Upload could not be stored", "The local storage provider could not persist the upload.")
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) confirmUpload(c *gin.Context) {
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
	principal := currentPrincipal(c)
	verifiedIntent := file
	info, err := a.store.Stat(c.Request.Context(), verifiedIntent.ObjectKey)
	if err != nil {
		logRequestFailure(c, "storage_stat_upload", err)
		writeProblem(c, http.StatusConflict, "Uploaded object was not found", "Finish uploading the object, then confirm it again.")
		return
	}
	if info.Size != verifiedIntent.Size {
		writeProblem(c, http.StatusConflict, "Uploaded object does not match", "The stored size differs from the upload intent.")
		return
	}
	object, err := a.store.Open(c.Request.Context(), verifiedIntent.ObjectKey)
	if err != nil {
		logRequestFailure(c, "storage_open_upload", err)
		writeProblem(c, http.StatusConflict, "Uploaded object could not be read", "Finish uploading the object, then confirm it again.")
		return
	}
	verified, verificationErr := a.imageFiles.Verify(
		c.Request.Context(),
		object,
		verifiedIntent.ContentType,
	)
	closeErr := object.Close()
	if verificationErr == nil && closeErr != nil {
		logRequestFailure(c, "storage_close_upload", closeErr)
		writeProblem(c, http.StatusInternalServerError, "Uploaded object could not be verified", "The object stream could not be closed safely.")
		return
	}
	if verificationErr != nil {
		if errors.Is(verificationErr, context.Canceled) ||
			errors.Is(verificationErr, context.DeadlineExceeded) {
			writeProblem(c, http.StatusRequestTimeout, "Upload verification was interrupted", "Retry the confirmation request.")
			return
		}
		if !isUnsafeImageError(verificationErr) {
			logRequestFailure(
				c,
				"verify_uploaded_image",
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
			fileResponse(file),
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
	c.JSON(http.StatusOK, fileResponse(file))
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
		items = append(items, fileResponse(file))
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
	signed, err := a.store.SignRead(c.Request.Context(), file.ObjectKey, 5*time.Minute)
	if err != nil {
		logRequestFailure(c, "storage_sign_read", err)
		writeProblem(c, http.StatusInternalServerError, "File URL unavailable", "A temporary file URL could not be created.")
		return
	}
	c.JSON(http.StatusOK, signedRequestResponse(signed))
}

func (a *App) localContent(c *gin.Context) {
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
	local, ok := storage.AsLocal(a.store)
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
	c.Header("Content-Disposition", "inline; filename="+strconvQuote(metadata.OriginalName))
	c.DataFromReader(http.StatusOK, metadata.Size, metadata.ContentType, file, nil)
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
			time.Time{},
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

func extensionFor(contentType string) string {
	switch strings.ToLower(contentType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

func strconvQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, "") + `"`
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

func fileResponse(file domain.FileObject) FileResponse {
	return FileResponse{
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
	}
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
	payload, err := json.Marshal(filecleanup.PayloadV2{
		FileID:    file.ID,
		Provider:  file.Provider,
		ObjectKey: file.ObjectKey,
		Mode:      mode,
	})
	if err != nil {
		return err
	}
	principal := currentPrincipal(c)
	_, err = queue.Enqueue(c.Request.Context(), jobs.EnqueueRequest{
		Type:           filecleanup.JobType,
		Version:        filecleanup.PayloadVersion2,
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

func isUnsafeImageError(err error) bool {
	return errors.Is(err, frameworkstorage.ErrEmptyContent) ||
		errors.Is(err, frameworkstorage.ErrContentTooLarge) ||
		errors.Is(err, frameworkstorage.ErrUnsupportedMIMEType) ||
		errors.Is(err, frameworkstorage.ErrMIMETypeMismatch) ||
		errors.Is(err, frameworkstorage.ErrMalformedImage) ||
		errors.Is(err, frameworkstorage.ErrImageDimensionsExceeded) ||
		errors.Is(err, frameworkstorage.ErrImagePixelsExceeded)
}

func writeFileVerificationProblem(c *gin.Context, err error) {
	if errors.Is(err, frameworkstorage.ErrContentTooLarge) {
		httpx.WriteProblem(
			c,
			http.StatusRequestEntityTooLarge,
			"FILE_TOO_LARGE",
			"Uploaded image is too large",
			"Upload an image within the configured byte limit.",
		)
		return
	}
	httpx.WriteProblem(
		c,
		http.StatusUnprocessableEntity,
		"FILE_CONTENT_INVALID",
		"Uploaded image is invalid",
		"The object must be a complete JPEG, PNG, or WebP image within the configured dimension limits.",
	)
}
