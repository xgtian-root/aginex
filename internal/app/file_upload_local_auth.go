package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
	"github.com/xgtian-root/aginex/internal/domain"
	platformstorage "github.com/xgtian-root/aginex/internal/platform/storage"
)

// protectLocalSingleUpload turns the authenticated Local PUT route into the
// same bounded credential represented by cloud presigned requests. The query
// values are capabilities only; they are never persisted in idempotency state.
func (a *App) protectLocalSingleUpload(
	store platformstorage.Storage,
	file domain.FileObject,
	signed frameworkstorage.SignedRequest,
) (frameworkstorage.SignedRequest, error) {
	if _, local := platformstorage.AsLocal(store); !local {
		return signed, nil
	}
	expires := signed.ExpiresAt.UTC().Unix()
	if expires <= time.Now().UTC().Unix() {
		return frameworkstorage.SignedRequest{}, fmt.Errorf("local upload authorization expiry is invalid")
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		return frameworkstorage.SignedRequest{}, err
	}
	query := parsed.Query()
	query.Set("expires", strconv.FormatInt(expires, 10))
	query.Set("signature", a.localSingleUploadSignature(file, expires))
	parsed.RawQuery = query.Encode()
	signed.URL = parsed.String()
	return signed, nil
}

func (a *App) verifyLocalSingleUpload(c *gin.Context, file domain.FileObject) bool {
	expires, err := strconv.ParseInt(c.Query("expires"), 10, 64)
	if err != nil || expires <= time.Now().UTC().Unix() {
		writeUploadSessionProblem(
			c,
			http.StatusGone,
			"UPLOAD_AUTHORIZATION_EXPIRED",
			"Upload authorization has expired",
			"Create or replay the upload intent and retry with its new URL.",
		)
		return false
	}
	if file.UploadExpiresAt == nil || expires > file.UploadExpiresAt.UTC().Unix() {
		writeUploadSessionProblem(
			c,
			http.StatusForbidden,
			"UPLOAD_AUTHORIZATION_INVALID",
			"Upload authorization is invalid",
			"Create a new upload intent and retry with its signed URL.",
		)
		return false
	}
	expected := a.localSingleUploadSignature(file, expires)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(c.Query("signature"))) != 1 {
		writeUploadSessionProblem(
			c,
			http.StatusForbidden,
			"UPLOAD_AUTHORIZATION_INVALID",
			"Upload authorization is invalid",
			"Create or replay the upload intent and retry with its signed URL.",
		)
		return false
	}
	return true
}

func (a *App) localSingleUploadSignature(file domain.FileObject, expires int64) string {
	mac := hmac.New(sha256.New, []byte(a.cfg.Session.Secret))
	_, _ = fmt.Fprintf(
		mac,
		"aginex:local-single-upload:v1\n%s\n%s\n%d\n%d",
		file.ID,
		file.ObjectKey,
		file.Size,
		expires,
	)
	return hex.EncodeToString(mac.Sum(nil))
}
