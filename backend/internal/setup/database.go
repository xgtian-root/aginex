package setup

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	mysqldriver "github.com/go-sql-driver/mysql"
)

const (
	maximumDatabaseDSNBytes      = 8192
	maximumDatabaseHostBytes     = 255
	maximumDatabaseNameBytes     = 128
	maximumDatabaseUsernameBytes = 128
	maximumDatabasePasswordBytes = 1024
	maximumSQLiteDirectoryBytes  = 4096
	maximumSQLiteFilenameBytes   = 255
)

var errInvalidDatabaseInput = errors.New("invalid setup database configuration")

// resolveDatabaseInput is the single boundary which converts browser-facing
// structured fields into the opaque runtime and persisted database config.
// Its errors deliberately never include submitted values because every remote
// database input can contain credentials or infrastructure details.
func resolveDatabaseInput(input DatabaseInput) (DatabaseConfig, error) {
	if input.Driver == "" || input.Driver != strings.TrimSpace(input.Driver) {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}

	switch input.Driver {
	case "sqlite":
		if input.SQLite == nil || input.Postgres != nil || input.MySQL != nil {
			return DatabaseConfig{}, errInvalidDatabaseInput
		}
		return resolveSQLiteDatabase(*input.SQLite)
	case "postgres":
		if input.SQLite != nil || input.Postgres == nil || input.MySQL != nil {
			return DatabaseConfig{}, errInvalidDatabaseInput
		}
		return resolvePostgresDatabase(*input.Postgres)
	case "mysql":
		if input.SQLite != nil || input.Postgres != nil || input.MySQL == nil {
			return DatabaseConfig{}, errInvalidDatabaseInput
		}
		return resolveMySQLDatabase(*input.MySQL)
	default:
		return DatabaseConfig{}, errInvalidDatabaseInput
	}
}

func resolveSQLiteDatabase(input SQLiteDatabaseInput) (DatabaseConfig, error) {
	directory, ok := cleanSQLiteDirectory(input.Directory)
	if !ok || !validSQLiteFilename(input.Filename) {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}

	dsn := filepath.Join(directory, input.Filename)
	if filepath.Dir(dsn) != directory || len(dsn) > maximumDatabaseDSNBytes {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}
	return DatabaseConfig{Driver: "sqlite", DSN: dsn}, nil
}

func cleanSQLiteDirectory(value string) (string, bool) {
	if !validTrimmedText(value, maximumSQLiteDirectoryBytes) ||
		strings.ContainsAny(value, "?#") ||
		strings.HasPrefix(strings.ToLower(value), "file:") ||
		(filepath.Separator != '\\' && strings.ContainsRune(value, '\\')) ||
		containsParentPathSegment(value) {
		return "", false
	}
	cleaned := filepath.Clean(value)
	return cleaned, true
}

func validSQLiteFilename(value string) bool {
	return validTrimmedText(value, maximumSQLiteFilenameBytes) &&
		value != "." && value != ".." &&
		filepath.Base(value) == value &&
		filepath.VolumeName(value) == "" &&
		!strings.ContainsAny(value, `/\\:?#`)
}

func containsParentPathSegment(value string) bool {
	for _, segment := range strings.FieldsFunc(value, func(value rune) bool {
		return value == '/' || value == '\\'
	}) {
		if segment == ".." {
			return true
		}
	}
	return false
}

func resolvePostgresDatabase(input PostgresDatabaseInput) (DatabaseConfig, error) {
	if !validDatabaseHost(input.Host) ||
		!validPort(input.Port) ||
		!validConnectionName(input.Database, maximumDatabaseNameBytes) ||
		!validConnectionName(input.Username, maximumDatabaseUsernameBytes) ||
		!validDatabasePassword(input.Password) ||
		!validPostgresSSLMode(input.SSLMode) {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}

	connection := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(input.Username, input.Password),
		Host:   net.JoinHostPort(input.Host, strconv.Itoa(input.Port)),
		Path:   "/" + input.Database,
	}
	query := url.Values{}
	query.Set("sslmode", input.SSLMode)
	connection.RawQuery = query.Encode()
	dsn := connection.String()
	if dsn == "" || len(dsn) > maximumDatabaseDSNBytes {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}
	return DatabaseConfig{Driver: "postgres", DSN: dsn}, nil
}

func resolveMySQLDatabase(input MySQLDatabaseInput) (DatabaseConfig, error) {
	if !validDatabaseHost(input.Host) ||
		!validPort(input.Port) ||
		!validConnectionName(input.Database, maximumDatabaseNameBytes) ||
		!validConnectionName(input.Username, maximumDatabaseUsernameBytes) ||
		!validDatabasePassword(input.Password) {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}

	tlsConfig, ok := mysqlTLSConfig(input.TLSMode)
	if !ok {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}
	address := net.JoinHostPort(input.Host, strconv.Itoa(input.Port))
	connection := mysqldriver.NewConfig()
	connection.User = input.Username
	connection.Passwd = input.Password
	connection.Net = "tcp"
	connection.Addr = address
	connection.DBName = input.Database
	connection.ParseTime = true
	connection.TLSConfig = tlsConfig
	dsn := connection.FormatDSN()
	if dsn == "" || len(dsn) > maximumDatabaseDSNBytes {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}

	// FormatDSN intentionally emits MySQL's native credential grammar rather
	// than URL user-info. Round-trip the result so delimiter-bearing input can
	// never silently resolve to different credentials or a different endpoint.
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil || parsed.User != input.Username ||
		parsed.Passwd != input.Password || parsed.Net != "tcp" ||
		parsed.Addr != address || parsed.DBName != input.Database ||
		!parsed.ParseTime || parsed.TLSConfig != tlsConfig {
		return DatabaseConfig{}, errInvalidDatabaseInput
	}
	return DatabaseConfig{Driver: "mysql", DSN: dsn}, nil
}

func validPort(value int) bool {
	return value >= 1 && value <= 65535
}

func validDatabaseHost(value string) bool {
	if !validTrimmedText(value, maximumDatabaseHostBytes) ||
		strings.ContainsAny(value, `/\\?#@%[]`) ||
		strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	return !strings.ContainsRune(value, ':') &&
		!strings.HasPrefix(value, ".") &&
		!strings.HasSuffix(value, ".") &&
		!strings.Contains(value, "..")
}

func validConnectionName(value string, maximum int) bool {
	return validTrimmedText(value, maximum) &&
		value != "." && value != ".." &&
		!strings.ContainsAny(value, `/\\?#`)
}

func validDatabasePassword(value string) bool {
	return value != "" && len(value) <= maximumDatabasePasswordBytes &&
		!containsControl(value)
}

func validPostgresSSLMode(value string) bool {
	switch value {
	case "disable", "require", "verify-ca", "verify-full":
		return true
	default:
		return false
	}
}

func mysqlTLSConfig(value string) (string, bool) {
	switch value {
	case "disabled":
		return "", true
	case "required":
		return "true", true
	case "skip-verify":
		return "skip-verify", true
	default:
		return "", false
	}
}

func validTrimmedText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		len(value) <= maximum && !containsControl(value)
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
