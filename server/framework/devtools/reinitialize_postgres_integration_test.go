package devtools

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPostgresReinitializeRollbackProbe(t *testing.T) {
	configPath := os.Getenv("AGINEX_REINITIALIZE_PROBE_CONFIG")
	if configPath == "" {
		t.Skip("AGINEX_REINITIALIZE_PROBE_CONFIG is not set")
	}
	plan, err := buildReinitializePlan(configPath, time.Now().UTC())
	if err != nil {
		t.Fatalf("build reinitialization probe plan: %v", err)
	}
	if plan.database.Driver != "postgres" {
		t.Fatalf("probe requires PostgreSQL, got %q", plan.database.Driver)
	}
	db, err := openReinitializeDatabase(t.Context(), plan.database)
	if err != nil {
		t.Fatalf("open PostgreSQL probe target: %v", err)
	}
	defer db.Close()

	beforeOwner := postgresSchemaOwner(t, db, "public")
	beforeRelations := postgresRelationCount(t, db, "public")
	beforeRoutines := postgresRoutineCount(t, db, "public")
	backupSchema := fmt.Sprintf("aginex_reinit_probe_%d", time.Now().UnixNano())
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin PostgreSQL rollback probe: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `SET LOCAL lock_timeout = '3s'`); err != nil {
		t.Fatalf("bound PostgreSQL rollback probe lock wait: %v", err)
	}
	if err := archivePostgresObjects(t.Context(), tx, backupSchema); err != nil {
		t.Fatalf("execute PostgreSQL rollback probe: %v", err)
	}
	if owner := postgresSchemaOwner(t, tx, "public"); owner != beforeOwner {
		t.Fatalf("public schema owner changed from %q to %q", beforeOwner, owner)
	}
	if count := postgresRelationCount(t, tx, "public"); count != 0 {
		t.Fatalf("public relations during archive = %d", count)
	}
	if count := postgresRelationCount(t, tx, backupSchema); count != beforeRelations {
		t.Fatalf("backup relations = %d, want %d", count, beforeRelations)
	}
	if count := postgresRoutineCount(t, tx, "public"); count != 0 {
		t.Fatalf("public routines during archive = %d", count)
	}
	if count := postgresRoutineCount(t, tx, backupSchema); count != beforeRoutines {
		t.Fatalf("backup routines = %d, want %d", count, beforeRoutines)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back PostgreSQL archive probe: %v", err)
	}
	if postgresSchemaExists(t, db, backupSchema) {
		t.Fatal("rollback probe left its backup schema behind")
	}
	if owner := postgresSchemaOwner(t, db, "public"); owner != beforeOwner {
		t.Fatalf("public schema owner after rollback = %q, want %q", owner, beforeOwner)
	}
	if count := postgresRelationCount(t, db, "public"); count != beforeRelations {
		t.Fatalf("public relations after rollback = %d, want %d", count, beforeRelations)
	}
	if count := postgresRoutineCount(t, db, "public"); count != beforeRoutines {
		t.Fatalf("public routines after rollback = %d, want %d", count, beforeRoutines)
	}
}

type postgresRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func postgresSchemaOwner(t *testing.T, db postgresRowQueryer, schema string) string {
	t.Helper()
	var owner string
	if err := db.QueryRowContext(t.Context(),
		`SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = $1`,
		schema,
	).Scan(&owner); err != nil {
		t.Fatalf("inspect PostgreSQL schema owner: %v", err)
	}
	return owner
}

func postgresRelationCount(t *testing.T, db postgresRowQueryer, schema string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*)
		 FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1
		   AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')`,
		schema,
	).Scan(&count); err != nil {
		t.Fatalf("inspect PostgreSQL relation count: %v", err)
	}
	return count
}

func postgresSchemaExists(t *testing.T, db postgresRowQueryer, schema string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`,
		schema,
	).Scan(&exists); err != nil {
		t.Fatalf("inspect PostgreSQL schema existence: %v", err)
	}
	return exists
}

func postgresRoutineCount(t *testing.T, db postgresRowQueryer, schema string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(),
		`SELECT count(*)
		 FROM pg_proc p
		 JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = $1
		   AND NOT EXISTS (
		     SELECT 1 FROM pg_depend d
		     WHERE d.classid = 'pg_proc'::regclass
		       AND d.objid = p.oid
		       AND d.deptype = 'e'
		   )`,
		schema,
	).Scan(&count); err != nil {
		t.Fatalf("inspect PostgreSQL routine count: %v", err)
	}
	return count
}
