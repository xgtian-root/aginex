package app

import (
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/xgtian/aginex/internal/domain"
	"github.com/xgtian/aginex/internal/platform/storage"
)

type uploadIntentInput struct {
	Filename    string `json:"filename" binding:"required,max=500"`
	ContentType string `json:"contentType" binding:"required"`
	Size        int64  `json:"size" binding:"required,min=1"`
	Visibility  string `json:"visibility" binding:"required,oneof=private public"`
}

func (a *App) createUploadIntent(c *gin.Context) {
	var input uploadIntentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeProblem(c, http.StatusBadRequest, "Invalid upload request", err.Error())
		return
	}
	extension := extensionFor(input.ContentType)
	if extension == "" {
		writeProblem(c, http.StatusUnsupportedMediaType, "Image type is not allowed", "Use a JPEG, PNG, or WebP image.")
		return
	}
	now := time.Now().UTC()
	key := "uploads/" + now.Format("2006/01") + "/" + uuid.NewString() + extension
	signed, err := a.store.CreateUpload(c.Request.Context(), storage.UploadRequest{
		Key: key, ContentType: input.ContentType, Size: input.Size, Expires: 10 * time.Minute,
	})
	if err != nil {
		writeProblem(c, http.StatusBadRequest, "Upload could not be prepared", err.Error())
		return
	}
	principal := currentPrincipal(c)
	file := domain.FileObject{
		ID: uuid.NewString(), Provider: a.cfg.Storage.Driver, Bucket: a.cfg.Storage.Bucket,
		ObjectKey: key, OriginalName: path.Base(strings.ReplaceAll(input.Filename, "\\", "/")), ContentType: input.ContentType,
		Size: input.Size, OwnerID: principal.User.ID, Visibility: input.Visibility, Status: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := a.db.Create(&file).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Upload could not be prepared", err.Error())
		return
	}
	a.audit(c, &principal.User.ID, "files:create-intent", "file", file.ID, "Prepared "+file.OriginalName)
	c.JSON(http.StatusCreated, gin.H{"file": file, "upload": signed})
}

func (a *App) localUpload(c *gin.Context) {
	local, ok := a.store.(*storage.Local)
	if !ok {
		writeProblem(c, http.StatusNotFound, "Local upload is unavailable", "The active storage provider uses direct cloud uploads.")
		return
	}
	key := strings.TrimPrefix(c.Param("key"), "/")
	var file domain.FileObject
	if err := a.db.First(&file, "object_key = ? AND status = ?", key, "pending").Error; err != nil {
		notFoundOrInternal(c, "Upload intent", err)
		return
	}
	if c.GetHeader("Content-Type") != file.ContentType {
		writeProblem(c, http.StatusUnsupportedMediaType, "Content type does not match", "Use the content type declared by the upload intent.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, file.Size+1))
	if err != nil {
		writeProblem(c, http.StatusBadRequest, "Upload could not be read", err.Error())
		return
	}
	if int64(len(body)) != file.Size {
		writeProblem(c, http.StatusBadRequest, "File size does not match", "Upload the exact file declared by the upload intent.")
		return
	}
	if err := local.Put(key, body, file.ContentType); err != nil {
		writeProblem(c, http.StatusBadRequest, "Upload could not be stored", err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *App) confirmUpload(c *gin.Context) {
	var file domain.FileObject
	if err := a.db.First(&file, "id = ?", c.Param("id")).Error; err != nil {
		notFoundOrInternal(c, "File", err)
		return
	}
	principal := currentPrincipal(c)
	if file.OwnerID != principal.User.ID {
		writeProblem(c, http.StatusForbidden, "Permission denied", "Only the upload owner can confirm this file.")
		return
	}
	info, err := a.store.Stat(c.Request.Context(), file.ObjectKey)
	if err != nil {
		writeProblem(c, http.StatusConflict, "Uploaded object was not found", "Finish uploading the object, then confirm it again.")
		return
	}
	if info.Size != file.Size || info.ContentType != file.ContentType {
		writeProblem(c, http.StatusConflict, "Uploaded object does not match", "The stored size or content type differs from the upload intent.")
		return
	}
	file.Status = "ready"
	file.ETag = info.ETag
	file.UpdatedAt = time.Now().UTC()
	if err := a.db.Save(&file).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "File could not be confirmed", err.Error())
		return
	}
	a.audit(c, &principal.User.ID, "files:confirm", "file", file.ID, "Confirmed "+file.OriginalName)
	c.JSON(http.StatusOK, file)
}

func (a *App) listFiles(c *gin.Context) {
	page, pageSize := pagination(c)
	var total int64
	a.db.Model(&domain.FileObject{}).Count(&total)
	var files []domain.FileObject
	if err := a.db.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&files).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "Files unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": files, "page": page, "pageSize": pageSize, "total": total})
}

func (a *App) fileURL(c *gin.Context) {
	var file domain.FileObject
	if err := a.db.First(&file, "id = ? AND status = ?", c.Param("id"), "ready").Error; err != nil {
		notFoundOrInternal(c, "File", err)
		return
	}
	signed, err := a.store.SignRead(c.Request.Context(), file.ObjectKey, 5*time.Minute)
	if err != nil {
		writeProblem(c, http.StatusInternalServerError, "File URL unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, signed)
}

func (a *App) localContent(c *gin.Context) {
	local, ok := a.store.(*storage.Local)
	if !ok {
		writeProblem(c, http.StatusNotFound, "Local content is unavailable", "The active provider uses signed cloud URLs.")
		return
	}
	key := strings.TrimPrefix(c.Param("key"), "/")
	var metadata domain.FileObject
	if err := a.db.First(&metadata, "object_key = ? AND status = ?", key, "ready").Error; err != nil {
		notFoundOrInternal(c, "File", err)
		return
	}
	file, err := local.Open(key)
	if err != nil {
		writeProblem(c, http.StatusNotFound, "File content not found", err.Error())
		return
	}
	defer file.Close()
	c.Header("Content-Disposition", "inline; filename="+strconvQuote(metadata.OriginalName))
	c.DataFromReader(http.StatusOK, metadata.Size, metadata.ContentType, file, nil)
}

func (a *App) deleteFile(c *gin.Context) {
	var file domain.FileObject
	if err := a.db.First(&file, "id = ?", c.Param("id")).Error; err != nil {
		notFoundOrInternal(c, "File", err)
		return
	}
	if err := a.store.Delete(c.Request.Context(), file.ObjectKey); err != nil {
		a.db.Model(&file).Updates(map[string]any{"status": "delete_failed", "updated_at": time.Now().UTC()})
		writeProblem(c, http.StatusConflict, "File deletion will be retried", err.Error())
		return
	}
	if err := a.db.Delete(&file).Error; err != nil {
		writeProblem(c, http.StatusInternalServerError, "File metadata could not be deleted", err.Error())
		return
	}
	principal := currentPrincipal(c)
	a.audit(c, &principal.User.ID, "files:delete", "file", file.ID, "Deleted "+file.OriginalName)
	c.Status(http.StatusNoContent)
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
