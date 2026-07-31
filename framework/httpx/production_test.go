package httpx

import (
	"net/http"
	"strings"
	"testing"
)

func TestValidateProductionSecurity(t *testing.T) {
	valid := ProductionSecurityConfig{
		SessionSecret:         "9Yz!mQ7#vL2@pR8$kT4^wN6&cD1*xF5!",
		SessionCookieSecure:   true,
		SessionCookieHTTPOnly: true,
		SessionCookieSameSite: http.SameSiteLaxMode,
		AllowedOrigins:        []string{"https://admin.example"},
		CORSAllowCredentials:  true,
	}
	if err := ValidateProductionSecurity(valid); err != nil {
		t.Fatalf("valid configuration: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ProductionSecurityConfig)
	}{
		{"empty secret", func(config *ProductionSecurityConfig) { config.SessionSecret = "" }},
		{"short secret", func(config *ProductionSecurityConfig) { config.SessionSecret = "short" }},
		{"placeholder secret", func(config *ProductionSecurityConfig) {
			config.SessionSecret = "replace-with-at-least-32-random-bytes"
		}},
		{"repeated secret", func(config *ProductionSecurityConfig) {
			config.SessionSecret = strings.Repeat("0123456789abcdef", 2)
		}},
		{"whitespace-padded secret", func(config *ProductionSecurityConfig) {
			config.SessionSecret = " 9Yz!mQ7#vL2@pR8$kT4^wN6&cD1*xF5!"
		}},
		{"insecure cookie", func(config *ProductionSecurityConfig) { config.SessionCookieSecure = false }},
		{"script-readable session cookie", func(config *ProductionSecurityConfig) { config.SessionCookieHTTPOnly = false }},
		{"unspecified SameSite", func(config *ProductionSecurityConfig) {
			config.SessionCookieSameSite = http.SameSiteDefaultMode
		}},
		{"empty origin allowlist", func(config *ProductionSecurityConfig) { config.AllowedOrigins = nil }},
		{"wildcard credentialed CORS", func(config *ProductionSecurityConfig) {
			config.AllowedOrigins = []string{"*"}
			config.CORSAllowCredentials = true
		}},
		{"origin with a path", func(config *ProductionSecurityConfig) {
			config.AllowedOrigins = []string{"https://admin.example/path"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if err := ValidateProductionSecurity(config); err == nil {
				t.Fatal("expected production validation to fail")
			}
		})
	}
}

func TestCredentialLooksInsecure(t *testing.T) {
	for _, credential := range []string{
		"",
		" change-me-before-production",
		"placeholder-credential-for-production-123456789",
		"replace-with-at-least-32-random-bytes",
		"your-production-session-secret-goes-here",
		strings.Repeat("abcd", 8),
		strings.Repeat("x", 32),
	} {
		if !CredentialLooksInsecure(credential) {
			t.Errorf("credential %q was accepted", credential)
		}
	}
	if CredentialLooksInsecure(
		"correct horse battery staple 7! violet",
	) {
		t.Fatal("non-template passphrase was rejected")
	}
}
