package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/xgtian-root/aginex/framework/module"
)

type operationResponseWriter struct {
	*bufferedResponseWriter
	contract       module.APIContract
	streamBinary   bool
	streaming      bool
	precommitError error
}

func newOperationResponseWriter(
	underlying gin.ResponseWriter,
	contract module.APIContract,
) *operationResponseWriter {
	responseType := contract.ResponseDTO
	return &operationResponseWriter{
		bufferedResponseWriter: newBufferedResponseWriter(underlying),
		contract:               contract,
		streamBinary: responseType != nil &&
			normalizedDTOType(responseType) == reflect.TypeFor[[]byte](),
	}
}

func (writer *operationResponseWriter) WriteHeader(status int) {
	if writer.written {
		return
	}
	writer.bufferedResponseWriter.WriteHeader(status)
}

func (writer *operationResponseWriter) WriteHeaderNow() {
	if !writer.written {
		writer.WriteHeader(writer.status)
	}
}

func (writer *operationResponseWriter) Write(value []byte) (int, error) {
	writer.WriteHeaderNow()
	writer.prepareBinaryStream()
	if writer.streaming {
		return writer.underlying.Write(value)
	}
	if writer.precommitError != nil {
		return len(value), nil
	}
	return writer.bufferedResponseWriter.Write(value)
}

func (writer *operationResponseWriter) WriteString(
	value string,
) (int, error) {
	return writer.Write([]byte(value))
}

func (writer *operationResponseWriter) Size() int {
	if writer.streaming {
		return writer.underlying.Size()
	}
	return writer.bufferedResponseWriter.Size()
}

func (writer *operationResponseWriter) Flush() {
	writer.WriteHeaderNow()
	writer.prepareBinaryStream()
	if writer.streaming {
		writer.underlying.Flush()
	}
}

func (writer *operationResponseWriter) Hijack() (
	net.Conn,
	*bufio.ReadWriter,
	error,
) {
	return nil, nil, errors.New(
		"hijacking is unavailable for contract-validated responses",
	)
}

func (writer *operationResponseWriter) commitStreamingHeader() {
	target := writer.underlying.Header()
	for name := range target {
		delete(target, name)
	}
	for name, values := range writer.header {
		target[name] = append([]string(nil), values...)
	}
	writer.underlying.WriteHeader(writer.status)
	writer.streaming = true
}

func (writer *operationResponseWriter) prepareBinaryStream() {
	if writer.streaming ||
		writer.precommitError != nil ||
		!writer.streamBinary ||
		writer.status < http.StatusOK ||
		writer.status >= http.StatusMultipleChoices {
		return
	}
	if err := validateContractSuccessMetadata(
		writer.bufferedResponseWriter,
		writer.contract,
	); err != nil {
		writer.precommitError = err
		return
	}
	writer.commitStreamingHeader()
}

func (a *App) enforceOperationResponseContract(
	operationID string,
) gin.HandlerFunc {
	contract := a.effectiveOperationContract(operationID)
	return func(c *gin.Context) {
		originalWriter := c.Writer
		response := newOperationResponseWriter(originalWriter, contract)
		c.Writer = response
		defer func() {
			c.Writer = originalWriter
		}()

		c.Next()
		c.Writer = originalWriter
		response.prepareBinaryStream()
		if response.streaming {
			return
		}
		if response.precommitError != nil {
			writeResponseContractViolation(c, response.precommitError)
			return
		}
		if err := validateContractResponse(
			response.bufferedResponseWriter,
			contract,
			c.Request,
		); err != nil {
			writeResponseContractViolation(c, err)
			return
		}
		response.flush(originalWriter)
	}
}

func (a *App) validateOperationContract(
	operationID string,
) gin.HandlerFunc {
	contract := a.effectiveOperationContract(operationID)
	return func(c *gin.Context) {
		if err := validateContractParameters(c, contract.Parameters); err != nil {
			writeProblem(
				c,
				http.StatusBadRequest,
				"Invalid request parameters",
				"One or more request parameters are invalid.",
			)
			c.Abort()
			return
		}
		if contract.RequestDTO != nil {
			decoded, status, err := decodeContractBody(c, contract)
			if err != nil {
				title := "Invalid request body"
				if status == http.StatusUnsupportedMediaType {
					title = "Unsupported media type"
				}
				if status == http.StatusRequestEntityTooLarge {
					title = "Request body is too large"
				}
				detail := "The request body is invalid."
				if status == http.StatusUnsupportedMediaType {
					detail = "Use the media type declared by this operation."
				}
				if status == http.StatusRequestEntityTooLarge {
					detail = "The request body exceeds the configured limit."
				}
				writeProblem(c, status, title, detail)
				c.Abort()
				return
			}
			c.Request = c.Request.WithContext(
				module.ContextWithValidatedRequestDTO(
					c.Request.Context(),
					decoded,
				),
			)
		}
		if !shouldValidateContractResponse(contract) {
			c.Next()
			return
		}
		originalWriter := c.Writer
		buffered := newBufferedResponseWriter(originalWriter)
		c.Writer = buffered
		defer func() {
			c.Writer = originalWriter
		}()
		c.Next()
		c.Writer = originalWriter
		if err := validateContractResponse(
			buffered,
			contract,
			c.Request,
		); err != nil {
			writeResponseContractViolation(c, err)
			return
		}
		buffered.flush(originalWriter)
	}
}

func (a *App) effectiveOperationContract(
	operationID string,
) module.APIContract {
	contract, ok := a.registry.APIContract(operationID)
	if !ok {
		panic("validated runtime operation has no API contract: " + operationID)
	}
	operation, ok := a.operations[operationID]
	if !ok {
		panic("validated runtime operation has no operation definition: " + operationID)
	}
	return effectiveAPIContract(contract, operation)
}

// effectiveAPIContract adds only errors emitted by framework middleware that
// the operation explicitly enables. API handlers and module middleware remain
// responsible for declaring every business error in APIContract.ErrorStatuses.
func effectiveAPIContract(
	contract module.APIContract,
	operation module.OperationDefinition,
) module.APIContract {
	if operation.Idempotency.Enabled() {
		contract.ErrorStatuses = appendUniqueStatuses(
			contract.ErrorStatuses,
			http.StatusBadRequest,
			http.StatusConflict,
			http.StatusServiceUnavailable,
		)
	}
	if operation.RateLimit.Enabled() {
		contract.ErrorStatuses = appendUniqueStatuses(
			contract.ErrorStatuses,
			http.StatusTooManyRequests,
			http.StatusServiceUnavailable,
		)
	}
	return contract
}

func writeResponseContractViolation(c *gin.Context, err error) {
	logRequestFailure(c, "validate_response_contract", err)
	writeProblem(
		c,
		http.StatusInternalServerError,
		"Response contract violated",
		"The operation returned an invalid response.",
	)
	c.Abort()
}

func validatedRequestDTO[T any](c *gin.Context) (T, bool) {
	value, ok := module.ValidatedRequestDTOFromContext[T](
		c.Request.Context(),
	)
	if ok {
		return value, true
	}
	var zero T
	writeProblem(
		c,
		http.StatusInternalServerError,
		"Request contract unavailable",
		"The validated request DTO is missing.",
	)
	c.Abort()
	return zero, false
}

func shouldValidateContractResponse(
	contract module.APIContract,
) bool {
	return contract.ResponseDTO == nil ||
		normalizedDTOType(contract.ResponseDTO) != reflect.TypeFor[[]byte]()
}

func validateContractResponse(
	response *bufferedResponseWriter,
	contract module.APIContract,
	request *http.Request,
) error {
	status := response.Status()
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return validateContractProblemResponse(
			response,
			contract,
			request,
		)
	}
	if response.overflow {
		return errors.New(
			"Response body exceeds the contract validation limit.",
		)
	}
	if err := validateContractSuccessMetadata(response, contract); err != nil {
		return err
	}
	if contract.ResponseDTO == nil {
		if response.body.Len() != 0 {
			return errors.New(
				"Response body was returned for a bodyless API contract.",
			)
		}
		return nil
	}
	expectedMediaType := contract.ResponseMediaType
	if expectedMediaType == "" {
		expectedMediaType = jsonMediaType
	}
	actualMediaType, _, err := mime.ParseMediaType(
		response.Header().Get("Content-Type"),
	)
	if err != nil ||
		!mediaTypeMatches(actualMediaType, expectedMediaType) ||
		(actualMediaType != jsonMediaType &&
			!strings.HasSuffix(actualMediaType, "+json")) {
		return errors.New(
			"Response Content-Type does not match the registered API contract.",
		)
	}
	baseType := normalizedDTOType(contract.ResponseDTO)
	target := reflect.New(baseType)
	decoder := json.NewDecoder(bytes.NewReader(response.body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target.Interface()); err != nil {
		return errors.New(
			"Response body does not match the registered DTO.",
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return errors.New(
			"Response body must contain exactly one JSON value.",
		)
	}
	return nil
}

func validateContractSuccessMetadata(
	response *bufferedResponseWriter,
	contract module.APIContract,
) error {
	if response.Status() != contract.SuccessStatus {
		return errors.New(
			"Successful response status does not match the registered API contract.",
		)
	}
	if contract.ResponseDTO == nil {
		return nil
	}
	expectedMediaType := contract.ResponseMediaType
	if expectedMediaType == "" {
		expectedMediaType = jsonMediaType
	}
	actualMediaType, _, err := mime.ParseMediaType(
		response.Header().Get("Content-Type"),
	)
	if err != nil || !mediaTypeMatches(actualMediaType, expectedMediaType) {
		return errors.New(
			"Response Content-Type does not match the registered API contract.",
		)
	}
	return nil
}

func validateContractProblemResponse(
	response *bufferedResponseWriter,
	contract module.APIContract,
	request *http.Request,
) error {
	status := response.Status()
	declared := false
	for _, candidate := range contract.ErrorStatuses {
		if candidate == status {
			declared = true
			break
		}
	}
	if !declared {
		return errors.New(
			"Error response status is not declared by the registered API contract.",
		)
	}
	if response.overflow {
		return errors.New(
			"Error response body exceeds the contract validation limit.",
		)
	}
	actualMediaType, _, err := mime.ParseMediaType(
		response.Header().Get("Content-Type"),
	)
	if err != nil || actualMediaType != problemMediaType {
		return errors.New(
			"Error response Content-Type must be application/problem+json.",
		)
	}

	body := response.body.Bytes()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return errors.New(
			"Error response body is not a valid Problem document.",
		)
	}
	for _, required := range []string{
		"type",
		"title",
		"status",
		"detail",
		"instance",
		"code",
		"requestId",
	} {
		if _, ok := fields[required]; !ok {
			return errors.New(
				"Error response body is missing a required Problem field.",
			)
		}
	}

	var problem Problem
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&problem); err != nil {
		return errors.New(
			"Error response body does not match the registered Problem schema.",
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return errors.New(
			"Error response body must contain exactly one JSON value.",
		)
	}
	if problem.Status != status {
		return errors.New(
			"Problem status does not match the HTTP response status.",
		)
	}
	if strings.TrimSpace(problem.Title) == "" ||
		strings.TrimSpace(problem.Code) == "" ||
		strings.TrimSpace(problem.RequestID) == "" {
		return errors.New(
			"Problem title, code, and requestId must be non-empty.",
		)
	}
	problemType, err := url.Parse(problem.Type)
	if err != nil || !problemType.IsAbs() {
		return errors.New(
			"Problem type must be an absolute URI.",
		)
	}
	if strings.TrimSpace(problem.Instance) == "" {
		return errors.New(
			"Problem instance must be a non-empty URI reference.",
		)
	}
	if _, err := url.Parse(problem.Instance); err != nil {
		return errors.New(
			"Problem instance must be a valid URI reference.",
		)
	}
	if request != nil && request.URL != nil &&
		problem.Instance != request.URL.Path {
		return errors.New(
			"Problem instance does not identify the current request.",
		)
	}
	if expected := response.Header().Get("X-Request-ID"); expected == "" ||
		problem.RequestID != expected {
		return errors.New(
			"Problem requestId does not match the response request ID.",
		)
	}
	return nil
}

func decodeContractBody(
	c *gin.Context,
	contract module.APIContract,
) (any, int, error) {
	expectedMediaType := contract.RequestMediaType
	if expectedMediaType == "" {
		expectedMediaType = jsonMediaType
	}
	actualMediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || !mediaTypeMatches(actualMediaType, expectedMediaType) {
		return nil, http.StatusUnsupportedMediaType, errors.New(
			"Content-Type does not match the registered API contract.",
		)
	}
	if normalizedDTOType(contract.RequestDTO) == reflect.TypeFor[[]byte]() {
		return []byte(nil), 0, nil
	}
	if actualMediaType != jsonMediaType &&
		!strings.HasSuffix(actualMediaType, "+json") {
		return nil, http.StatusUnsupportedMediaType, errors.New(
			"Structured request DTOs require a JSON media type.",
		)
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, http.StatusRequestEntityTooLarge, errors.New(
				"Request body exceeds the configured limit.",
			)
		}
		return nil, http.StatusBadRequest, errors.New(
			"Request body could not be read.",
		)
	}
	c.Request.Body = http.NoBody
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, http.StatusBadRequest, errors.New(
			"Request body is required.",
		)
	}

	declaredType := contract.RequestDTO
	baseType := normalizedDTOType(declaredType)
	target := reflect.New(baseType)
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target.Interface()); err != nil {
		return nil, http.StatusBadRequest, errors.New(
			"Request body does not match the registered DTO.",
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, http.StatusBadRequest, errors.New(
			"Request body must contain exactly one JSON value.",
		)
	}
	if err := binding.Validator.ValidateStruct(target.Interface()); err != nil {
		return nil, http.StatusBadRequest, errors.New(
			"Request body failed validation.",
		)
	}
	if declaredType.Kind() == reflect.Pointer {
		return target.Interface(), 0, nil
	}
	return target.Elem().Interface(), 0, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("additional JSON value")
		}
		return err
	}
	return nil
}

func normalizedDTOType(value reflect.Type) reflect.Type {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value
}

func mediaTypeMatches(actual, expected string) bool {
	if expected == "*/*" {
		return actual != ""
	}
	if actual == expected {
		return true
	}
	prefix, wildcard := strings.CutSuffix(expected, "/*")
	return wildcard && strings.HasPrefix(actual, prefix+"/")
}

func validateContractParameters(
	c *gin.Context,
	parameters []module.ParameterDefinition,
) error {
	for _, parameter := range parameters {
		value, present := contractParameterValue(c, parameter)
		if !present {
			if parameter.Required {
				return errors.New(
					"Required parameter " + parameter.Name + " is missing.",
				)
			}
			continue
		}
		if err := validateContractParameterValue(value, parameter); err != nil {
			return errors.New(
				"Parameter " + parameter.Name + " is invalid.",
			)
		}
	}
	return nil
}

func contractParameterValue(
	c *gin.Context,
	parameter module.ParameterDefinition,
) (string, bool) {
	switch parameter.In {
	case module.ParameterPath:
		value := c.Param(parameter.Name)
		return value, value != ""
	case module.ParameterQuery:
		return c.GetQuery(parameter.Name)
	case module.ParameterHeader:
		values, present := c.Request.Header[http.CanonicalHeaderKey(
			parameter.Name,
		)]
		if !present || len(values) != 1 {
			return "", false
		}
		return values[0], true
	default:
		return "", false
	}
}

func validateContractParameterValue(
	value string,
	parameter module.ParameterDefinition,
) error {
	switch parameter.Type {
	case module.ParameterString:
		if parameter.MaxLength != nil &&
			len([]rune(value)) > *parameter.MaxLength {
			return errors.New("too long")
		}
		if parameter.Pattern != "" {
			matches, err := regexp.MatchString(parameter.Pattern, value)
			if err != nil || !matches {
				return errors.New("pattern mismatch")
			}
		}
		if len(parameter.Enum) != 0 {
			found := false
			for _, candidate := range parameter.Enum {
				if value == candidate {
					found = true
					break
				}
			}
			if !found {
				return errors.New("unsupported value")
			}
		}
	case module.ParameterInteger:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		number := float64(parsed)
		if parameter.Minimum != nil && number < *parameter.Minimum {
			return errors.New("below minimum")
		}
		if parameter.Maximum != nil && number > *parameter.Maximum {
			return errors.New("above maximum")
		}
	case module.ParameterBoolean:
		if _, err := strconv.ParseBool(value); err != nil {
			return err
		}
	default:
		return errors.New("unsupported parameter type")
	}
	return nil
}
