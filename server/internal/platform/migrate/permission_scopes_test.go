package migrate

import (
	"context"
	"database/sql"
	"testing"
)

func TestSQLitePermissionScopeMigrationUpgradesPreviousVersion(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 3)
	insertLegacyRolePermission(t, db)

	upSQLiteTo(t, db, 4)

	var scope string
	if err := db.QueryRow(`
		SELECT scope
		FROM role_permissions
		WHERE role_id = ? AND permission_id = ?
	`, "role-1", "permission-1").Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if scope != "own" {
		t.Fatalf("legacy permission scope = %q, want own", scope)
	}
	if _, err := db.Exec(`
		UPDATE role_permissions
		SET scope = 'invalid'
		WHERE role_id = ? AND permission_id = ?
	`, "role-1", "permission-1"); err == nil {
		t.Fatal("invalid permission scope update succeeded")
	}
}

func TestSQLitePermissionScopeMigrationRollsBackToPreviousVersion(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 4)
	insertScopedRolePermission(t, db, "all")

	provider, err := newProvider(db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(context.Background(), 3); err != nil {
		t.Fatal(err)
	}

	if sqliteColumns(t, db, "role_permissions")["scope"] {
		t.Fatal("role_permissions retained rolled-back scope column")
	}
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM role_permissions
		WHERE role_id = ? AND permission_id = ?
	`, "role-1", "permission-1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("role permission rows after rollback = %d, want 1", count)
	}
}

func insertLegacyRolePermission(t *testing.T, db *sql.DB) {
	t.Helper()
	insertPermissionFixture(t, db)
	if _, err := db.Exec(`
		INSERT INTO role_permissions (role_id, permission_id)
		VALUES (?, ?)
	`, "role-1", "permission-1"); err != nil {
		t.Fatal(err)
	}
}

func insertScopedRolePermission(t *testing.T, db *sql.DB, scope string) {
	t.Helper()
	insertPermissionFixture(t, db)
	if _, err := db.Exec(`
		INSERT INTO role_permissions (role_id, permission_id, scope)
		VALUES (?, ?, ?)
	`, "role-1", "permission-1", scope); err != nil {
		t.Fatal(err)
	}
}

func insertPermissionFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO roles (id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, "role-1", "role-1", "", "2026-07-30T00:00:00Z", "2026-07-30T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO permissions (id, code, description, created_at)
		VALUES (?, ?, ?, ?)
	`, "permission-1", "files:read", "", "2026-07-30T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}
