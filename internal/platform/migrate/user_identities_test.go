package migrate

import (
	"context"
	"database/sql"
	"testing"
)

const (
	legacyUserID       = "51cf668c-8c2d-4212-a85c-c67f84bb2516"
	legacyPasswordHash = "$2a$12$legacy-password-hash"
)

func TestSQLiteUserIdentityMigrationInstallsFromEmptyDatabase(t *testing.T) {
	db := openSQLite(t)
	if err := Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}

	userColumns := sqliteColumnDefinitions(t, db, "users")
	if _, ok := userColumns["deleted_at"]; !ok {
		t.Fatal("users is missing deleted_at")
	}
	passwordColumn, ok := userColumns["password_hash"]
	if !ok {
		t.Fatal("users is missing compatibility password_hash")
	}
	if passwordColumn.notNull {
		t.Fatal("users.password_hash remains NOT NULL")
	}

	identityColumns := sqliteColumnDefinitions(t, db, "user_identities")
	for _, name := range []string{
		"id",
		"user_id",
		"provider",
		"subject",
		"credential_hash",
		"status",
		"created_at",
		"updated_at",
	} {
		if _, ok := identityColumns[name]; !ok {
			t.Errorf("user_identities is missing %s", name)
		}
	}

	if _, err := db.Exec(`
		INSERT INTO users (
			id, email, display_name, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`,
		legacyUserID,
		"fresh@example.com",
		"Fresh user",
		"active",
		"2026-07-31T00:00:00Z",
		"2026-07-31T00:00:00Z",
	); err != nil {
		t.Fatalf("insert user without legacy credential: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_identities (
			id, user_id, provider, subject, credential_hash, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"4c244bf1-31ea-4aa2-b1aa-0acdb0a33409",
		legacyUserID,
		"password",
		"fresh@example.com",
		"hash",
		"active",
		"2026-07-31T00:00:00Z",
		"2026-07-31T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO user_identities (
			id, user_id, provider, subject, credential_hash, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"a978037e-2949-4b7f-bb40-974b99e8227d",
		legacyUserID,
		"password",
		"fresh@example.com",
		"different-hash",
		"active",
		"2026-07-31T00:00:00Z",
		"2026-07-31T00:00:00Z",
	); err == nil {
		t.Fatal("duplicate provider and subject was accepted")
	}
	assertSQLiteForeignKeysValid(t, db)
}

func TestSQLiteUserIdentityMigrationUpgradesPreviousVersion(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 6)
	enableSQLiteForeignKeys(t, db)
	insertLegacyUserWithSession(t, db)

	upSQLiteTo(t, db, 7)

	var (
		identityID string
		userID     string
		provider   string
		subject    string
		hash       string
		status     string
	)
	if err := db.QueryRow(`
		SELECT id, user_id, provider, subject, credential_hash, status
		FROM user_identities
		WHERE provider = ? AND subject = ?
	`, "password", "legacy@example.com").Scan(
		&identityID,
		&userID,
		&provider,
		&subject,
		&hash,
		&status,
	); err != nil {
		t.Fatal(err)
	}
	if identityID != legacyUserID || userID != legacyUserID {
		t.Fatalf("identity IDs = %q / %q, want migrated user ID", identityID, userID)
	}
	if provider != "password" || subject != "legacy@example.com" ||
		hash != legacyPasswordHash || status != "active" {
		t.Fatalf(
			"migrated identity = provider %q subject %q hash %q status %q",
			provider,
			subject,
			hash,
			status,
		)
	}

	var retainedHash sql.NullString
	if err := db.QueryRow(
		"SELECT password_hash FROM users WHERE id = ?",
		legacyUserID,
	).Scan(&retainedHash); err != nil {
		t.Fatal(err)
	}
	if !retainedHash.Valid || retainedHash.String != legacyPasswordHash {
		t.Fatalf("compatibility hash = %#v", retainedHash)
	}
	var sessionCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		legacyUserID,
	).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 {
		t.Fatalf("legacy sessions = %d, want 1", sessionCount)
	}
	assertSQLiteForeignKeysValid(t, db)
}

func TestSQLiteUserIdentityMigrationRollsBackToPreviousVersion(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 6)
	enableSQLiteForeignKeys(t, db)
	insertLegacyUserWithSession(t, db)
	upSQLiteTo(t, db, 7)

	const replacementHash = "$2a$12$replacement-password-hash"
	if _, err := db.Exec(
		"UPDATE user_identities SET credential_hash = ? WHERE user_id = ? AND provider = ?",
		replacementHash,
		legacyUserID,
		"password",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"UPDATE users SET password_hash = NULL WHERE id = ?",
		legacyUserID,
	); err != nil {
		t.Fatal(err)
	}

	provider, err := newProvider(db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(context.Background(), 6); err != nil {
		t.Fatal(err)
	}

	if sqliteTableExists(t, db, "user_identities") {
		t.Fatal("user_identities remains after rollback")
	}
	userColumns := sqliteColumnDefinitions(t, db, "users")
	if _, ok := userColumns["deleted_at"]; ok {
		t.Fatal("users.deleted_at remains after rollback")
	}
	if !userColumns["password_hash"].notNull {
		t.Fatal("users.password_hash is nullable after rollback")
	}
	var passwordHash string
	if err := db.QueryRow(
		"SELECT password_hash FROM users WHERE id = ?",
		legacyUserID,
	).Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	if passwordHash != replacementHash {
		t.Fatalf("rolled-back password hash = %q, want latest identity credential", passwordHash)
	}
	var sessionCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		legacyUserID,
	).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 {
		t.Fatalf("legacy sessions after rollback = %d, want 1", sessionCount)
	}
	assertSQLiteForeignKeysValid(t, db)
}

type sqliteColumnDefinition struct {
	notNull bool
}

func sqliteColumnDefinitions(
	t *testing.T,
	db *sql.DB,
	table string,
) map[string]sqliteColumnDefinition {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	columns := make(map[string]sqliteColumnDefinition)
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue any
			primaryKey   int
		)
		if err := rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			t.Fatal(err)
		}
		columns[name] = sqliteColumnDefinition{notNull: notNull == 1}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func insertLegacyUserWithSession(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (
			id, email, display_name, password_hash, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		legacyUserID,
		" Legacy@Example.com ",
		"Legacy user",
		legacyPasswordHash,
		"active",
		"2026-07-30T00:00:00Z",
		"2026-07-30T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO sessions (
			id, user_id, token_hash, expires_at, created_at, last_seen_at,
			ip_address, user_agent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"3aa3506b-605f-4a0a-9311-a18731af74e3",
		legacyUserID,
		"1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		"2026-08-01T00:00:00Z",
		"2026-07-30T00:00:00Z",
		"2026-07-30T00:00:00Z",
		"127.0.0.1",
		"migration-test",
	); err != nil {
		t.Fatal(err)
	}
}

func enableSQLiteForeignKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
}

func assertSQLiteForeignKeysValid(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("SQLite foreign_key_check reported a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}
