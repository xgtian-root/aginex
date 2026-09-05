package httpx

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type CORSConfig struct {
	AllowedOrigins   []string
	AllowCredentials bool
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
}

func NewCORS(config CORSConfig) (gin.HandlerFunc, error) {
	allowlist, err := buildOriginAllowlist(config.AllowedOrigins)
	if err != nil {
		return nil, fmt.Errorf("configure CORS origins: %w", err)
	}
	methods, methodSet, err := normalizeMethods(config.AllowedMethods)
	if err != nil {
		return nil, err
	}
	headers, headerSet, err := normalizeHeaders(config.AllowedHeaders)
	if err != nil {
		return nil, err
	}
	exposed, _, err := normalizeHeaders(config.ExposedHeaders)
	if err != nil {
		return nil, fmt.Errorf("configure exposed CORS headers: %w", err)
	}

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" {
			c.Next()
			return
		}
		canonical, err := canonicalOrigin(origin)
		if err != nil {
			AbortProblem(c, http.StatusForbidden, "CORS_FORBIDDEN", "Cross-origin request denied", "The request origin is invalid.")
			return
		}
		if _, allowed := allowlist[canonical]; !allowed {
			AbortProblem(c, http.StatusForbidden, "CORS_FORBIDDEN", "Cross-origin request denied", "The request origin is not allowed.")
			return
		}

		addVary(c.Writer.Header(), "Origin")
		c.Header("Access-Control-Allow-Origin", origin)
		if config.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if len(exposed) > 0 {
			c.Header("Access-Control-Expose-Headers", strings.Join(exposed, ", "))
		}

		if c.Request.Method == http.MethodOptions {
			if !validPreflight(c, methodSet, headerSet) {
				AbortProblem(c, http.StatusForbidden, "CORS_PREFLIGHT_FORBIDDEN", "CORS preflight denied", "The requested method or header is not allowed.")
				return
			}
			c.Header("Access-Control-Allow-Methods", strings.Join(methods, ", "))
			if len(headers) > 0 {
				c.Header("Access-Control-Allow-Headers", strings.Join(headers, ", "))
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}, nil
}

func normalizeMethods(input []string) ([]string, map[string]struct{}, error) {
	if len(input) == 0 {
		input = []string{
			http.MethodGet,
			http.MethodHead,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		}
	}
	output := make([]string, 0, len(input))
	values := make(map[string]struct{}, len(input))
	for _, method := range input {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "" || strings.ContainsAny(method, ", \t\r\n") {
			return nil, nil, fmt.Errorf("invalid CORS method %q", method)
		}
		if _, exists := values[method]; exists {
			continue
		}
		values[method] = struct{}{}
		output = append(output, method)
	}
	return output, values, nil
}

func normalizeHeaders(input []string) ([]string, map[string]struct{}, error) {
	output := make([]string, 0, len(input))
	values := make(map[string]struct{}, len(input))
	for _, header := range input {
		header = strings.TrimSpace(header)
		if header == "" || strings.ContainsAny(header, ",\r\n") {
			return nil, nil, fmt.Errorf("invalid CORS header %q", header)
		}
		normalized := strings.ToLower(header)
		if _, exists := values[normalized]; exists {
			continue
		}
		values[normalized] = struct{}{}
		output = append(output, header)
	}
	return output, values, nil
}

func validPreflight(c *gin.Context, methods, headers map[string]struct{}) bool {
	if method := strings.ToUpper(strings.TrimSpace(c.GetHeader("Access-Control-Request-Method"))); method != "" {
		if _, allowed := methods[method]; !allowed {
			return false
		}
	}
	for _, header := range strings.Split(c.GetHeader("Access-Control-Request-Headers"), ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header == "" {
			continue
		}
		if _, allowed := headers[header]; !allowed {
			return false
		}
	}
	return true
}

func addVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
