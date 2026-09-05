package httpx

import (
	"fmt"
	"net/url"
	"strings"
)

func buildOriginAllowlist(origins []string) (map[string]struct{}, error) {
	if len(origins) == 0 {
		return nil, fmt.Errorf("at least one allowed origin is required")
	}
	allowlist := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		canonical, err := canonicalOrigin(origin)
		if err != nil {
			return nil, err
		}
		if _, exists := allowlist[canonical]; exists {
			return nil, fmt.Errorf("duplicate allowed origin %q", origin)
		}
		allowlist[canonical] = struct{}{}
	}
	return allowlist, nil
}

func canonicalOrigin(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("origin must not be empty")
	}
	if value == "*" {
		return "", fmt.Errorf("wildcard origins are not allowed")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid origin %q: %w", value, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Opaque != "" ||
		parsed.Path != "" ||
		parsed.RawPath != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", fmt.Errorf("origin %q must contain only an http(s) scheme and authority", value)
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}

func refererOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("invalid referer: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Opaque != "" {
		return "", fmt.Errorf("referer must contain an http(s) origin")
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}

func sourceHeadersAllowed(origin, referer string, allowlist map[string]struct{}, required bool) bool {
	seen := false
	if origin = strings.TrimSpace(origin); origin != "" {
		seen = true
		canonical, err := canonicalOrigin(origin)
		if err != nil {
			return false
		}
		if _, allowed := allowlist[canonical]; !allowed {
			return false
		}
	}
	if referer = strings.TrimSpace(referer); referer != "" {
		seen = true
		canonical, err := refererOrigin(referer)
		if err != nil {
			return false
		}
		if _, allowed := allowlist[canonical]; !allowed {
			return false
		}
	}
	return seen || !required
}
