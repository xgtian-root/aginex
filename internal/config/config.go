package config

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xgtian-root/aginex/framework/httpx"
)

type Config struct {
	Environment string
	HTTP        HTTP
	Database    Database
	Session     Session
	RateLimit   RateLimit
	Idempotency Idempotency
	Jobs        Jobs
	Bootstrap   Bootstrap
	Storage     Storage
	WebOrigin   string
	WebOrigins  []string
}

type HTTP struct {
	Address             string
	PublicURL           string
	TrustedProxies      []string
	MaxBodyBytes        int64
	MaxHeaderBytes      int
	MaxHeaderCount      int
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownGracePeriod time.Duration
}

type Database struct {
	Driver string
	DSN    string
}

type Session struct {
	CookieName string
	Secret     string
	Secure     bool
	TTL        time.Duration
	SameSite   http.SameSite
	CSRFCookie string
	CSRFHeader string
}

type RateLimit struct {
	LoginLimit      uint64
	LoginWindow     time.Duration
	UploadLimit     uint64
	UploadWindow    time.Duration
	SensitiveLimit  uint64
	SensitiveWindow time.Duration
}

type Idempotency struct {
	Driver        string
	LeaseDuration time.Duration
	TTL           time.Duration
}

type Jobs struct {
	Driver        string
	WorkerID      string
	PollInterval  time.Duration
	LeaseDuration time.Duration
	Concurrency   int
}

type Bootstrap struct {
	AdminEmail    string
	AdminPassword string
}

type Storage struct {
	Driver          string
	LocalRoot       string
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	AccessKeySecret string
}

// loadEnvironmentConfig loads configuration owned by the process environment.
// Database installation state is resolved separately by LoadState so an
// entirely unconfigured database can be represented without an implicit
// SQLite fallback.
func loadEnvironmentConfig() (Config, error) {
	sessionTTL, err := durationValue("AGINEX_SESSION_TTL", "24h")
	if err != nil {
		return Config{}, err
	}

	secureCookie, err := strconv.ParseBool(value("AGINEX_SESSION_SECURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("AGINEX_SESSION_SECURE: %w", err)
	}
	sameSite, err := parseSameSite(value("AGINEX_SESSION_SAME_SITE", "lax"))
	if err != nil {
		return Config{}, err
	}
	maxBodyBytes, err := int64Value("AGINEX_HTTP_MAX_BODY_BYTES", 12<<20)
	if err != nil {
		return Config{}, err
	}
	maxHeaderBytes, err := intValue("AGINEX_HTTP_MAX_HEADER_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	maxHeaderCount, err := intValue("AGINEX_HTTP_MAX_HEADER_COUNT", 100)
	if err != nil {
		return Config{}, err
	}
	readHeaderTimeout, err := durationValue("AGINEX_HTTP_READ_HEADER_TIMEOUT", "5s")
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := durationValue("AGINEX_HTTP_READ_TIMEOUT", "15s")
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := durationValue("AGINEX_HTTP_WRITE_TIMEOUT", "30s")
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := durationValue("AGINEX_HTTP_IDLE_TIMEOUT", "60s")
	if err != nil {
		return Config{}, err
	}
	shutdownGracePeriod, err := durationValue("AGINEX_HTTP_SHUTDOWN_GRACE_PERIOD", "10s")
	if err != nil {
		return Config{}, err
	}
	webOrigins := csvValue("AGINEX_WEB_ORIGINS")
	if len(webOrigins) == 0 {
		webOrigins = []string{value("AGINEX_WEB_ORIGIN", "http://localhost:3000")}
	}
	loginLimit, err := uint64Value("AGINEX_RATE_LIMIT_LOGIN_LIMIT", 10)
	if err != nil {
		return Config{}, err
	}
	loginWindow, err := durationValue("AGINEX_RATE_LIMIT_LOGIN_WINDOW", "5m")
	if err != nil {
		return Config{}, err
	}
	uploadLimit, err := uint64Value("AGINEX_RATE_LIMIT_UPLOAD_LIMIT", 120)
	if err != nil {
		return Config{}, err
	}
	uploadWindow, err := durationValue("AGINEX_RATE_LIMIT_UPLOAD_WINDOW", "1m")
	if err != nil {
		return Config{}, err
	}
	sensitiveLimit, err := uint64Value("AGINEX_RATE_LIMIT_SENSITIVE_LIMIT", 300)
	if err != nil {
		return Config{}, err
	}
	sensitiveWindow, err := durationValue("AGINEX_RATE_LIMIT_SENSITIVE_WINDOW", "1m")
	if err != nil {
		return Config{}, err
	}
	idempotencyLeaseDuration, err := durationValue(
		"AGINEX_IDEMPOTENCY_LEASE_DURATION",
		"30s",
	)
	if err != nil {
		return Config{}, err
	}
	idempotencyTTL, err := durationValue("AGINEX_IDEMPOTENCY_TTL", "24h")
	if err != nil {
		return Config{}, err
	}
	jobPollInterval, err := durationValue("AGINEX_JOBS_POLL_INTERVAL", "1s")
	if err != nil {
		return Config{}, err
	}
	jobLeaseDuration, err := durationValue("AGINEX_JOBS_LEASE_DURATION", "1m")
	if err != nil {
		return Config{}, err
	}
	jobConcurrency, err := intValue("AGINEX_JOBS_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Environment: value("AGINEX_ENV", "development"),
		HTTP: HTTP{
			Address:             value("AGINEX_HTTP_ADDRESS", ":8080"),
			PublicURL:           value("AGINEX_API_PUBLIC_URL", "http://localhost:8080"),
			TrustedProxies:      csvValue("AGINEX_TRUSTED_PROXIES"),
			MaxBodyBytes:        maxBodyBytes,
			MaxHeaderBytes:      maxHeaderBytes,
			MaxHeaderCount:      maxHeaderCount,
			ReadHeaderTimeout:   readHeaderTimeout,
			ReadTimeout:         readTimeout,
			WriteTimeout:        writeTimeout,
			IdleTimeout:         idleTimeout,
			ShutdownGracePeriod: shutdownGracePeriod,
		},
		Database: Database{
			Driver: strings.ToLower(strings.TrimSpace(os.Getenv("AGINEX_DATABASE_DRIVER"))),
			DSN:    strings.TrimSpace(os.Getenv("AGINEX_DATABASE_DSN")),
		},
		Session: Session{
			CookieName: value(
				"AGINEX_SESSION_COOKIE",
				httpx.SessionCookieName,
			),
			Secret:   os.Getenv("AGINEX_SESSION_SECRET"),
			Secure:   secureCookie,
			TTL:      sessionTTL,
			SameSite: sameSite,
			CSRFCookie: value(
				"AGINEX_CSRF_COOKIE",
				httpx.CSRFCookieName,
			),
			CSRFHeader: value(
				"AGINEX_CSRF_HEADER",
				httpx.CSRFHeaderName,
			),
		},
		RateLimit: RateLimit{
			LoginLimit:      loginLimit,
			LoginWindow:     loginWindow,
			UploadLimit:     uploadLimit,
			UploadWindow:    uploadWindow,
			SensitiveLimit:  sensitiveLimit,
			SensitiveWindow: sensitiveWindow,
		},
		Idempotency: Idempotency{
			Driver:        strings.ToLower(value("AGINEX_IDEMPOTENCY_DRIVER", "database")),
			LeaseDuration: idempotencyLeaseDuration,
			TTL:           idempotencyTTL,
		},
		Jobs: Jobs{
			Driver:        strings.ToLower(value("AGINEX_JOBS_DRIVER", "disabled")),
			WorkerID:      value("AGINEX_JOBS_WORKER_ID", "aginex-worker"),
			PollInterval:  jobPollInterval,
			LeaseDuration: jobLeaseDuration,
			Concurrency:   jobConcurrency,
		},
		Bootstrap: Bootstrap{
			AdminEmail:    strings.TrimSpace(strings.ToLower(os.Getenv("AGINEX_BOOTSTRAP_ADMIN_EMAIL"))),
			AdminPassword: os.Getenv("AGINEX_BOOTSTRAP_ADMIN_PASSWORD"),
		},
		Storage: Storage{
			Driver:          strings.ToLower(value("AGINEX_STORAGE_DRIVER", "local")),
			LocalRoot:       value("AGINEX_STORAGE_LOCAL_ROOT", "data/uploads"),
			Bucket:          os.Getenv("AGINEX_STORAGE_BUCKET"),
			Region:          os.Getenv("AGINEX_STORAGE_REGION"),
			Endpoint:        os.Getenv("AGINEX_STORAGE_ENDPOINT"),
			AccessKeyID:     os.Getenv("AGINEX_STORAGE_ACCESS_KEY_ID"),
			AccessKeySecret: os.Getenv("AGINEX_STORAGE_ACCESS_KEY_SECRET"),
		},
		WebOrigin:  webOrigins[0],
		WebOrigins: webOrigins,
	}
	cfg = WithDefaults(cfg)
	return cfg, nil
}

// WithDefaults normalizes fields that are commonly omitted by tests and
// embedders constructing Config directly.
func WithDefaults(cfg Config) Config {
	if cfg.Environment == "" {
		cfg.Environment = "development"
	}
	if cfg.HTTP.Address == "" {
		cfg.HTTP.Address = ":8080"
	}
	if cfg.HTTP.PublicURL == "" {
		cfg.HTTP.PublicURL = "http://localhost:8080"
	}
	if cfg.Session.CookieName == "" {
		cfg.Session.CookieName = httpx.SessionCookieName
	}
	if cfg.Session.CSRFCookie == "" {
		cfg.Session.CSRFCookie = httpx.CSRFCookieName
	}
	if cfg.Session.CSRFHeader == "" {
		cfg.Session.CSRFHeader = httpx.CSRFHeaderName
	}
	if cfg.Session.SameSite == http.SameSiteDefaultMode {
		cfg.Session.SameSite = http.SameSiteLaxMode
	}
	if cfg.Session.TTL <= 0 {
		cfg.Session.TTL = 24 * time.Hour
	}
	if cfg.RateLimit.LoginLimit == 0 {
		cfg.RateLimit.LoginLimit = 10
	}
	if cfg.RateLimit.LoginWindow <= 0 {
		cfg.RateLimit.LoginWindow = 5 * time.Minute
	}
	if cfg.RateLimit.UploadLimit == 0 {
		cfg.RateLimit.UploadLimit = 120
	}
	if cfg.RateLimit.UploadWindow <= 0 {
		cfg.RateLimit.UploadWindow = time.Minute
	}
	if cfg.RateLimit.SensitiveLimit == 0 {
		cfg.RateLimit.SensitiveLimit = 300
	}
	if cfg.RateLimit.SensitiveWindow <= 0 {
		cfg.RateLimit.SensitiveWindow = time.Minute
	}
	if cfg.Idempotency.Driver == "" {
		cfg.Idempotency.Driver = "database"
	}
	if cfg.Idempotency.LeaseDuration <= 0 {
		cfg.Idempotency.LeaseDuration = 30 * time.Second
	}
	if cfg.Idempotency.TTL <= 0 {
		cfg.Idempotency.TTL = 24 * time.Hour
	}
	if cfg.Jobs.Driver == "" {
		cfg.Jobs.Driver = "disabled"
	}
	if cfg.Jobs.WorkerID == "" {
		cfg.Jobs.WorkerID = "aginex-worker"
	}
	if cfg.Jobs.PollInterval <= 0 {
		cfg.Jobs.PollInterval = time.Second
	}
	if cfg.Jobs.LeaseDuration <= 0 {
		cfg.Jobs.LeaseDuration = time.Minute
	}
	if cfg.Jobs.Concurrency <= 0 {
		cfg.Jobs.Concurrency = 4
	}
	if cfg.HTTP.MaxBodyBytes <= 0 {
		cfg.HTTP.MaxBodyBytes = 12 << 20
	}
	if cfg.HTTP.MaxHeaderBytes <= 0 {
		cfg.HTTP.MaxHeaderBytes = 1 << 20
	}
	if cfg.HTTP.MaxHeaderCount <= 0 {
		cfg.HTTP.MaxHeaderCount = 100
	}
	if cfg.HTTP.ReadHeaderTimeout <= 0 {
		cfg.HTTP.ReadHeaderTimeout = 5 * time.Second
	}
	if cfg.HTTP.ReadTimeout <= 0 {
		cfg.HTTP.ReadTimeout = 15 * time.Second
	}
	if cfg.HTTP.WriteTimeout <= 0 {
		cfg.HTTP.WriteTimeout = 30 * time.Second
	}
	if cfg.HTTP.IdleTimeout <= 0 {
		cfg.HTTP.IdleTimeout = 60 * time.Second
	}
	if cfg.HTTP.ShutdownGracePeriod <= 0 {
		cfg.HTTP.ShutdownGracePeriod = 10 * time.Second
	}
	if len(cfg.WebOrigins) == 0 && cfg.WebOrigin != "" {
		cfg.WebOrigins = []string{cfg.WebOrigin}
	}
	if len(cfg.WebOrigins) == 0 && cfg.WebOrigin == "" {
		cfg.WebOrigin = "http://localhost:3000"
		cfg.WebOrigins = []string{cfg.WebOrigin}
	}
	if cfg.WebOrigin == "" && len(cfg.WebOrigins) > 0 {
		cfg.WebOrigin = cfg.WebOrigins[0]
	}
	return cfg
}

// Validate applies the same fail-closed configuration checks used by Load to
// programmatically constructed configurations. Embedders must not be able to
// bypass production cookie, origin, or provider checks by skipping the
// environment loader.
func Validate(input Config) error {
	cfg := WithDefaults(input)
	if err := validateWithoutDatabase(cfg); err != nil {
		return err
	}
	switch cfg.Database.Driver {
	case "sqlite", "postgres", "mysql":
	default:
		return fmt.Errorf(
			"unsupported database driver %q",
			cfg.Database.Driver,
		)
	}
	if cfg.Jobs.Driver == "postgres" && cfg.Database.Driver != "postgres" {
		return fmt.Errorf(
			"AGINEX_JOBS_DRIVER=postgres requires AGINEX_DATABASE_DRIVER=postgres",
		)
	}
	return nil
}

// validateWithoutDatabase validates configuration needed by both the setup
// server and the fully initialized application. Database-specific checks are
// intentionally deferred until setup supplies a database configuration.
func validateWithoutDatabase(cfg Config) error {
	switch cfg.Environment {
	case "development", "test", "production":
	default:
		return fmt.Errorf(
			"unsupported AGINEX_ENV %q; use development, test, or production",
			cfg.Environment,
		)
	}
	if err := validateBrowserProtocol(cfg.Session); err != nil {
		return err
	}
	if err := validateTrustedProxies(cfg.HTTP.TrustedProxies); err != nil {
		return err
	}
	switch cfg.Storage.Driver {
	case "local", "s3", "oss":
	default:
		return fmt.Errorf(
			"unsupported storage driver %q",
			cfg.Storage.Driver,
		)
	}
	switch cfg.Jobs.Driver {
	case "disabled", "postgres":
	default:
		return fmt.Errorf(
			"unsupported jobs driver %q",
			cfg.Jobs.Driver,
		)
	}
	switch cfg.Idempotency.Driver {
	case "disabled", "database":
	default:
		return fmt.Errorf(
			"unsupported idempotency driver %q",
			cfg.Idempotency.Driver,
		)
	}
	if cfg.Environment == "production" {
		return validateProduction(cfg)
	}
	return nil
}

func (c Config) AllowedWebOrigins() []string {
	if len(c.WebOrigins) > 0 {
		return append([]string(nil), c.WebOrigins...)
	}
	if c.WebOrigin != "" {
		return []string{c.WebOrigin}
	}
	return nil
}

func validateProduction(cfg Config) error {
	if err := httpx.ValidateProductionSecurity(httpx.ProductionSecurityConfig{
		SessionSecret:         cfg.Session.Secret,
		SessionCookieSecure:   cfg.Session.Secure,
		SessionCookieHTTPOnly: true,
		SessionCookieSameSite: cfg.Session.SameSite,
		AllowedOrigins:        cfg.AllowedWebOrigins(),
		CORSAllowCredentials:  true,
	}); err != nil {
		return fmt.Errorf("unsafe production configuration: %w", err)
	}
	publicURL, err := url.Parse(cfg.HTTP.PublicURL)
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		return fmt.Errorf("AGINEX_API_PUBLIC_URL must be an absolute https URL in production")
	}
	for _, origin := range cfg.AllowedWebOrigins() {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "https" {
			return fmt.Errorf("AGINEX_WEB_ORIGINS must contain only https origins in production")
		}
	}
	if (cfg.Bootstrap.AdminEmail == "") !=
		(cfg.Bootstrap.AdminPassword == "") {
		return fmt.Errorf(
			"AGINEX_BOOTSTRAP_ADMIN_EMAIL and AGINEX_BOOTSTRAP_ADMIN_PASSWORD must either both be set or both be empty in production",
		)
	}
	if cfg.Bootstrap.AdminPassword != "" {
		if len(cfg.Bootstrap.AdminPassword) < 12 {
			return fmt.Errorf(
				"AGINEX_BOOTSTRAP_ADMIN_PASSWORD must contain at least 12 bytes in production",
			)
		}
		if httpx.CredentialLooksInsecure(
			cfg.Bootstrap.AdminPassword,
		) {
			return fmt.Errorf(
				"AGINEX_BOOTSTRAP_ADMIN_PASSWORD must not be a placeholder, repeated template, or whitespace-padded value in production",
			)
		}
	}
	return nil
}

func validateBrowserProtocol(session Session) error {
	switch {
	case session.CookieName != httpx.SessionCookieName:
		return fmt.Errorf(
			"AGINEX_SESSION_COOKIE is fixed to %q by the published API contract",
			httpx.SessionCookieName,
		)
	case session.CSRFCookie != httpx.CSRFCookieName:
		return fmt.Errorf(
			"AGINEX_CSRF_COOKIE is fixed to %q by the published API contract",
			httpx.CSRFCookieName,
		)
	case session.CSRFHeader != httpx.CSRFHeaderName:
		return fmt.Errorf(
			"AGINEX_CSRF_HEADER is fixed to %q by the published API contract",
			httpx.CSRFHeaderName,
		)
	default:
		return nil
	}
}

func validateTrustedProxies(proxies []string) error {
	for _, raw := range proxies {
		proxy := strings.TrimSpace(raw)
		if proxy == "" {
			return fmt.Errorf(
				"AGINEX_TRUSTED_PROXIES must not contain empty entries",
			)
		}
		if net.ParseIP(proxy) != nil {
			continue
		}
		_, network, err := net.ParseCIDR(proxy)
		if err != nil {
			return fmt.Errorf(
				"AGINEX_TRUSTED_PROXIES contains invalid IP or CIDR %q",
				proxy,
			)
		}
		prefix, _ := network.Mask.Size()
		if prefix == 0 {
			return fmt.Errorf(
				"AGINEX_TRUSTED_PROXIES must not trust an entire address family (%q)",
				proxy,
			)
		}
	}
	return nil
}

func parseSameSite(input string) (http.SameSite, error) {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "lax":
		return http.SameSiteLaxMode, nil
	case "strict":
		return http.SameSiteStrictMode, nil
	case "none":
		return http.SameSiteNoneMode, nil
	default:
		return http.SameSiteDefaultMode, fmt.Errorf(
			"AGINEX_SESSION_SAME_SITE must be one of lax, strict, or none",
		)
	}
}

func durationValue(key, fallback string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value(key, fallback))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}

func intValue(key string, fallback int) (int, error) {
	parsed, err := strconv.Atoi(value(key, strconv.Itoa(fallback)))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func int64Value(key string, fallback int64) (int64, error) {
	parsed, err := strconv.ParseInt(value(key, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func uint64Value(key string, fallback uint64) (uint64, error) {
	parsed, err := strconv.ParseUint(value(key, strconv.FormatUint(fallback, 10)), 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func csvValue(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func value(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
