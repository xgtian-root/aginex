package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
)

func (a *App) getFileUploadPolicy(c *gin.Context) {
	principal := currentPrincipal(c)
	if !authorizeFile(c, "files:create", domain.FileObject{
		ID: "upload-policy", OwnerID: principal.User.ID,
	}) {
		return
	}
	runtime := a.cfg.FileUploadRuntime()
	_, resumableAvailable := storage.AsMultipart(a.store)
	c.JSON(http.StatusOK, UploadPolicyResponse{
		MaxUploadBytes:          runtime.MaxUploadBytes,
		ResumableUploadsEnabled: runtime.ResumableUploadsEnabled,
		ResumableAvailable:      resumableAvailable,
		MultipartThresholdBytes: multipartThresholdBytes,
		MultipartPartSizeBytes:  multipartPartSizeBytes,
		SessionTTLSeconds:       multipartSessionSeconds,
		MaxBatchFiles:           maxUploadBatchFiles,
	})
}
