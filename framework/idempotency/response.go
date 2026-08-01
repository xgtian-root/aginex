package idempotency

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const (
	maxHeaderValues = 64
	maxJSONNodes    = 100_000
)

var safeResponseHeaders = map[string]struct{}{
	"Cache-Control":      {},
	"Content-Language":   {},
	"Content-Location":   {},
	"Etag":               {},
	"Expires":            {},
	"Last-Modified":      {},
	"Location":           {},
	"Preference-Applied": {},
	"Retry-After":        {},
	"Vary":               {},
}

var sensitiveJSONFields = map[string]struct{}{
	"password":         {},
	"passwordhash":     {},
	"passwd":           {},
	"passphrase":       {},
	"token":            {},
	"accesstoken":      {},
	"refreshtoken":     {},
	"sessiontoken":     {},
	"authorization":    {},
	"cookie":           {},
	"setcookie":        {},
	"secret":           {},
	"clientsecret":     {},
	"apikey":           {},
	"credential":       {},
	"otp":              {},
	"verificationcode": {},
	"signedurl":        {},
	"signature":        {},
}

func normalizeResponse(response Response) (Response, error) {
	if response.Status < http.StatusOK || response.Status > 599 {
		return Response{}, fmt.Errorf(
			"%w: response status must be between 200 and 599",
			ErrInvalidRequest,
		)
	}
	if len(response.Body) > MaxResponseBodyBytes {
		return Response{}, fmt.Errorf(
			"%w: body has %d bytes, maximum is %d",
			ErrResponseTooLarge,
			len(response.Body),
			MaxResponseBodyBytes,
		)
	}

	contentType := strings.TrimSpace(response.ContentType)
	if len(contentType) > 255 || hasControl(contentType) {
		return Response{}, fmt.Errorf("%w: invalid content type", ErrUnsafeResponse)
	}
	mediaType := ""
	if contentType != "" {
		parsed, parameters, err := mime.ParseMediaType(contentType)
		if err != nil {
			return Response{}, fmt.Errorf("%w: invalid content type", ErrUnsafeResponse)
		}
		for name, value := range parameters {
			if sensitiveFieldName(name) || containsSignedURL(value) {
				return Response{}, fmt.Errorf(
					"%w: content type contains sensitive parameters",
					ErrUnsafeResponse,
				)
			}
		}
		mediaType = strings.ToLower(parsed)
		contentType = mime.FormatMediaType(mediaType, parameters)
	}
	if len(response.Body) > 0 {
		if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
			return Response{}, fmt.Errorf(
				"%w: non-empty replay bodies must use a JSON media type",
				ErrUnsafeResponse,
			)
		}
		if err := validateJSONBody(response.Body); err != nil {
			return Response{}, err
		}
	}

	headers, err := normalizeHeaders(response.Headers, len(contentType))
	if err != nil {
		return Response{}, err
	}
	return Response{
		Status:      response.Status,
		ContentType: contentType,
		Headers:     headers,
		Body:        bytes.Clone(response.Body),
	}, nil
}

func normalizeHeaders(headers http.Header, contentTypeSize int) (http.Header, error) {
	normalized := make(http.Header, len(headers))
	valueCount := 0
	currentSize := contentTypeSize
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" || hasControl(canonical) {
			return nil, fmt.Errorf("%w: invalid response header name", ErrUnsafeResponse)
		}
		if _, allowed := safeResponseHeaders[canonical]; !allowed {
			return nil, fmt.Errorf(
				"%w: response header %q is not safe to persist",
				ErrUnsafeResponse,
				canonical,
			)
		}
		if len(values) == 0 {
			continue
		}
		currentSize += len(canonical)
		for _, value := range values {
			if hasControl(value) {
				return nil, fmt.Errorf(
					"%w: response header %q contains control characters",
					ErrUnsafeResponse,
					canonical,
				)
			}
			if (canonical == "Location" || canonical == "Content-Location") &&
				!safeLocalLocation(value) {
				return nil, fmt.Errorf(
					"%w: response header %q must contain a local absolute path without query or fragment",
					ErrUnsafeResponse,
					canonical,
				)
			}
			valueCount++
			if valueCount > maxHeaderValues {
				return nil, fmt.Errorf(
					"%w: response has more than %d header values",
					ErrResponseTooLarge,
					maxHeaderValues,
				)
			}
			currentSize += len(value)
			if currentSize > MaxResponseHeadersBytes {
				return nil, fmt.Errorf(
					"%w: response headers exceed %d bytes",
					ErrResponseTooLarge,
					MaxResponseHeadersBytes,
				)
			}
			normalized[canonical] = append(normalized[canonical], value)
		}
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: encode response headers: %v", ErrUnsafeResponse, err)
	}
	if len(encoded)+contentTypeSize > MaxResponseHeadersBytes {
		return nil, fmt.Errorf(
			"%w: encoded response headers exceed storage bounds",
			ErrResponseTooLarge,
		)
	}
	return normalized, nil
}

func validateJSONBody(body []byte) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: response body is not valid JSON", ErrUnsafeResponse)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("%w: response body contains multiple JSON values", ErrUnsafeResponse)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: response body has invalid trailing data", ErrUnsafeResponse)
	}

	queue := []any{value}
	nodes := 0
	for len(queue) > 0 {
		current := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		nodes++
		if nodes > maxJSONNodes {
			return fmt.Errorf("%w: response JSON is too complex", ErrResponseTooLarge)
		}
		switch typed := current.(type) {
		case map[string]any:
			for name, child := range typed {
				if sensitiveFieldName(name) {
					return fmt.Errorf(
						"%w: response JSON field %q cannot be persisted",
						ErrUnsafeResponse,
						name,
					)
				}
				queue = append(queue, child)
			}
		case []any:
			queue = append(queue, typed...)
		case string:
			if containsSignedURL(typed) {
				return fmt.Errorf(
					"%w: response JSON contains a signed or credential-bearing URL",
					ErrUnsafeResponse,
				)
			}
		}
	}
	return nil
}

func safeLocalLocation(value string) bool {
	return strings.HasPrefix(value, "/") &&
		!strings.HasPrefix(value, "//") &&
		!strings.ContainsAny(value, "?#\\") &&
		!hasControl(value)
}

func containsSignedURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return strings.Contains(value, "?")
	}
	if parsed.RawQuery == "" {
		return false
	}
	for name := range parsed.Query() {
		normalized := normalizeFieldName(name)
		if normalized == "sig" ||
			normalized == "signature" ||
			strings.HasSuffix(normalized, "signature") ||
			strings.HasSuffix(normalized, "credential") ||
			strings.HasSuffix(normalized, "token") ||
			normalized == "ossaccesskeyid" ||
			normalized == "apikey" {
			return true
		}
	}
	return false
}

func sensitiveFieldName(name string) bool {
	normalized := normalizeFieldName(name)
	if _, sensitive := sensitiveJSONFields[normalized]; sensitive {
		return true
	}
	for _, suffix := range []string{
		"password",
		"passphrase",
		"token",
		"secret",
		"credential",
		"cookie",
		"otp",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

func normalizeFieldName(name string) string {
	var builder strings.Builder
	builder.Grow(len(name))
	for _, character := range strings.ToLower(name) {
		switch character {
		case '-', '_', '.', ' ':
			continue
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func hasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func cloneResponse(response Response) Response {
	cloned := Response{
		Status:      response.Status,
		ContentType: response.ContentType,
		Body:        bytes.Clone(response.Body),
	}
	if response.Headers != nil {
		cloned.Headers = response.Headers.Clone()
	}
	return cloned
}
