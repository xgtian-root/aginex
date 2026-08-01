package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const ProblemMediaType = "application/problem+json"

// Problem is Aginex's RFC 9457-compatible error envelope. Code is stable for
// machines while title and detail remain human-readable.
type Problem struct {
	Type      string         `json:"type" format:"uri"`
	Title     string         `json:"title"`
	Status    int            `json:"status"`
	Detail    string         `json:"detail"`
	Instance  string         `json:"instance" format:"uri-reference"`
	Code      string         `json:"code"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

func WriteProblem(
	c *gin.Context,
	status int,
	code string,
	title string,
	detail string,
) {
	if code == "" {
		code = DefaultProblemCode(status)
	}
	c.Header("Content-Type", ProblemMediaType)
	c.JSON(status, Problem{
		Type:      "about:blank",
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  c.Request.URL.Path,
		Code:      code,
		RequestID: c.Writer.Header().Get("X-Request-ID"),
	})
}

func AbortProblem(
	c *gin.Context,
	status int,
	code string,
	title string,
	detail string,
) {
	WriteProblem(c, status, code, title, detail)
	c.Abort()
}

func DefaultProblemCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "REQUEST_INVALID"
	case http.StatusUnauthorized:
		return "AUTHENTICATION_REQUIRED"
	case http.StatusForbidden:
		return "RESOURCE_FORBIDDEN"
	case http.StatusNotFound:
		return "RESOURCE_NOT_FOUND"
	case http.StatusConflict:
		return "REQUEST_CONFLICT"
	case http.StatusRequestEntityTooLarge:
		return "REQUEST_TOO_LARGE"
	case http.StatusUnsupportedMediaType:
		return "UNSUPPORTED_MEDIA_TYPE"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	case http.StatusRequestHeaderFieldsTooLarge:
		return "HEADERS_TOO_LARGE"
	default:
		if status >= 500 {
			return "INTERNAL_ERROR"
		}
		return "REQUEST_FAILED"
	}
}
