package migrate

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

func Up(db *sql.DB, driver string) error {
	gooseDialect, err := dialect(driver)
	if err != nil {
		return err
	}
	if err := goose.SetDialect(gooseDialect); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	goose.SetBaseFS(migrationFS)
	if err := goose.Up(db, "migrations/"+driver); err != nil {
		return fmt.Errorf("apply %s migrations: %w", driver, err)
	}
	return nil
}

func DownToZero(db *sql.DB, driver string) error {
	gooseDialect, err := dialect(driver)
	if err != nil {
		return err
	}
	if err := goose.SetDialect(gooseDialect); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	goose.SetBaseFS(migrationFS)
	if err := goose.DownTo(db, "migrations/"+driver, 0); err != nil {
		return fmt.Errorf("roll back %s migrations: %w", driver, err)
	}
	return nil
}

func dialect(driver string) (string, error) {
	switch driver {
	case "sqlite":
		return "sqlite3", nil
	case "postgres":
		return "postgres", nil
	case "mysql":
		return "mysql", nil
	default:
		return "", fmt.Errorf("unsupported migration driver %q", driver)
	}
}
