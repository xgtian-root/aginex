package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	frameworkjobs "github.com/xgtian-root/aginex/backend/framework/jobs"
	"github.com/xgtian-root/aginex/backend/framework/services"
	frameworkstorage "github.com/xgtian-root/aginex/backend/framework/storage"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/multipartcleanup"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const multipartPartURLTTL = 10 * time.Minute

var (
	errUploadSessionChanged      = errors.New("upload session changed")
	errUploadSessionTransitioned = errors.New("upload session transition already started")
)

func (a *App) createResumableUploadIntent(c *gin.Context, input UploadIntentRequest) {
	runtimePolicy := a.cfg.FileUploadRuntime()
	if !runtimePolicy.ResumableUploadsEnabled {
		writeUploadSessionProblem(c, http.StatusConflict, "RESUMABLE_UPLOADS_DISABLED", "Resumable uploads are disabled", "Use the single strategy or enable resumable uploads and restart the API and worker.")
		return
	}
	if input.Size <= multipartThresholdBytes {
		writeUploadSessionProblem(c, http.StatusUnprocessableEntity, "RESUMABLE_THRESHOLD_NOT_MET", "Resumable upload is not applicable", "Use the single strategy for files of 32 MiB or less.")
		return
	}
	if len(input.ResumeFingerprint) != 64 {
		writeUploadSessionProblem(c, http.StatusBadRequest, "RESUME_FINGERPRINT_REQUIRED", "Resume fingerprint is required", "Provide the 64-character SHA-256 recovery fingerprint for a resumable upload.")
		return
	}
	multipartStore, ok := storage.AsMultipart(a.store)
	if !ok {
		writeUploadSessionProblem(c, http.StatusUnprocessableEntity, "RESUMABLE_PROVIDER_UNSUPPORTED", "Storage does not support resumable uploads", "Use the single strategy with the active storage provider.")
		return
	}

	now := time.Now().UTC()
	partCount := int((input.Size + multipartPartSizeBytes - 1) / multipartPartSizeBytes)
	if partCount < 2 || partCount > 32 {
		writeUploadSessionProblem(c, http.StatusUnprocessableEntity, "RESUMABLE_PART_COUNT_INVALID", "File cannot be split safely", "Choose a file that fits within the configured multipart limits.")
		return
	}
	activeProfile, ok := a.storageRegistry.ActiveProfile()
	if !ok {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The active storage profile is unavailable.")
		return
	}
	profileID := activeProfile.ID
	activeStorage := activeProfile.StorageConfig()
	principal := currentPrincipal(c)
	file := domain.FileObject{
		ID: uuid.NewString(), StorageProfileID: &profileID,
		Provider: activeStorage.Driver, Bucket: activeStorage.Bucket,
		ObjectKey:    "uploads/" + now.Format("2006/01") + "/" + uuid.NewString(),
		OriginalName: safeOriginalFilename(input.Filename), ContentType: input.ContentType,
		Size: input.Size, OwnerID: principal.User.ID, Visibility: input.Visibility,
		Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	upload, err := multipartStore.InitiateMultipart(c.Request.Context(), frameworkstorage.UploadRequest{
		Key: file.ObjectKey, ContentType: frameworkstorage.StoredContentType,
		Size: file.Size, Expires: time.Duration(multipartSessionSeconds) * time.Second,
	})
	if err != nil {
		logRequestFailure(c, "storage_initiate_multipart", err)
		writeProblem(c, http.StatusBadRequest, "Upload could not be prepared", "The storage provider could not start a resumable upload.")
		return
	}
	session := domain.FileUploadSession{
		ID: uuid.NewString(), FileID: file.ID,
		ProviderUploadID:  upload.ProviderUploadID,
		ResumeFingerprint: input.ResumeFingerprint,
		PartSize:          multipartPartSizeBytes, PartCount: partCount,
		Status:    domain.FileUploadSessionStatusActive,
		ExpiresAt: now.Add(time.Duration(multipartSessionSeconds) * time.Second),
		CreatedAt: now, UpdatedAt: now,
	}
	committed := false
	defer func() {
		if !committed {
			abortContext, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Second)
			defer cancel()
			_ = multipartStore.AbortMultipart(abortContext, upload)
		}
	}()

	response := UploadIntentResponse{
		Strategy: "resumable",
		File:     a.fileResponse(file),
		Session:  pointerTo(a.uploadSessionResponseValue(file, session, nil)),
	}
	err = a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := tx.Create(&file).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := tx.Create(&session).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if a.jobs != nil {
			if err := a.enqueueMultipartCleanup(c, tx, session, multipartcleanup.CauseExpiry, session.ExpiresAt); err != nil {
				return frameworkaudit.Event{}, err
			}
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusCreated, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(
			c, &principal.User.ID, "files:create-resumable-intent", "file-upload-session",
			session.ID, "Prepared resumable upload for "+file.OriginalName,
			nil, uploadSessionAuditFields(file, session, 0),
		), nil
	})
	if err != nil {
		logRequestFailure(c, "create_resumable_upload_intent", err)
		writeProblem(c, http.StatusInternalServerError, "Upload could not be prepared", "The resumable upload intent could not be committed.")
		return
	}
	committed = true
	c.JSON(http.StatusCreated, response)
}

func (a *App) listUploadSessions(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	if state != "" && state != "incomplete" {
		writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_SESSION_STATE_INVALID", "Session state filter is invalid", "Use state=incomplete.")
		return
	}
	page, pageSize := pagination(c)
	runtime, ok := services.RuntimeFromContext(c.Request.Context())
	if !ok {
		writeProblem(c, http.StatusInternalServerError, "Upload sessions unavailable", "The module query service is unavailable.")
		return
	}
	statuses := incompleteUploadSessionStatuses()
	query, ok := scopeFileQuery(c, "files:create", runtime.Database.Model(&domain.FileObject{}))
	if !ok {
		return
	}
	query = query.Joins("JOIN file_upload_sessions ON file_upload_sessions.file_id = file_objects.id").
		Where("file_upload_sessions.status IN ?", statuses)
	var total int64
	if result := query.Distinct("file_objects.id").Count(&total); result.Error != nil {
		logRequestFailure(c, "list_upload_sessions_count", result.Error)
		writeProblem(c, http.StatusInternalServerError, "Upload sessions unavailable", "The upload session list could not be loaded.")
		return
	}
	var files []domain.FileObject
	if result := query.Order("file_upload_sessions.updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&files); result.Error != nil {
		logRequestFailure(c, "list_upload_sessions_query", result.Error)
		writeProblem(c, http.StatusInternalServerError, "Upload sessions unavailable", "The upload session list could not be loaded.")
		return
	}
	items := make([]UploadSessionResponse, 0, len(files))
	for _, file := range files {
		var session domain.FileUploadSession
		if err := a.db.WithContext(c.Request.Context()).First(&session, "file_id = ?", file.ID).Error; err != nil {
			logRequestFailure(c, "load_upload_session", err)
			writeProblem(c, http.StatusInternalServerError, "Upload sessions unavailable", "An upload session could not be loaded.")
			return
		}
		response, err := a.uploadSessionResponse(c.Request.Context(), a.db, file, session)
		if err != nil {
			logRequestFailure(c, "load_upload_session_parts", err)
			writeProblem(c, http.StatusInternalServerError, "Upload sessions unavailable", "Upload progress could not be loaded.")
			return
		}
		items = append(items, response)
	}
	c.JSON(http.StatusOK, Page[UploadSessionResponse]{Items: items, Page: page, PageSize: pageSize, Total: total})
}

func (a *App) getUploadSession(c *gin.Context) {
	file, session, ok := a.loadAuthorizedUploadSession(c, c.Param("id"))
	if !ok {
		return
	}
	response, err := a.uploadSessionResponse(c.Request.Context(), a.db, file, session)
	if err != nil {
		logRequestFailure(c, "get_upload_session", err)
		writeProblem(c, http.StatusInternalServerError, "Upload session unavailable", "Upload progress could not be loaded.")
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) resumeUploadSession(c *gin.Context) {
	input, ok := validatedRequestDTO[ResumeUploadSessionRequest](c)
	if !ok {
		return
	}
	file, session, ok := a.loadAuthorizedUploadSession(c, c.Param("id"))
	if !ok {
		return
	}
	if subtle.ConstantTimeCompare([]byte(input.Fingerprint), []byte(session.ResumeFingerprint)) != 1 {
		writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_FINGERPRINT_MISMATCH", "File does not match this upload", "Re-select the original file used to create this resumable upload.")
		return
	}
	if !a.requireActiveUploadSession(c, session) {
		return
	}
	multipartStore, upload, ok := a.multipartStoreForSession(c, file, session)
	if !ok {
		return
	}
	inventory, err := multipartStore.ListUploadedParts(c.Request.Context(), upload)
	if err != nil {
		logRequestFailure(c, "storage_list_multipart_resume", err)
		writeProblem(c, http.StatusServiceUnavailable, "Upload session unavailable", "The storage provider could not reconcile uploaded parts.")
		return
	}
	providerParts := uploadedPartMap(inventory)
	var persisted []domain.FileUploadPart
	if err := a.db.WithContext(c.Request.Context()).Where("session_id = ?", session.ID).Order("part_number").Find(&persisted).Error; err != nil {
		logRequestFailure(c, "load_acknowledged_parts", err)
		writeProblem(c, http.StatusInternalServerError, "Upload session unavailable", "Acknowledged parts could not be loaded.")
		return
	}
	stale := make([]int, 0)
	for _, part := range persisted {
		provider, exists := providerParts[int32(part.PartNumber)]
		if !exists || provider.Size != part.Size || normalizeOpaqueETag(provider.ETag) != normalizeOpaqueETag(part.ETag) {
			stale = append(stale, part.PartNumber)
		}
	}
	if len(stale) > 0 {
		principal := currentPrincipal(c)
		err = a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
			lockedFile, lockedSession, err := lockUploadSession(tx, session.ID)
			if err != nil {
				return frameworkaudit.Event{}, err
			}
			if err := checkFileAuthorization(c, "files:create", lockedFile); err != nil {
				return frameworkaudit.Event{}, err
			}
			if lockedSession.Status != domain.FileUploadSessionStatusActive {
				return frameworkaudit.Event{}, errUploadSessionChanged
			}
			if err := tx.Where("session_id = ? AND part_number IN ?", session.ID, stale).Delete(&domain.FileUploadPart{}).Error; err != nil {
				return frameworkaudit.Event{}, err
			}
			lockedSession.UpdatedAt = time.Now().UTC()
			if err := tx.Save(&lockedSession).Error; err != nil {
				return frameworkaudit.Event{}, err
			}
			return successfulAuditEvent(c, &principal.User.ID, "files:resume-reconcile", "file-upload-session", session.ID, "Reconciled resumable upload progress", nil, uploadSessionAuditFields(lockedFile, lockedSession, len(persisted)-len(stale))), nil
		})
		if err != nil {
			writeUploadSessionWriteError(c, "resume_upload_session", err)
			return
		}
	}
	response, err := a.uploadSessionResponse(c.Request.Context(), a.db, file, session)
	if err != nil {
		logRequestFailure(c, "resume_upload_session_response", err)
		writeProblem(c, http.StatusInternalServerError, "Upload session unavailable", "Upload progress could not be loaded.")
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) signUploadSessionParts(c *gin.Context) {
	input, ok := validatedRequestDTO[SignUploadPartsRequest](c)
	if !ok {
		return
	}
	file, session, ok := a.loadAuthorizedUploadSession(c, c.Param("id"))
	if !ok || !a.requireActiveUploadSession(c, session) {
		return
	}
	multipartStore, upload, ok := a.multipartStoreForSession(c, file, session)
	if !ok {
		return
	}
	fileStore, err := a.storeForFile(file)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The upload session's storage profile is unavailable.")
		return
	}
	seen := make(map[int]struct{}, len(input.PartNumbers))
	items := make([]SignedUploadPartResponse, 0, len(input.PartNumbers))
	for _, partNumber := range input.PartNumbers {
		if _, duplicate := seen[partNumber]; duplicate {
			writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_PART_DUPLICATE", "Part number is duplicated", "Request each part number at most once.")
			return
		}
		seen[partNumber] = struct{}{}
		size, err := expectedUploadPartSize(file.Size, session, partNumber)
		if err != nil {
			writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_PART_INVALID", "Part number is invalid", "Request a part within this session's manifest.")
			return
		}
		var signed frameworkstorage.SignedRequest
		if _, local := storage.AsLocal(fileStore); local {
			signed = a.signLocalUploadPart(session.ID, partNumber, size)
		} else {
			signed, err = multipartStore.SignUploadPart(c.Request.Context(), frameworkstorage.MultipartPartRequest{
				Upload: upload, PartNumber: int32(partNumber), Size: size, Expires: multipartPartURLTTL,
			})
			if err != nil {
				logRequestFailure(c, "storage_sign_multipart_part", err)
				writeProblem(c, http.StatusServiceUnavailable, "Part upload unavailable", "The storage provider could not sign an upload part.")
				return
			}
		}
		items = append(items, SignedUploadPartResponse{PartNumber: partNumber, Size: size, Upload: signedRequestResponse(signed)})
	}
	c.JSON(http.StatusOK, SignUploadPartsResponse{Items: items})
}

func (a *App) localUploadSessionPart(c *gin.Context) {
	partNumber, err := strconv.Atoi(c.Param("number"))
	if err != nil {
		writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_PART_INVALID", "Part number is invalid", "Use a numeric part number within the session manifest.")
		return
	}
	file, session, ok := a.loadAuthorizedUploadSession(c, c.Param("id"))
	if !ok || !a.requireActiveUploadSession(c, session) {
		return
	}
	if !a.verifyLocalUploadPartSignature(c, session.ID, partNumber) {
		return
	}
	fileStore, err := a.storeForFile(file)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The file's storage profile is unavailable.")
		return
	}
	local, ok := storage.AsLocal(fileStore)
	if !ok {
		writeUploadSessionProblem(c, http.StatusNotFound, "LOCAL_PART_UPLOAD_UNAVAILABLE", "Local part upload is unavailable", "Use the signed cloud request returned for this part.")
		return
	}
	size, err := expectedUploadPartSize(file.Size, session, partNumber)
	if err != nil {
		writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_PART_INVALID", "Part number is invalid", "Use a part within this session's manifest.")
		return
	}
	contentType, _, parseErr := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if parseErr != nil || contentType != frameworkstorage.StoredContentType {
		writeUploadSessionProblem(c, http.StatusUnsupportedMediaType, "UPLOAD_PART_CONTENT_TYPE_INVALID", "Part content type is invalid", "Upload each part as application/octet-stream.")
		return
	}
	if c.Request.ContentLength != size {
		writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_PART_SIZE_MISMATCH", "Part size does not match", "Upload the exact part byte length returned by the signing endpoint.")
		return
	}
	cancelTransfer := setFileTransferDeadline(c)
	defer cancelTransfer()
	body := http.MaxBytesReader(c.Writer, c.Request.Body, size+1)
	part, err := local.PutMultipartPart(c.Request.Context(), frameworkstorage.MultipartPartRequest{
		Upload:     frameworkstorage.MultipartUpload{Key: file.ObjectKey, ProviderUploadID: session.ProviderUploadID},
		PartNumber: int32(partNumber), Size: size,
	}, body)
	if err != nil {
		logRequestFailure(c, "local_part_upload", err)
		if errors.Is(err, frameworkstorage.ErrFileSizeMismatch) || errors.Is(err, frameworkstorage.ErrContentTooLarge) {
			writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_PART_SIZE_MISMATCH", "Part size does not match", "Upload the exact part byte length returned by the signing endpoint.")
			return
		}
		writeProblem(c, http.StatusBadRequest, "Part upload failed", "The local provider could not persist this part.")
		return
	}
	c.Header("ETag", part.ETag)
	c.Status(http.StatusNoContent)
}

func (a *App) ackUploadSessionParts(c *gin.Context) {
	input, ok := validatedRequestDTO[AckUploadPartsRequest](c)
	if !ok {
		return
	}
	file, session, ok := a.loadAuthorizedUploadSession(c, c.Param("id"))
	if !ok || !a.requireActiveUploadSession(c, session) {
		return
	}
	multipartStore, upload, ok := a.multipartStoreForSession(c, file, session)
	if !ok {
		return
	}
	inventory, err := multipartStore.ListUploadedParts(c.Request.Context(), upload)
	if err != nil {
		logRequestFailure(c, "storage_list_multipart_ack", err)
		writeProblem(c, http.StatusServiceUnavailable, "Part acknowledgement unavailable", "The storage provider could not verify uploaded parts.")
		return
	}
	providerParts := uploadedPartMap(inventory)
	confirmed := make([]domain.FileUploadPart, 0, len(input.Parts))
	seen := make(map[int]struct{}, len(input.Parts))
	now := time.Now().UTC()
	for _, acknowledged := range input.Parts {
		if _, duplicate := seen[acknowledged.PartNumber]; duplicate {
			writeUploadSessionProblem(c, http.StatusBadRequest, "UPLOAD_PART_DUPLICATE", "Part number is duplicated", "Acknowledge each part number at most once.")
			return
		}
		seen[acknowledged.PartNumber] = struct{}{}
		expectedSize, err := expectedUploadPartSize(file.Size, session, acknowledged.PartNumber)
		provider, exists := providerParts[int32(acknowledged.PartNumber)]
		if err != nil || !exists || provider.Size != expectedSize || normalizeOpaqueETag(provider.ETag) != normalizeOpaqueETag(acknowledged.ETag) {
			writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_PART_MISMATCH", "Uploaded part does not match", "Re-sign and upload the part again before acknowledging it.")
			return
		}
		confirmed = append(confirmed, domain.FileUploadPart{
			SessionID: session.ID, PartNumber: acknowledged.PartNumber,
			// Persist the ETag returned by UploadPart to the browser. Provider
			// inventory is only a verification source; it is not the completion
			// manifest promised by S3-compatible multipart contracts.
			Size: provider.Size, ETag: strings.TrimSpace(acknowledged.ETag), ConfirmedAt: now,
		})
	}
	principal := currentPrincipal(c)
	err = a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		lockedFile, lockedSession, err := lockUploadSession(tx, session.ID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", lockedFile); err != nil {
			return frameworkaudit.Event{}, err
		}
		if lockedSession.Status != domain.FileUploadSessionStatusActive || lockedSession.ExpiresAt.Before(now) {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		for _, part := range confirmed {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "session_id"}, {Name: "part_number"}},
				DoUpdates: clause.AssignmentColumns([]string{"size", "etag", "confirmed_at"}),
			}).Create(&part).Error; err != nil {
				return frameworkaudit.Event{}, err
			}
		}
		lockedSession.UpdatedAt = now
		if err := tx.Save(&lockedSession).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		response, err := a.uploadSessionResponse(c.Request.Context(), tx, lockedFile, lockedSession)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "files:acknowledge-parts", "file-upload-session", session.ID, "Acknowledged uploaded file parts", nil, uploadSessionAuditFields(lockedFile, lockedSession, len(response.CompletedParts))), nil
	})
	if err != nil {
		writeUploadSessionWriteError(c, "ack_upload_session_parts", err)
		return
	}
	response, err := a.uploadSessionResponse(c.Request.Context(), a.db, file, session)
	if err != nil {
		writeProblem(c, http.StatusInternalServerError, "Upload session unavailable", "Upload progress could not be loaded.")
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) completeUploadSession(c *gin.Context) {
	cancelTransfer := setFileTransferDeadline(c)
	defer cancelTransfer()
	file, session, ok := a.loadAuthorizedUploadSession(c, c.Param("id"))
	if !ok {
		return
	}
	if session.Status == domain.FileUploadSessionStatusCompleted && file.Status == "ready" {
		c.JSON(http.StatusOK, a.fileResponse(file))
		return
	}
	if !completionMayProceed(session) {
		writeUploadSessionStateProblem(c, session)
		return
	}
	if session.ExpiresAt.Before(time.Now().UTC()) && session.Status == domain.FileUploadSessionStatusActive {
		writeUploadSessionProblem(c, http.StatusGone, "UPLOAD_SESSION_EXPIRED", "Upload session has expired", "Create a new upload intent.")
		return
	}
	multipartStore, upload, ok := a.multipartStoreForSession(c, file, session)
	if !ok {
		return
	}
	fileStore, err := a.storeForFile(file)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The upload session's storage profile is unavailable.")
		return
	}
	manifest, err := a.persistedCompletionManifest(c.Request.Context(), file, session)
	if err != nil {
		if errors.Is(err, errUploadSessionChanged) {
			writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_PARTS_INCOMPLETE", "Upload parts are incomplete", "Upload and acknowledge every part before completing the session.")
			return
		}
		logRequestFailure(c, "load_completion_manifest", err)
		writeProblem(c, http.StatusInternalServerError, "Upload could not be completed", "The acknowledged part manifest could not be loaded.")
		return
	}
	var info frameworkstorage.ObjectInfo
	switch session.Status {
	case domain.FileUploadSessionStatusActive:
		inventory, listErr := multipartStore.ListUploadedParts(c.Request.Context(), upload)
		if listErr != nil || !multipartManifestMatches(manifest, inventory) {
			if listErr != nil {
				logRequestFailure(c, "storage_list_multipart_complete", listErr)
			}
			writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_PART_MISMATCH", "Uploaded parts do not match", "Re-upload and acknowledge the missing or changed parts before completing.")
			return
		}
		if err := a.beginUploadSessionCompletion(c, session.ID); err != nil && !errors.Is(err, errUploadSessionTransitioned) {
			writeUploadSessionWriteError(c, "begin_upload_session_completion", err)
			return
		}
		session.Status = domain.FileUploadSessionStatusCompleting
		fallthrough
	case domain.FileUploadSessionStatusCompleting:
		inventory, listErr := multipartStore.ListUploadedParts(c.Request.Context(), upload)
		if listErr == nil && !multipartManifestMatches(manifest, inventory) {
			if err := a.resetUploadSessionAfterCompletionMismatch(c, session.ID); err != nil && !errors.Is(err, errUploadSessionTransitioned) {
				writeUploadSessionWriteError(c, "reset_upload_session_after_part_mismatch", err)
				return
			}
			writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_PART_MISMATCH", "Uploaded parts do not match", "Re-upload and acknowledge the missing or changed parts before completing.")
			return
		}
		completeErr := listErr
		if listErr == nil {
			info, completeErr = multipartStore.CompleteMultipart(c.Request.Context(), frameworkstorage.MultipartCompleteRequest{Upload: upload, Parts: manifest})
		}
		if completeErr != nil {
			info, err = fileStore.Stat(c.Request.Context(), file.ObjectKey)
			if err != nil || info.Size != file.Size {
				logRequestFailure(c, "storage_complete_multipart", completeErr)
				writeProblem(c, http.StatusServiceUnavailable, "Upload completion is uncertain", "Retry completion; the server will recover an already-merged object safely.")
				return
			}
		}
		if err := a.markUploadSessionVerifying(c, session.ID); err != nil && !errors.Is(err, errUploadSessionTransitioned) {
			writeUploadSessionWriteError(c, "mark_upload_session_verifying", err)
			return
		}
		session.Status = domain.FileUploadSessionStatusVerifying
	case domain.FileUploadSessionStatusVerifying:
		info, err = fileStore.Stat(c.Request.Context(), file.ObjectKey)
		if err != nil {
			logRequestFailure(c, "storage_stat_completed_upload", err)
			writeProblem(c, http.StatusServiceUnavailable, "Upload completion is uncertain", "Retry completion after the final object becomes readable.")
			return
		}
	}
	if info.Size != file.Size {
		writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_PART_MISMATCH", "Completed object size does not match", "The final object does not match the upload intent.")
		return
	}
	reader, err := fileStore.Open(c.Request.Context(), file.ObjectKey)
	if err != nil {
		logRequestFailure(c, "storage_open_completed_upload", err)
		writeProblem(c, http.StatusServiceUnavailable, "Upload could not be verified", "Retry completion after the final object becomes readable.")
		return
	}
	verifier, verifierErr := a.fileVerifierForIntent(file.Size)
	if verifierErr != nil {
		_ = reader.Close()
		logRequestFailure(c, "configure_completed_file_verifier", verifierErr)
		writeProblem(c, http.StatusInternalServerError, "Upload could not be verified", "The upload policy snapshot could not be restored.")
		return
	}
	verified, verificationErr := verifier.Verify(c.Request.Context(), reader, file.Size)
	closeErr := reader.Close()
	if verificationErr == nil && closeErr != nil {
		verificationErr = closeErr
	}
	if verificationErr != nil {
		if writeFileVerificationInterrupted(c, verificationErr) {
			return
		}
		if errors.Is(verificationErr, frameworkstorage.ErrEmptyContent) || errors.Is(verificationErr, frameworkstorage.ErrFileSizeMismatch) || errors.Is(verificationErr, frameworkstorage.ErrContentTooLarge) {
			writeFileVerificationProblem(c, verificationErr)
			return
		}
		logRequestFailure(c, "verify_completed_upload", verificationErr)
		writeProblem(c, http.StatusInternalServerError, "Upload could not be verified", "Retry completion after the final object becomes readable.")
		return
	}
	principal := currentPrincipal(c)
	err = a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		lockedFile, lockedSession, err := lockUploadSession(tx, session.ID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", lockedFile); err != nil {
			return frameworkaudit.Event{}, err
		}
		if lockedSession.Status == domain.FileUploadSessionStatusCompleted && lockedFile.Status == "ready" {
			return frameworkaudit.Event{}, errUploadSessionTransitioned
		}
		if lockedFile.Status != "pending" || (lockedSession.Status != domain.FileUploadSessionStatusVerifying && lockedSession.Status != domain.FileUploadSessionStatusCompleting) {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		before := uploadSessionAuditFields(lockedFile, lockedSession, len(manifest))
		now := time.Now().UTC()
		lockedFile.Status = "ready"
		lockedFile.ETag = info.ETag
		lockedFile.ContentType = verified.MIMEType
		lockedFile.Size = verified.Size
		lockedFile.SHA256 = verified.SHA256
		lockedFile.Width = verified.Width
		lockedFile.Height = verified.Height
		lockedFile.UpdatedAt = now
		lockedSession.Status = domain.FileUploadSessionStatusCompleted
		lockedSession.UpdatedAt = now
		if err := tx.Save(&lockedFile).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := tx.Save(&lockedSession).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		response := a.fileResponse(lockedFile)
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "files:complete-resumable", "file", lockedFile.ID, "Completed "+lockedFile.OriginalName, before, uploadSessionAuditFields(lockedFile, lockedSession, len(manifest))), nil
	})
	if errors.Is(err, errUploadSessionTransitioned) {
		var current domain.FileObject
		if loadErr := a.db.WithContext(c.Request.Context()).First(&current, "id = ?", file.ID).Error; loadErr == nil && current.Status == "ready" {
			c.JSON(http.StatusOK, a.fileResponse(current))
			return
		}
	}
	if err != nil {
		writeUploadSessionWriteError(c, "complete_upload_session", err)
		return
	}
	var completed domain.FileObject
	if err := a.db.WithContext(c.Request.Context()).First(&completed, "id = ?", file.ID).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Upload completed", "The completed file metadata could not be reloaded.")
		return
	}
	c.JSON(http.StatusOK, a.fileResponse(completed))
}

func (a *App) cancelUploadSession(c *gin.Context) {
	file, session, ok := a.loadAuthorizedUploadSession(c, c.Param("id"))
	if !ok {
		return
	}
	switch session.Status {
	case domain.FileUploadSessionStatusCancelled, domain.FileUploadSessionStatusExpired:
		c.Status(http.StatusNoContent)
		return
	case domain.FileUploadSessionStatusCompleting, domain.FileUploadSessionStatusVerifying, domain.FileUploadSessionStatusCompleted:
		writeUploadSessionStateProblem(c, session)
		return
	}
	if session.Status == domain.FileUploadSessionStatusActive {
		principal := currentPrincipal(c)
		err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
			lockedFile, lockedSession, err := lockUploadSession(tx, session.ID)
			if err != nil {
				return frameworkaudit.Event{}, err
			}
			if err := checkFileAuthorization(c, "files:create", lockedFile); err != nil {
				return frameworkaudit.Event{}, err
			}
			if lockedSession.Status != domain.FileUploadSessionStatusActive {
				return frameworkaudit.Event{}, errUploadSessionTransitioned
			}
			before := uploadSessionAuditFields(lockedFile, lockedSession, 0)
			lockedSession.Status = domain.FileUploadSessionStatusCancelling
			lockedSession.UpdatedAt = time.Now().UTC()
			if err := tx.Save(&lockedSession).Error; err != nil {
				return frameworkaudit.Event{}, err
			}
			if a.jobs != nil {
				if err := a.enqueueMultipartCleanup(c, tx, lockedSession, multipartcleanup.CauseCancel, time.Time{}); err != nil {
					return frameworkaudit.Event{}, err
				}
			}
			return successfulAuditEvent(c, &principal.User.ID, "files:cancel-resumable-request", "file-upload-session", session.ID, "Requested resumable upload cancellation", before, uploadSessionAuditFields(lockedFile, lockedSession, 0)), nil
		})
		if err != nil && !errors.Is(err, errUploadSessionTransitioned) {
			writeUploadSessionWriteError(c, "begin_cancel_upload_session", err)
			return
		}
	}
	// Reload after the CAS attempt before touching provider state. Completion
	// may have won the active -> completing race; in that case aborting here
	// would destroy the exact multipart upload the completion path owns.
	if err := a.db.WithContext(c.Request.Context()).First(&session, "id = ?", session.ID).Error; err != nil {
		notFoundOrInternal(c, "Upload session", err)
		return
	}
	if err := a.db.WithContext(c.Request.Context()).First(&file, "id = ?", session.FileID).Error; err != nil {
		notFoundOrInternal(c, "File", err)
		return
	}
	switch session.Status {
	case domain.FileUploadSessionStatusCancelled, domain.FileUploadSessionStatusExpired:
		c.Status(http.StatusNoContent)
		return
	case domain.FileUploadSessionStatusCancelling, domain.FileUploadSessionStatusExpiring:
		// These states exclusively own provider abort.
	default:
		writeUploadSessionStateProblem(c, session)
		return
	}
	multipartStore, upload, ok := a.multipartStoreForSession(c, file, session)
	if !ok {
		return
	}
	if err := multipartStore.AbortMultipart(c.Request.Context(), upload); err != nil {
		logRequestFailure(c, "storage_abort_multipart", err)
		writeProblem(c, http.StatusServiceUnavailable, "Upload could not be cancelled", "Retry cancellation after the storage provider becomes available.")
		return
	}
	principal := currentPrincipal(c)
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		lockedFile, lockedSession, err := lockUploadSession(tx, session.ID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", lockedFile); err != nil {
			return frameworkaudit.Event{}, err
		}
		if lockedSession.Status == domain.FileUploadSessionStatusCancelled {
			return frameworkaudit.Event{}, errUploadSessionTransitioned
		}
		if lockedSession.Status != domain.FileUploadSessionStatusCancelling && lockedSession.Status != domain.FileUploadSessionStatusExpiring {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		before := uploadSessionAuditFields(lockedFile, lockedSession, 0)
		now := time.Now().UTC()
		lockedSession.Status = domain.FileUploadSessionStatusCancelled
		lockedSession.UpdatedAt = now
		lockedFile.Status = "deleted"
		lockedFile.UpdatedAt = now
		lockedFile.DeletedAt = &now
		if err := tx.Save(&lockedSession).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := tx.Save(&lockedFile).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := a.completeIdempotentWrite(c, tx, http.StatusNoContent, nil, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "files:cancel-resumable", "file-upload-session", session.ID, "Cancelled resumable upload", before, uploadSessionAuditFields(lockedFile, lockedSession, 0)), nil
	})
	if err != nil && !errors.Is(err, errUploadSessionTransitioned) {
		writeUploadSessionWriteError(c, "cancel_upload_session", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) enqueueMultipartCleanup(
	c *gin.Context,
	tx *gorm.DB,
	session domain.FileUploadSession,
	cause multipartcleanup.Cause,
	scheduledAt time.Time,
) error {
	queue, err := a.jobs.Bind(tx)
	if err != nil {
		return err
	}
	principal := currentPrincipal(c)
	request, err := multipartcleanup.NewEnqueueRequest(
		session.ID,
		cause,
		scheduledAt,
		principal.Actor(),
		frameworkjobs.TraceContext{
			RequestID:   c.Writer.Header().Get("X-Request-ID"),
			TraceParent: c.Writer.Header().Get(httpx.TraceParentHeader),
		},
	)
	if err != nil {
		return err
	}
	_, err = queue.Enqueue(c.Request.Context(), request)
	return err
}

func (a *App) loadAuthorizedUploadSession(c *gin.Context, sessionID string) (domain.FileObject, domain.FileUploadSession, bool) {
	file, found := a.findAuthorizedFile(
		c, "files:create",
		"EXISTS (SELECT 1 FROM file_upload_sessions WHERE file_upload_sessions.file_id = file_objects.id AND file_upload_sessions.id = ?)",
		sessionID,
	)
	if !found {
		return domain.FileObject{}, domain.FileUploadSession{}, false
	}
	var session domain.FileUploadSession
	if err := a.db.WithContext(c.Request.Context()).First(&session, "id = ? AND file_id = ?", sessionID, file.ID).Error; err != nil {
		notFoundOrInternal(c, "Upload session", err)
		return domain.FileObject{}, domain.FileUploadSession{}, false
	}
	return file, session, true
}

func (a *App) uploadSessionResponse(ctx context.Context, db *gorm.DB, file domain.FileObject, session domain.FileUploadSession) (UploadSessionResponse, error) {
	var parts []domain.FileUploadPart
	if err := db.WithContext(ctx).Where("session_id = ?", session.ID).Order("part_number").Find(&parts).Error; err != nil {
		return UploadSessionResponse{}, err
	}
	return a.uploadSessionResponseValue(file, session, parts), nil
}

func (a *App) uploadSessionResponseValue(file domain.FileObject, session domain.FileUploadSession, parts []domain.FileUploadPart) UploadSessionResponse {
	completed := make([]UploadPartResponse, 0, len(parts))
	for _, part := range parts {
		completed = append(completed, UploadPartResponse{PartNumber: part.PartNumber, Size: part.Size, ConfirmedAt: part.ConfirmedAt})
	}
	return UploadSessionResponse{
		ID: session.ID, File: a.fileResponse(file), Status: string(session.Status),
		PartSize: session.PartSize, PartCount: session.PartCount,
		CompletedParts: completed, ExpiresAt: session.ExpiresAt,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
}

func (a *App) multipartStoreForSession(c *gin.Context, file domain.FileObject, session domain.FileUploadSession) (frameworkstorage.MultipartObjectStore, frameworkstorage.MultipartUpload, bool) {
	fileStore, err := a.storeForFile(file)
	if err != nil {
		writeProblem(c, http.StatusServiceUnavailable, "Storage unavailable", "The upload session's storage profile is unavailable.")
		return nil, frameworkstorage.MultipartUpload{}, false
	}
	multipartStore, ok := storage.AsMultipart(fileStore)
	if !ok {
		writeUploadSessionProblem(c, http.StatusUnprocessableEntity, "RESUMABLE_PROVIDER_UNSUPPORTED", "Storage does not support resumable uploads", "The upload session's storage provider does not expose multipart support.")
		return nil, frameworkstorage.MultipartUpload{}, false
	}
	return multipartStore, frameworkstorage.MultipartUpload{Key: file.ObjectKey, ProviderUploadID: session.ProviderUploadID}, true
}

func (a *App) requireActiveUploadSession(c *gin.Context, session domain.FileUploadSession) bool {
	if session.Status != domain.FileUploadSessionStatusActive {
		writeUploadSessionStateProblem(c, session)
		return false
	}
	if !session.ExpiresAt.After(time.Now().UTC()) {
		writeUploadSessionProblem(c, http.StatusGone, "UPLOAD_SESSION_EXPIRED", "Upload session has expired", "Create a new upload intent.")
		return false
	}
	return true
}

func expectedUploadPartSize(fileSize int64, session domain.FileUploadSession, number int) (int64, error) {
	if number < 1 || number > session.PartCount || session.PartSize != multipartPartSizeBytes {
		return 0, errUploadSessionChanged
	}
	if number < session.PartCount {
		return session.PartSize, nil
	}
	size := fileSize - int64(number-1)*session.PartSize
	if size < 1 || size > session.PartSize {
		return 0, errUploadSessionChanged
	}
	return size, nil
}

func uploadedPartMap(parts []frameworkstorage.UploadedPart) map[int32]frameworkstorage.UploadedPart {
	result := make(map[int32]frameworkstorage.UploadedPart, len(parts))
	for _, part := range parts {
		result[part.PartNumber] = part
	}
	return result
}

func normalizeOpaqueETag(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "W/"))
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	return value
}

func (a *App) signLocalUploadPart(sessionID string, partNumber int, size int64) frameworkstorage.SignedRequest {
	expiresAt := time.Now().UTC().Add(multipartPartURLTTL)
	expires := expiresAt.Unix()
	signature := a.localUploadPartSignature(sessionID, partNumber, expires)
	partURL := fmt.Sprintf("%s/api/v1/files/upload-sessions/%s/parts/%d", strings.TrimRight(a.cfg.HTTP.PublicURL, "/"), url.PathEscape(sessionID), partNumber)
	query := url.Values{}
	query.Set("expires", strconv.FormatInt(expires, 10))
	query.Set("signature", signature)
	return frameworkstorage.SignedRequest{
		URL: partURL + "?" + query.Encode(), Method: http.MethodPut,
		Headers:   map[string]string{"Content-Type": frameworkstorage.StoredContentType, "Content-Length": strconv.FormatInt(size, 10)},
		ExpiresAt: expiresAt,
	}
}

func (a *App) verifyLocalUploadPartSignature(c *gin.Context, sessionID string, partNumber int) bool {
	expires, err := strconv.ParseInt(c.Query("expires"), 10, 64)
	if err != nil || expires <= time.Now().UTC().Unix() {
		writeUploadSessionProblem(c, http.StatusGone, "UPLOAD_PART_AUTHORIZATION_EXPIRED", "Part upload authorization has expired", "Sign the part again and retry.")
		return false
	}
	expected := a.localUploadPartSignature(sessionID, partNumber, expires)
	provided := c.Query("signature")
	if subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		writeUploadSessionProblem(c, http.StatusForbidden, "UPLOAD_PART_AUTHORIZATION_INVALID", "Part upload authorization is invalid", "Sign the part again and retry.")
		return false
	}
	return true
}

func (a *App) localUploadPartSignature(sessionID string, partNumber int, expires int64) string {
	mac := hmac.New(sha256.New, []byte(a.cfg.Session.Secret))
	_, _ = fmt.Fprintf(mac, "aginex:local-upload-part:v1\n%s\n%d\n%d", sessionID, partNumber, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) beginUploadSessionCompletion(c *gin.Context, sessionID string) error {
	principal := currentPrincipal(c)
	return a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		file, session, err := lockUploadSession(tx, sessionID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", file); err != nil {
			return frameworkaudit.Event{}, err
		}
		if file.Status != "pending" {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		if session.Status != domain.FileUploadSessionStatusActive {
			if session.Status == domain.FileUploadSessionStatusCompleting || session.Status == domain.FileUploadSessionStatusVerifying {
				return frameworkaudit.Event{}, errUploadSessionTransitioned
			}
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		before := uploadSessionAuditFields(file, session, 0)
		session.Status = domain.FileUploadSessionStatusCompleting
		session.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&session).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "files:begin-resumable-completion", "file-upload-session", session.ID, "Started resumable upload completion", before, uploadSessionAuditFields(file, session, 0)), nil
	})
}

func (a *App) markUploadSessionVerifying(c *gin.Context, sessionID string) error {
	principal := currentPrincipal(c)
	return a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		file, session, err := lockUploadSession(tx, sessionID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", file); err != nil {
			return frameworkaudit.Event{}, err
		}
		if file.Status != "pending" {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		if session.Status == domain.FileUploadSessionStatusVerifying {
			return frameworkaudit.Event{}, errUploadSessionTransitioned
		}
		// A stale-session reconciler may have returned completing -> active
		// after its inventory read while this request conclusively completed
		// the provider object. The final-object owner is allowed to recover
		// that narrow race by advancing active directly to verifying.
		if session.Status != domain.FileUploadSessionStatusCompleting && session.Status != domain.FileUploadSessionStatusActive {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		before := uploadSessionAuditFields(file, session, 0)
		session.Status = domain.FileUploadSessionStatusVerifying
		session.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&session).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "files:verify-resumable", "file-upload-session", session.ID, "Started final file verification", before, uploadSessionAuditFields(file, session, 0)), nil
	})
}

func (a *App) resetUploadSessionAfterCompletionMismatch(c *gin.Context, sessionID string) error {
	principal := currentPrincipal(c)
	return a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		file, session, err := lockUploadSession(tx, sessionID)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := checkFileAuthorization(c, "files:create", file); err != nil {
			return frameworkaudit.Event{}, err
		}
		if file.Status != "pending" {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		if session.Status == domain.FileUploadSessionStatusActive {
			return frameworkaudit.Event{}, errUploadSessionTransitioned
		}
		if session.Status != domain.FileUploadSessionStatusCompleting {
			return frameworkaudit.Event{}, errUploadSessionChanged
		}
		before := uploadSessionAuditFields(file, session, 0)
		session.Status = domain.FileUploadSessionStatusActive
		session.UpdatedAt = time.Now().UTC()
		if err := tx.Save(&session).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "files:resume-resumable-after-mismatch", "file-upload-session", session.ID, "Returned resumable upload to active after provider part mismatch", before, uploadSessionAuditFields(file, session, 0)), nil
	})
}

func (a *App) persistedCompletionManifest(ctx context.Context, file domain.FileObject, session domain.FileUploadSession) ([]frameworkstorage.UploadedPart, error) {
	var persisted []domain.FileUploadPart
	if err := a.db.WithContext(ctx).Where("session_id = ?", session.ID).Order("part_number").Find(&persisted).Error; err != nil {
		return nil, err
	}
	if len(persisted) != session.PartCount {
		return nil, errUploadSessionChanged
	}
	manifest := make([]frameworkstorage.UploadedPart, 0, len(persisted))
	for index, part := range persisted {
		number := index + 1
		expected, err := expectedUploadPartSize(file.Size, session, number)
		if err != nil || part.PartNumber != number || part.Size != expected || strings.TrimSpace(part.ETag) == "" {
			return nil, errUploadSessionChanged
		}
		manifest = append(manifest, frameworkstorage.UploadedPart{PartNumber: int32(part.PartNumber), Size: part.Size, ETag: part.ETag})
	}
	return manifest, nil
}

func multipartManifestMatches(expected, actual []frameworkstorage.UploadedPart) bool {
	if len(expected) != len(actual) {
		return false
	}
	sort.Slice(actual, func(i, j int) bool { return actual[i].PartNumber < actual[j].PartNumber })
	for index := range expected {
		if expected[index].PartNumber != actual[index].PartNumber || expected[index].Size != actual[index].Size || normalizeOpaqueETag(expected[index].ETag) != normalizeOpaqueETag(actual[index].ETag) {
			return false
		}
	}
	return true
}

func lockUploadSession(tx *gorm.DB, sessionID string) (domain.FileObject, domain.FileUploadSession, error) {
	var session domain.FileUploadSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ?", sessionID).Error; err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}
	var file domain.FileObject
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, "id = ?", session.FileID).Error; err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}
	return file, session, nil
}

func completionMayProceed(session domain.FileUploadSession) bool {
	return session.Status == domain.FileUploadSessionStatusActive || session.Status == domain.FileUploadSessionStatusCompleting || session.Status == domain.FileUploadSessionStatusVerifying
}

func incompleteUploadSessionStatuses() []domain.FileUploadSessionStatus {
	return []domain.FileUploadSessionStatus{
		domain.FileUploadSessionStatusActive,
		domain.FileUploadSessionStatusCompleting,
		domain.FileUploadSessionStatusVerifying,
		domain.FileUploadSessionStatusCancelling,
		domain.FileUploadSessionStatusExpiring,
	}
}

func uploadSessionAuditFields(file domain.FileObject, session domain.FileUploadSession, completedParts int) map[string]any {
	return map[string]any{
		"id": session.ID, "fileId": file.ID, "status": session.Status,
		"partSize": session.PartSize, "partCount": session.PartCount,
		"completedParts": completedParts, "expiresAt": session.ExpiresAt,
	}
}

func writeUploadSessionStateProblem(c *gin.Context, session domain.FileUploadSession) {
	if session.Status == domain.FileUploadSessionStatusExpired || (session.Status == domain.FileUploadSessionStatusActive && !session.ExpiresAt.After(time.Now().UTC())) {
		writeUploadSessionProblem(c, http.StatusGone, "UPLOAD_SESSION_EXPIRED", "Upload session has expired", "Create a new upload intent.")
		return
	}
	writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_SESSION_STATE_CONFLICT", "Upload session state has changed", "Reload the upload session before continuing.")
}

func writeUploadSessionWriteError(c *gin.Context, operation string, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFoundOrInternal(c, "Upload session", err)
		return
	}
	if isFileAuthorizationError(err) {
		writeFileAuthorizationProblem(c, err)
		return
	}
	if errors.Is(err, errUploadSessionChanged) || errors.Is(err, errUploadSessionTransitioned) {
		writeUploadSessionProblem(c, http.StatusConflict, "UPLOAD_SESSION_STATE_CONFLICT", "Upload session state has changed", "Reload the upload session before continuing.")
		return
	}
	logRequestFailure(c, operation, err)
	writeProblem(c, http.StatusInternalServerError, "Upload session could not be updated", "The upload session write could not be committed.")
}

func writeUploadSessionProblem(c *gin.Context, status int, code, title, detail string) {
	httpx.WriteProblem(c, status, code, title, detail)
}

func safeOriginalFilename(filename string) string {
	filename = strings.ReplaceAll(filename, "\\", "/")
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "download"
	}
	segments := strings.Split(filename, "/")
	result := strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, segments[len(segments)-1])
	result = strings.TrimSpace(result)
	if result == "" || result == "." || result == ".." {
		return "download"
	}
	return result
}

func pointerTo[T any](value T) *T {
	return &value
}
