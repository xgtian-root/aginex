package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	frameworkidempotency "github.com/xgtian-root/aginex/server/framework/idempotency"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/framework/services"
	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/storage"
	"gorm.io/gorm"
)

const (
	idempotencyHeader         = "Idempotency-Key"
	idempotencyReplayedHeader = "Idempotency-Replayed"
	idempotencyStateKey       = "aginex.idempotency"
	jsonResponseContentType   = "application/json; charset=utf-8"
)

var errIdempotencyReplayUnauthorized = errors.New(
	"idempotency replay authorization was not satisfied",
)

type requestIdempotency struct {
	operationID string
	lease       frameworkidempotency.Lease
	completed   bool
}

func (a *App) enforceIdempotency(
	operationID string,
	route string,
	authorizationMode module.AuthorizationMode,
	replayAuthorizer module.ReplayAuthorizer,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		key, present, err := requestIdempotencyKey(c.Request.Header)
		if err != nil {
			writeIdempotencyProblem(
				c,
				http.StatusBadRequest,
				"IDEMPOTENCY_KEY_INVALID",
				"Invalid idempotency key",
				"Use a bounded Idempotency-Key containing only supported characters.",
			)
			c.Abort()
			return
		}
		if !present {
			c.Next()
			return
		}
		if a.idempotency == nil {
			writeIdempotencyProblem(
				c,
				http.StatusServiceUnavailable,
				"IDEMPOTENCY_UNAVAILABLE",
				"Idempotency is unavailable",
				"The server is not configured to honor Idempotency-Key.",
			)
			c.Abort()
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeBindingProblem(c, "Invalid idempotent request", err)
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		actor, ok := module.AuthenticatedActorFromContext(
			c.Request.Context(),
		)
		if !ok {
			writeIdempotencyProblem(
				c,
				http.StatusInternalServerError,
				"IDEMPOTENCY_ACTOR_UNAVAILABLE",
				"Idempotency actor is unavailable",
				"The authenticated actor was not attached to the request.",
			)
			c.Abort()
			return
		}
		result, err := a.idempotency.Claim(c.Request.Context(), frameworkidempotency.ClaimRequest{
			Actor: frameworkidempotency.Actor{
				Kind: frameworkidempotency.ActorKind(actor.Kind),
				ID:   actor.ID,
			},
			Method:        c.Request.Method,
			Route:         route,
			Key:           key,
			RequestDigest: requestDigest(c.Request, body, a.cfg.Session.Secret),
		})
		if err != nil {
			a.writeIdempotencyClaimError(c, err)
			c.Abort()
			return
		}

		switch result.Disposition {
		case frameworkidempotency.DispositionInProgress:
			retryAfter := int(math.Ceil(result.RetryAfter.Seconds()))
			if retryAfter < 1 {
				retryAfter = 1
			}
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
			writeIdempotencyProblem(
				c,
				http.StatusConflict,
				"IDEMPOTENCY_IN_PROGRESS",
				"Request is already in progress",
				"Retry this request after the active execution lease expires.",
			)
			c.Abort()
			return
		case frameworkidempotency.DispositionReplay:
			if result.Response == nil {
				writeIdempotencyProblem(
					c,
					http.StatusServiceUnavailable,
					"IDEMPOTENCY_UNAVAILABLE",
					"Idempotency replay is unavailable",
					"The stored response is incomplete.",
				)
				c.Abort()
				return
			}
			if err := authorizeIdempotencyReplay(
				c,
				authorizationMode,
				replayAuthorizer,
				*result.Response,
			); err != nil {
				a.writeIdempotencyReplayError(c, err)
				c.Abort()
				return
			}
			if err := a.replayIdempotentResponse(c, operationID, *result.Response); err != nil {
				a.writeIdempotencyReplayError(c, err)
			}
			c.Abort()
			return
		case frameworkidempotency.DispositionExecute:
		default:
			writeIdempotencyProblem(
				c,
				http.StatusServiceUnavailable,
				"IDEMPOTENCY_UNAVAILABLE",
				"Idempotency is unavailable",
				"The idempotency provider returned an unsupported state.",
			)
			c.Abort()
			return
		}

		state := &requestIdempotency{
			operationID: operationID,
			lease:       result.Lease,
		}
		c.Set(idempotencyStateKey, state)

		original := c.Writer
		buffered := newBufferedResponseWriter(original)
		c.Writer = buffered
		c.Next()
		c.Writer = original

		status := buffered.Status()
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			abandonErr := a.idempotency.Abandon(
				context.WithoutCancel(c.Request.Context()),
				state.lease,
			)
			if abandonErr != nil && status < http.StatusInternalServerError {
				writeIdempotencyProblem(
					c,
					http.StatusServiceUnavailable,
					"IDEMPOTENCY_UNAVAILABLE",
					"Idempotency is unavailable",
					"The failed request could not release its execution lease.",
				)
				return
			}
			buffered.flush(original)
			return
		}

		if buffered.overflow {
			_ = a.idempotency.Abandon(
				context.WithoutCancel(c.Request.Context()),
				state.lease,
			)
			writeIdempotencyProblem(
				c,
				http.StatusInternalServerError,
				"IDEMPOTENCY_RESPONSE_TOO_LARGE",
				"Response could not be retained",
				"The response exceeds the idempotency replay limit.",
			)
			return
		}

		if !state.completed {
			response, err := storedIdempotencyResponse(
				operationID,
				status,
				buffered.Header(),
				buffered.body.Bytes(),
			)
			if err == nil {
				_, err = a.idempotency.Complete(
					context.WithoutCancel(c.Request.Context()),
					state.lease,
					response,
				)
			}
			if err != nil {
				_ = a.idempotency.Abandon(
					context.WithoutCancel(c.Request.Context()),
					state.lease,
				)
				writeIdempotencyProblem(
					c,
					http.StatusInternalServerError,
					"IDEMPOTENCY_COMPLETION_FAILED",
					"Response could not be retained",
					"The idempotent operation could not be completed safely.",
				)
				return
			}
		}
		buffered.flush(original)
	}
}

func authorizeIdempotencyReplay(
	c *gin.Context,
	mode module.AuthorizationMode,
	authorizer module.ReplayAuthorizer,
	response frameworkidempotency.Response,
) error {
	decision, ok := module.RequestAuthorizationFromContext(
		c.Request.Context(),
	)
	if !ok {
		return errIdempotencyReplayUnauthorized
	}
	if mode == module.AuthorizationObject ||
		mode == module.AuthorizationQuery {
		if authorizer == nil {
			return errIdempotencyReplayUnauthorized
		}
		parameters := make(map[string]string, len(c.Params))
		for _, parameter := range c.Params {
			parameters[parameter.Key] = parameter.Value
		}
		if err := authorizer(
			c.Request.Context(),
			decision,
			module.NewIdempotencyReplay(
				parameters,
				response.Status,
				response.Body,
			),
		); err != nil {
			return err
		}
	}
	if !decision.Satisfies(mode) {
		return errIdempotencyReplayUnauthorized
	}
	return nil
}

func (a *App) completeIdempotentWrite(
	c *gin.Context,
	tx *gorm.DB,
	status int,
	body any,
	headers http.Header,
) error {
	state := currentIdempotency(c)
	if state == nil {
		return nil
	}
	encoded, err := marshalIdempotencyBody(body)
	if err != nil {
		return err
	}
	responseHeaders := make(http.Header)
	for name, values := range headers {
		responseHeaders[name] = append([]string(nil), values...)
	}
	if len(encoded) > 0 {
		responseHeaders.Set("Content-Type", jsonResponseContentType)
	}
	response, err := storedIdempotencyResponse(
		state.operationID,
		status,
		responseHeaders,
		encoded,
	)
	if err != nil {
		return err
	}
	transactional, err := a.idempotency.WithDB(tx)
	if err != nil {
		return err
	}
	completed, err := transactional.Complete(c.Request.Context(), state.lease, response)
	if err != nil {
		return err
	}
	if !completed.Stored {
		return errors.New("successful idempotency response was not stored")
	}
	state.completed = true
	return nil
}

func requestIdempotencyKey(header http.Header) (string, bool, error) {
	values, present := header[http.CanonicalHeaderKey(idempotencyHeader)]
	if !present {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", true, errors.New("Idempotency-Key must appear exactly once")
	}
	return values[0], true, nil
}

func requestDigest(request *http.Request, body []byte, secret string) string {
	// The digest is persisted for replay conflict detection. It must not become
	// a fast offline verifier for low-entropy fields (notably passwords), so use
	// a deployment secret instead of an unkeyed content hash. The first field
	// domain-separates this use from session token authentication.
	hash := hmac.New(sha256.New, []byte(secret))
	writeDigestField(hash, "aginex:idempotency-request:v1")
	writeDigestField(hash, request.URL.EscapedPath())
	writeDigestField(hash, request.URL.Query().Encode())
	writeDigestField(hash, strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Type"))))
	writeDigestBytes(hash, body)
	return hex.EncodeToString(hash.Sum(nil))
}

func writeDigestField(writer io.Writer, value string) {
	writeDigestBytes(writer, []byte(value))
}

func writeDigestBytes(writer io.Writer, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func currentIdempotency(c *gin.Context) *requestIdempotency {
	value, ok := c.Get(idempotencyStateKey)
	if !ok {
		return nil
	}
	state, _ := value.(*requestIdempotency)
	return state
}

func marshalIdempotencyBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode idempotency response: %w", err)
	}
	return encoded, nil
}

func storedIdempotencyResponse(
	operationID string,
	status int,
	header http.Header,
	body []byte,
) (frameworkidempotency.Response, error) {
	storedBody := bytes.Clone(body)
	if operationID == "createUploadIntent" && len(storedBody) > 0 {
		var response UploadIntentResponse
		if err := json.Unmarshal(storedBody, &response); err != nil {
			return frameworkidempotency.Response{}, fmt.Errorf(
				"decode upload intent replay: %w",
				err,
			)
		}
		response.Upload = nil
		var err error
		storedBody, err = json.Marshal(response)
		if err != nil {
			return frameworkidempotency.Response{}, fmt.Errorf(
				"sanitize upload intent replay: %w",
				err,
			)
		}
	}
	return frameworkidempotency.Response{
		Status:      status,
		ContentType: header.Get("Content-Type"),
		Headers:     replaySafeHeaders(header),
		Body:        storedBody,
	}, nil
}

func replaySafeHeaders(header http.Header) http.Header {
	result := make(http.Header)
	for _, name := range []string{
		"Cache-Control",
		"Content-Language",
		"Content-Location",
		"ETag",
		"Expires",
		"Last-Modified",
		"Location",
		"Preference-Applied",
		"Retry-After",
		"Vary",
	} {
		if values := header.Values(name); len(values) > 0 {
			result[name] = append([]string(nil), values...)
		}
	}
	return result
}

func (a *App) replayIdempotentResponse(
	c *gin.Context,
	operationID string,
	response frameworkidempotency.Response,
) error {
	if operationID == "createUploadIntent" {
		var cached UploadIntentResponse
		if err := json.Unmarshal(response.Body, &cached); err != nil {
			return fmt.Errorf("decode upload intent replay: %w", err)
		}
		var file domain.FileObject
		if err := a.db.WithContext(c.Request.Context()).
			First(&file, "id = ?", cached.File.ID).
			Error; err != nil {
			return err
		}
		if err := checkFileAuthorization(c, "files:create", file); err != nil {
			return err
		}
		if file.Status != "pending" {
			return errUploadIntentNotPending
		}
		if cached.Strategy == "resumable" {
			cached.File = a.fileResponse(file)
			encoded, encodeErr := json.Marshal(cached)
			if encodeErr != nil {
				return encodeErr
			}
			response.Body = encoded
			goto replay
		}
		if file.UploadExpiresAt == nil {
			return errUploadIntentExpired
		}
		fileStore, err := a.storeForFile(file)
		if err != nil {
			return err
		}
		remaining := file.UploadExpiresAt.UTC().Sub(time.Now().UTC()) - time.Second
		if remaining <= 0 {
			return errUploadIntentExpired
		}
		signed, err := fileStore.CreateUpload(c.Request.Context(), storage.UploadRequest{
			Key:          file.ObjectKey,
			ContentType:  frameworkstorage.StoredContentType,
			Size:         file.Size,
			Expires:      remaining,
			Continuation: true,
		})
		if err != nil {
			return err
		}
		signed, err = a.protectLocalSingleUpload(fileStore, file, signed)
		if err != nil {
			return err
		}
		cached.File = a.fileResponse(file)
		upload := signedRequestResponse(signed)
		cached.Upload = &upload
		encoded, err := json.Marshal(cached)
		if err != nil {
			return err
		}
		response.Body = encoded
	}

replay:
	for name, values := range response.Headers {
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Header(idempotencyReplayedHeader, "true")
	if len(response.Body) == 0 {
		c.Status(response.Status)
		return nil
	}
	c.Data(response.Status, response.ContentType, response.Body)
	return nil
}

var (
	errUploadIntentNotPending = errors.New("upload intent is no longer pending")
	errUploadIntentExpired    = errors.New("upload intent authorization has expired")
)

func (a *App) writeIdempotencyClaimError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, frameworkidempotency.ErrInvalidRequest):
		writeIdempotencyProblem(
			c,
			http.StatusBadRequest,
			"IDEMPOTENCY_KEY_INVALID",
			"Invalid idempotency key",
			"Use one bounded visible-ASCII Idempotency-Key value.",
		)
	case errors.Is(err, frameworkidempotency.ErrConflict):
		writeIdempotencyProblem(
			c,
			http.StatusConflict,
			"IDEMPOTENCY_KEY_REUSED",
			"Idempotency key was reused",
			"The key is already bound to a different request.",
		)
	default:
		writeIdempotencyProblem(
			c,
			http.StatusServiceUnavailable,
			"IDEMPOTENCY_UNAVAILABLE",
			"Idempotency is unavailable",
			"The idempotency provider could not claim this request.",
		)
	}
}

func (a *App) writeIdempotencyReplayError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, frameworkauthz.ErrUnauthenticated):
		writeIdempotencyProblem(
			c,
			http.StatusUnauthorized,
			"AUTHENTICATION_REQUIRED",
			"Authentication required",
			"Sign in to continue.",
		)
	case errors.Is(err, frameworkauthz.ErrForbidden),
		errors.Is(err, errIdempotencyReplayUnauthorized):
		writeIdempotencyProblem(
			c,
			http.StatusForbidden,
			"RESOURCE_FORBIDDEN",
			"Permission denied",
			"The stored response is not authorized for the current request.",
		)
	case errors.Is(err, errUploadIntentNotPending):
		writeIdempotencyProblem(
			c,
			http.StatusConflict,
			"UPLOAD_INTENT_NOT_PENDING",
			"Upload intent is no longer pending",
			"The original upload intent can no longer be replayed.",
		)
	case errors.Is(err, errUploadIntentExpired):
		writeIdempotencyProblem(
			c,
			http.StatusConflict,
			"UPLOAD_INTENT_EXPIRED",
			"Upload intent has expired",
			"Create a new upload intent before retrying the upload.",
		)
	case errors.Is(err, gorm.ErrRecordNotFound):
		writeIdempotencyProblem(
			c,
			http.StatusConflict,
			"IDEMPOTENCY_RESOURCE_MISSING",
			"Idempotent resource is unavailable",
			"The resource associated with this response no longer exists.",
		)
	default:
		writeIdempotencyProblem(
			c,
			http.StatusServiceUnavailable,
			"IDEMPOTENCY_REPLAY_FAILED",
			"Response replay is unavailable",
			"The stored response could not be reconstructed safely.",
		)
	}
}

func (a *App) reauthorizeFileIdempotencyReplay(
	ctx context.Context,
	operationID string,
	authorization module.RequestAuthorization,
	replay module.IdempotencyReplay,
) error {
	fileID := ""
	sessionID := ""
	switch operationID {
	case "createUploadIntent":
		var cached UploadIntentResponse
		if err := json.Unmarshal(replay.ResponseBody(), &cached); err != nil {
			return fmt.Errorf("decode upload intent replay authorization: %w", err)
		}
		fileID = cached.File.ID
	case "confirmUpload", "deleteFile":
		fileID = replay.PathParameter("id")
	case "resumeUploadSession", "ackUploadSessionParts", "completeUploadSession", "cancelUploadSession":
		sessionID = replay.PathParameter("id")
	default:
		return fmt.Errorf(
			"unsupported file replay authorization for %q",
			operationID,
		)
	}
	if strings.TrimSpace(fileID) == "" && strings.TrimSpace(sessionID) == "" {
		return gorm.ErrRecordNotFound
	}
	runtime, ok := services.RuntimeFromContext(ctx)
	if !ok || runtime.Database == nil {
		return errors.New("module query service is unavailable")
	}
	query, err := authorization.Scope(
		ctx,
		runtime.Database.Model(&domain.FileObject{}),
	)
	if err != nil {
		return err
	}
	var file domain.FileObject
	if sessionID != "" {
		return query.Where(
			"EXISTS (SELECT 1 FROM file_upload_sessions WHERE file_upload_sessions.file_id = file_objects.id AND file_upload_sessions.id = ?)",
			sessionID,
		).Take(&file).Error
	}
	return query.Where("id = ?", fileID).Take(&file).Error
}

func writeIdempotencyProblem(
	c *gin.Context,
	status int,
	code string,
	title string,
	detail string,
) {
	httpx.WriteProblem(c, status, code, title, detail)
}

type bufferedResponseWriter struct {
	underlying gin.ResponseWriter
	header     http.Header
	body       bytes.Buffer
	status     int
	written    bool
	overflow   bool
}

func newBufferedResponseWriter(underlying gin.ResponseWriter) *bufferedResponseWriter {
	return &bufferedResponseWriter{
		underlying: underlying,
		header:     underlying.Header().Clone(),
		status:     http.StatusOK,
	}
}

func (writer *bufferedResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *bufferedResponseWriter) WriteHeader(status int) {
	if writer.written {
		return
	}
	writer.status = status
	writer.written = true
}

func (writer *bufferedResponseWriter) WriteHeaderNow() {
	if !writer.written {
		writer.WriteHeader(writer.status)
	}
}

func (writer *bufferedResponseWriter) Write(value []byte) (int, error) {
	writer.WriteHeaderNow()
	if writer.body.Len()+len(value) > frameworkidempotency.MaxResponseBodyBytes {
		writer.overflow = true
		remaining := frameworkidempotency.MaxResponseBodyBytes + 1 - writer.body.Len()
		if remaining > 0 {
			_, _ = writer.body.Write(value[:min(remaining, len(value))])
		}
		return len(value), nil
	}
	return writer.body.Write(value)
}

func (writer *bufferedResponseWriter) WriteString(value string) (int, error) {
	return writer.Write([]byte(value))
}

func (writer *bufferedResponseWriter) Status() int {
	return writer.status
}

func (writer *bufferedResponseWriter) Size() int {
	if !writer.written {
		return -1
	}
	return writer.body.Len()
}

func (writer *bufferedResponseWriter) Written() bool {
	return writer.written
}

func (writer *bufferedResponseWriter) Flush() {
	writer.WriteHeaderNow()
}

func (writer *bufferedResponseWriter) CloseNotify() <-chan bool {
	return writer.underlying.CloseNotify()
}

func (writer *bufferedResponseWriter) Hijack() (
	net.Conn,
	*bufio.ReadWriter,
	error,
) {
	return nil, nil, errors.New("hijacking is unavailable for idempotent responses")
}

func (writer *bufferedResponseWriter) Pusher() http.Pusher {
	return writer.underlying.Pusher()
}

func (writer *bufferedResponseWriter) flush(destination gin.ResponseWriter) {
	target := destination.Header()
	for name := range target {
		delete(target, name)
	}
	for name, values := range writer.header {
		target[name] = append([]string(nil), values...)
	}
	destination.WriteHeader(writer.status)
	if writer.body.Len() > 0 {
		_, _ = destination.Write(writer.body.Bytes())
	}
}
