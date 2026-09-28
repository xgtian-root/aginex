package app

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	frameworkaudit "github.com/xgtian-root/aginex/server/framework/audit"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	"github.com/xgtian-root/aginex/server/internal/auth"
	"gorm.io/gorm"
)

func (a *App) createCaptcha(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var challenge auth.CaptchaChallenge
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var removed int64
		var err error
		challenge, removed, err = a.captcha.IssueTx(tx, c.GetHeader(a.cfg.Session.CSRFHeader))
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, nil, "auth:captcha-issue", "login-captcha", challenge.ID,
			"Issued login captcha and cleaned expired challenges", nil, map[string]any{"expiredRemoved": removed}), nil
	})
	if err != nil {
		logRequestFailure(c, "create-captcha", err)
		httpx.WriteProblem(c, http.StatusServiceUnavailable, "CAPTCHA_UNAVAILABLE", "Captcha unavailable", "The captcha could not be created. Please try again.")
		return
	}
	c.JSON(http.StatusOK, CaptchaResponse{CaptchaID: challenge.ID, Image: challenge.Image, ExpiresAt: challenge.ExpiresAt})
}

func (a *App) consumeCaptcha(c *gin.Context, input LoginRequest) bool {
	var valid bool
	err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var err error
		valid, err = a.captcha.ConsumeTx(tx, input.CaptchaID, input.CaptchaCode, c.GetHeader(a.cfg.Session.CSRFHeader))
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, nil, "auth:captcha-consume", "login-captcha", input.CaptchaID,
			"Consumed login captcha", nil, map[string]any{"verified": valid}), nil
	})
	if errors.Is(err, auth.ErrInvalidCaptcha) || (err == nil && !valid) {
		httpx.WriteProblem(c, http.StatusBadRequest, "CAPTCHA_INVALID", "Captcha invalid", "The captcha is incorrect or expired. Please try a new image.")
		return false
	}
	if err != nil {
		logRequestFailure(c, "consume-captcha", err)
		httpx.WriteProblem(c, http.StatusServiceUnavailable, "CAPTCHA_UNAVAILABLE", "Captcha unavailable", "The captcha could not be verified. Please try again.")
		return false
	}
	return true
}
