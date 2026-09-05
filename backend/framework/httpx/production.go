package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

type ProductionSecurityConfig struct {
	SessionSecret         string
	SessionCookieSecure   bool
	SessionCookieHTTPOnly bool
	SessionCookieSameSite http.SameSite
	AllowedOrigins        []string
	CORSAllowCredentials  bool
}

func ValidateProductionSecurity(config ProductionSecurityConfig) error {
	var validationErrors []error
	if len(config.SessionSecret) < 32 {
		validationErrors = append(
			validationErrors,
			fmt.Errorf("session secret must contain at least 32 bytes"),
		)
	} else if CredentialLooksInsecure(config.SessionSecret) {
		validationErrors = append(
			validationErrors,
			fmt.Errorf(
				"session secret must not be a placeholder, repeated template, or whitespace-padded value",
			),
		)
	}
	if !config.SessionCookieSecure {
		validationErrors = append(validationErrors, fmt.Errorf("session cookie must be Secure"))
	}
	if !config.SessionCookieHTTPOnly {
		validationErrors = append(validationErrors, fmt.Errorf("session cookie must be HttpOnly"))
	}
	if config.SessionCookieSameSite == http.SameSiteDefaultMode {
		validationErrors = append(validationErrors, fmt.Errorf("session cookie SameSite must be explicit"))
	}
	if _, err := buildOriginAllowlist(config.AllowedOrigins); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("CORS allowlist: %w", err))
	}
	return errors.Join(validationErrors...)
}
