package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/framework/httpx"
	"github.com/xgtian-root/aginex/framework/module"
)

var errAuthorizationScopeUnused = errors.New(
	"authorization scope must be applied before a successful response",
)

// authorizationGuardWriter prevents an owner/custom handler from committing a
// successful response before it has used the object or query policy declared
// by its route. Error responses remain available for not-found and validation
// paths that intentionally run before an ownership lookup.
type authorizationGuardWriter struct {
	gin.ResponseWriter
	request       *http.Request
	authorization module.RequestAuthorization
	mode          module.AuthorizationMode
	denied        bool
}

func newAuthorizationGuardWriter(
	writer gin.ResponseWriter,
	request *http.Request,
	authorization module.RequestAuthorization,
	mode module.AuthorizationMode,
) *authorizationGuardWriter {
	return &authorizationGuardWriter{
		ResponseWriter: writer,
		request:        request,
		authorization:  authorization,
		mode:           mode,
	}
}

func (writer *authorizationGuardWriter) WriteHeader(status int) {
	if !writer.allowStatus(status) {
		return
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *authorizationGuardWriter) WriteHeaderNow() {
	if !writer.allowStatus(writer.ResponseWriter.Status()) {
		return
	}
	writer.ResponseWriter.WriteHeaderNow()
}

func (writer *authorizationGuardWriter) Write(value []byte) (int, error) {
	if writer.denied {
		return len(value), nil
	}
	status := writer.ResponseWriter.Status()
	if status == 0 {
		status = http.StatusOK
	}
	if !writer.allowStatus(status) {
		return len(value), nil
	}
	return writer.ResponseWriter.Write(value)
}

func (writer *authorizationGuardWriter) WriteString(
	value string,
) (int, error) {
	return writer.Write([]byte(value))
}

func (writer *authorizationGuardWriter) Flush() {
	status := writer.ResponseWriter.Status()
	if status == 0 {
		status = http.StatusOK
	}
	if !writer.allowStatus(status) {
		return
	}
	writer.ResponseWriter.Flush()
}

func (writer *authorizationGuardWriter) Hijack() (
	net.Conn,
	*bufio.ReadWriter,
	error,
) {
	if !writer.authorization.Satisfies(writer.mode) {
		writer.deny()
		return nil, nil, errAuthorizationScopeUnused
	}
	return writer.ResponseWriter.Hijack()
}

func (writer *authorizationGuardWriter) allowStatus(status int) bool {
	if writer.denied {
		return false
	}
	if writer.authorization.Violated() {
		writer.deny()
		return false
	}
	if writer.authorization.Satisfies(writer.mode) {
		return true
	}
	writer.deny()
	return false
}

func (writer *authorizationGuardWriter) deny() {
	if writer.denied {
		return
	}
	writer.denied = true
	headers := writer.ResponseWriter.Header()
	requestID := headers.Get("X-Request-ID")
	traceParent := headers.Get(httpx.TraceParentHeader)
	for name := range headers {
		delete(headers, name)
	}
	headers.Set("Content-Type", httpx.ProblemMediaType)
	headers.Set("Cache-Control", "no-store")
	if requestID != "" {
		headers.Set("X-Request-ID", requestID)
	}
	if traceParent != "" {
		headers.Set(httpx.TraceParentHeader, traceParent)
	}
	instance := ""
	if writer.request != nil && writer.request.URL != nil {
		instance = writer.request.URL.Path
	}
	payload, _ := json.Marshal(httpx.Problem{
		Type:      "about:blank",
		Title:     "Authorization scope was not applied",
		Status:    http.StatusInternalServerError,
		Detail:    "The registered handler did not apply its required object or query authorization.",
		Instance:  instance,
		Code:      "AUTHORIZATION_SCOPE_UNUSED",
		RequestID: requestID,
	})
	writer.ResponseWriter.WriteHeader(http.StatusInternalServerError)
	_, _ = writer.ResponseWriter.Write(payload)
}

func enforceAuthorizationUsage(
	mode module.AuthorizationMode,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		decision, ok := module.RequestAuthorizationFromContext(
			c.Request.Context(),
		)
		if !ok {
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authorization unavailable",
				"The admitted authorization decision is missing.",
			)
			c.Abort()
			return
		}
		requestContext, err := module.ContextWithRequestAuthorizationMode(
			c.Request.Context(),
			mode,
		)
		if err != nil {
			writeProblem(
				c,
				http.StatusInternalServerError,
				"Authorization unavailable",
				"The route authorization mode is invalid.",
			)
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(requestContext)

		originalWriter := c.Writer
		guard := newAuthorizationGuardWriter(
			originalWriter,
			c.Request,
			decision,
			mode,
		)
		c.Writer = guard
		defer func() {
			if !guard.Written() &&
				!decision.Satisfies(mode) {
				guard.deny()
			}
			c.Writer = originalWriter
		}()
		c.Next()
	}
}
