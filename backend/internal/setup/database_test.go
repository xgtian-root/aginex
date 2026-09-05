package setup

import (
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestResolveDatabaseInputBuildsDriverDSNs(t *testing.T) {
	tests := []struct {
		name  string
		input DatabaseInput
		want  DatabaseConfig
	}{
		{
			name: "SQLite relative directory",
			input: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{
					Directory: "data/databases/",
					Filename:  "aginex.sqlite3",
				},
			},
			want: DatabaseConfig{
				Driver: "sqlite",
				DSN:    filepath.Join("data", "databases", "aginex.sqlite3"),
			},
		},
		{
			name: "PostgreSQL URL",
			input: DatabaseInput{
				Driver: "postgres",
				Postgres: &PostgresDatabaseInput{
					Host:     "db.example",
					Port:     5432,
					Database: "aginex",
					Username: "aginex",
					Password: "database-secret",
					SSLMode:  "verify-full",
				},
			},
			want: DatabaseConfig{
				Driver: "postgres",
				DSN: "postgres://aginex:database-secret@db.example:5432/" +
					"aginex?sslmode=verify-full",
			},
		},
		{
			name: "PostgreSQL IPv6",
			input: DatabaseInput{
				Driver: "postgres",
				Postgres: &PostgresDatabaseInput{
					Host:     "2001:db8::1",
					Port:     5432,
					Database: "aginex",
					Username: "aginex",
					Password: "database-secret",
					SSLMode:  "disable",
				},
			},
			want: DatabaseConfig{
				Driver: "postgres",
				DSN: "postgres://aginex:database-secret@[2001:db8::1]:5432/" +
					"aginex?sslmode=disable",
			},
		},
		{
			name: "MySQL required TLS",
			input: DatabaseInput{
				Driver: "mysql",
				MySQL: &MySQLDatabaseInput{
					Host:     "db.example",
					Port:     3306,
					Database: "aginex",
					Username: "aginex",
					Password: "database-secret",
					TLSMode:  "required",
				},
			},
			want: DatabaseConfig{
				Driver: "mysql",
				DSN: "aginex:database-secret@tcp(db.example:3306)/" +
					"aginex?parseTime=true&tls=true",
			},
		},
		{
			name: "MySQL disabled TLS",
			input: DatabaseInput{
				Driver: "mysql",
				MySQL: &MySQLDatabaseInput{
					Host:     "127.0.0.1",
					Port:     3306,
					Database: "aginex",
					Username: "aginex",
					Password: "database-secret",
					TLSMode:  "disabled",
				},
			},
			want: DatabaseConfig{
				Driver: "mysql",
				DSN: "aginex:database-secret@tcp(127.0.0.1:3306)/" +
					"aginex?parseTime=true",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveDatabaseInput(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("database = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolvePostgresDatabaseEscapesSpecialCredentials(t *testing.T) {
	input := PostgresDatabaseInput{
		Host:     "db.example",
		Port:     5432,
		Database: "aginex-data",
		Username: "aginex+owner@example",
		Password: " leading :@/?#% remains exact ",
		SSLMode:  "require",
	}
	resolved, err := resolveDatabaseInput(DatabaseInput{
		Driver:   "postgres",
		Postgres: &input,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(resolved.DSN)
	if err != nil {
		t.Fatal(err)
	}
	password, ok := parsed.User.Password()
	if !ok || parsed.User.Username() != input.Username ||
		password != input.Password || parsed.Host != "db.example:5432" ||
		parsed.Path != "/aginex-data" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("parsed PostgreSQL URL = %#v", parsed)
	}
}

func TestResolveMySQLDatabasePreservesSpecialCredentials(t *testing.T) {
	input := MySQLDatabaseInput{
		Host:     "db.example",
		Port:     3306,
		Database: "aginex-data",
		Username: "aginex@owner",
		Password: " leading @ and : remain exact ",
		TLSMode:  "skip-verify",
	}
	resolved, err := resolveDatabaseInput(DatabaseInput{
		Driver: "mysql",
		MySQL:  &input,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mysqldriver.ParseDSN(resolved.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.User != input.Username || parsed.Passwd != input.Password ||
		parsed.DBName != input.Database || parsed.TLSConfig != "skip-verify" ||
		!parsed.ParseTime {
		t.Fatalf("parsed MySQL config = %#v", parsed)
	}
}

func TestResolveSQLiteDatabaseAcceptsCleanAbsoluteDirectory(t *testing.T) {
	directory := t.TempDir() + string(filepath.Separator)
	resolved, err := resolveDatabaseInput(DatabaseInput{
		Driver: "sqlite",
		SQLite: &SQLiteDatabaseInput{
			Directory: directory,
			Filename:  "aginex.db",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Clean(directory), "aginex.db"); resolved.Driver != "sqlite" || resolved.DSN != want {
		t.Fatalf("database = %#v, want SQLite %q", resolved, want)
	}
}

func TestResolveDatabaseInputRejectsInvalidOrMixedFields(t *testing.T) {
	validSQLite := &SQLiteDatabaseInput{Directory: "data", Filename: "aginex.db"}
	validPostgres := &PostgresDatabaseInput{
		Host: "db.example", Port: 5432, Database: "aginex",
		Username: "aginex", Password: "database-secret", SSLMode: "require",
	}
	validMySQL := &MySQLDatabaseInput{
		Host: "db.example", Port: 3306, Database: "aginex",
		Username: "aginex", Password: "database-secret", TLSMode: "required",
	}

	tests := []struct {
		name  string
		input DatabaseInput
	}{
		{name: "missing driver object", input: DatabaseInput{Driver: "sqlite"}},
		{
			name:  "mismatched driver object",
			input: DatabaseInput{Driver: "sqlite", Postgres: validPostgres},
		},
		{
			name: "multiple driver objects",
			input: DatabaseInput{
				Driver: "sqlite", SQLite: validSQLite, MySQL: validMySQL,
			},
		},
		{
			name:  "whitespace padded driver",
			input: DatabaseInput{Driver: " sqlite", SQLite: validSQLite},
		},
		{
			name: "SQLite parent filename",
			input: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{Directory: "data", Filename: "../aginex.db"},
			},
		},
		{
			name: "SQLite parent directory",
			input: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{Directory: "../data", Filename: "aginex.db"},
			},
		},
		{
			name: "SQLite unclean directory",
			input: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{Directory: "data/../other", Filename: "aginex.db"},
			},
		},
		{
			name: "SQLite URI directory",
			input: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{Directory: "file:data", Filename: "aginex.db"},
			},
		},
		{
			name: "SQLite query filename",
			input: DatabaseInput{
				Driver: "sqlite",
				SQLite: &SQLiteDatabaseInput{Directory: "data", Filename: "aginex.db?mode=memory"},
			},
		},
		{
			name: "PostgreSQL host includes port",
			input: DatabaseInput{
				Driver: "postgres",
				Postgres: clonePostgresInput(validPostgres, func(value *PostgresDatabaseInput) {
					value.Host = "db.example:5432"
				}),
			},
		},
		{
			name: "PostgreSQL invalid port",
			input: DatabaseInput{
				Driver: "postgres",
				Postgres: clonePostgresInput(validPostgres, func(value *PostgresDatabaseInput) {
					value.Port = 65536
				}),
			},
		},
		{
			name: "PostgreSQL invalid SSL mode",
			input: DatabaseInput{
				Driver: "postgres",
				Postgres: clonePostgresInput(validPostgres, func(value *PostgresDatabaseInput) {
					value.SSLMode = "prefer"
				}),
			},
		},
		{
			name: "PostgreSQL control in password",
			input: DatabaseInput{
				Driver: "postgres",
				Postgres: clonePostgresInput(validPostgres, func(value *PostgresDatabaseInput) {
					value.Password = "secret\nvalue"
				}),
			},
		},
		{
			name: "PostgreSQL empty password",
			input: DatabaseInput{
				Driver: "postgres",
				Postgres: clonePostgresInput(validPostgres, func(value *PostgresDatabaseInput) {
					value.Password = ""
				}),
			},
		},
		{
			name: "MySQL invalid TLS mode",
			input: DatabaseInput{
				Driver: "mysql",
				MySQL: cloneMySQLInput(validMySQL, func(value *MySQLDatabaseInput) {
					value.TLSMode = "preferred"
				}),
			},
		},
		{
			name: "MySQL empty password",
			input: DatabaseInput{
				Driver: "mysql",
				MySQL: cloneMySQLInput(validMySQL, func(value *MySQLDatabaseInput) {
					value.Password = ""
				}),
			},
		},
		{
			name: "MySQL ambiguous username",
			input: DatabaseInput{
				Driver: "mysql",
				MySQL: cloneMySQLInput(validMySQL, func(value *MySQLDatabaseInput) {
					value.Username = "owner:name"
				}),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveDatabaseInput(test.input)
			if !errors.Is(err, errInvalidDatabaseInput) {
				t.Fatalf("error = %v, want invalid database input", err)
			}
			for _, secret := range []string{"database-secret", "secret\nvalue"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked submitted value: %v", err)
				}
			}
		})
	}
}

func clonePostgresInput(
	input *PostgresDatabaseInput,
	mutate func(*PostgresDatabaseInput),
) *PostgresDatabaseInput {
	cloned := *input
	mutate(&cloned)
	return &cloned
}

func cloneMySQLInput(
	input *MySQLDatabaseInput,
	mutate func(*MySQLDatabaseInput),
) *MySQLDatabaseInput {
	cloned := *input
	mutate(&cloned)
	return &cloned
}
