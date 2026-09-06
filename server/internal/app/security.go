package app

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/server/framework/httpx"
)

func (a *App) csrfToken(c *gin.Context) {
	token, err := httpx.ReuseOrGenerateCSRFToken(
		c.Request,
		a.cfg.Session.CSRFCookie,
	)
	if err != nil {
		httpx.WriteProblem(
			c,
			http.StatusInternalServerError,
			"CSRF_TOKEN_UNAVAILABLE",
			"CSRF token unavailable",
			"A request token could not be generated.",
		)
		return
	}
	expiresAt := time.Now().Add(a.cfg.Session.TTL)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     a.cfg.Session.CSRFCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(a.cfg.Session.TTL.Seconds()),
		Expires:  expiresAt,
		HttpOnly: false,
		Secure:   a.cfg.Session.Secure,
		SameSite: a.cfg.Session.SameSite,
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, CSRFTokenResponse{
		Token:      token,
		HeaderName: a.cfg.Session.CSRFHeader,
	})
}
