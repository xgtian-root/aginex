package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/database"
)

func TestReadFrontmatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	content := "---\nname: example-skill\ndescription: This description explains when the example Skill should be used.\n---\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	name, description, err := readFrontmatter(path)
	if err != nil {
		t.Fatal(err)
	}
	if name != "example-skill" || description == "" {
		t.Fatalf("name = %q, description = %q", name, description)
	}
}

func TestReadFrontmatterRejectsExtraKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	content := "---\nname: example-skill\ndescription: This description is intentionally long enough for validation.\nversion: one\n---\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readFrontmatter(path); err == nil {
		t.Fatal("expected an unsupported key to fail")
	}
}

func TestMigrateCommandsReportAndAdvanceSQLiteVersion(t *testing.T) {
	t.Setenv("AGINEX_ENV", "test")
	t.Setenv("AGINEX_DATABASE_DRIVER", "sqlite")
	t.Setenv("AGINEX_DATABASE_DSN", filepath.Join(t.TempDir(), "aginex.db"))

	status := executeCLI(t, "migrate", "status")
	for _, expected := range []string{
		"driver=sqlite",
		"current=0",
		"latest=8",
		"applied=0",
		"pending=8",
		"rateLimitCurrent=0",
		"rateLimitLatest=1",
		"rateLimitPending=1",
		"idempotency=database",
		"idempotencyCurrent=0",
		"idempotencyLatest=1",
		"idempotencyPending=1",
		"jobs=disabled",
		"state=pending",
	} {
		if !strings.Contains(status, expected) {
			t.Fatalf("status output %q does not contain %q", status, expected)
		}
	}

	up := executeCLI(t, "migrate", "up")
	for _, expected := range []string{
		"driver=sqlite",
		"current=8",
		"latest=8",
		"rateLimitCurrent=1",
		"rateLimitLatest=1",
		"idempotency=database",
		"idempotencyCurrent=1",
		"idempotencyLatest=1",
		"jobs=disabled",
	} {
		if !strings.Contains(up, expected) {
			t.Fatalf("up output %q does not contain %q", up, expected)
		}
	}

	version := executeCLI(t, "migrate", "version")
	for _, expected := range []string{
		"driver=sqlite",
		"current=8",
		"latest=8",
		"rateLimitCurrent=1",
		"rateLimitLatest=1",
		"idempotency=database",
		"idempotencyCurrent=1",
		"idempotencyLatest=1",
		"jobs=disabled",
	} {
		if !strings.Contains(version, expected) {
			t.Fatalf("version output %q does not contain %q", version, expected)
		}
	}
}

func TestMigrateCommandsLeaveDisabledIdempotencyModuleUninstalled(t *testing.T) {
	t.Setenv("AGINEX_ENV", "test")
	t.Setenv("AGINEX_DATABASE_DRIVER", "sqlite")
	t.Setenv("AGINEX_DATABASE_DSN", filepath.Join(t.TempDir(), "aginex.db"))
	t.Setenv("AGINEX_IDEMPOTENCY_DRIVER", "disabled")

	up := executeCLI(t, "migrate", "up")
	if !strings.Contains(up, "idempotency=disabled") {
		t.Fatalf("up output %q does not report disabled idempotency", up)
	}
	status := executeCLI(t, "migrate", "status")
	for _, expected := range []string{
		"idempotency=disabled",
		"idempotencyCurrent=0",
		"idempotencyLatest=0",
		"idempotencyPending=0",
		"state=current",
	} {
		if !strings.Contains(status, expected) {
			t.Fatalf("status output %q does not contain %q", status, expected)
		}
	}
}

func TestBootstrapCommandIsRepeatSafeAndAudited(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "aginex.db")
	t.Setenv("AGINEX_ENV", "test")
	t.Setenv("AGINEX_DATABASE_DRIVER", "sqlite")
	t.Setenv("AGINEX_DATABASE_DSN", dsn)
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_EMAIL", " ADMIN@example.com ")
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_PASSWORD", "correct bootstrap password")

	executeCLI(t, "migrate", "up")
	for run := 1; run <= 2; run++ {
		output := executeCLI(t, "bootstrap")
		if !strings.Contains(output, "Bootstrap completed") {
			t.Fatalf("bootstrap run %d output = %q", run, output)
		}
	}

	db, err := database.Open(config.Database{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close bootstrap command database: %v", err)
		}
	})

	for model, want := range map[any]int64{
		&domain.User{}:         1,
		&domain.UserIdentity{}: 1,
		&domain.Role{}:         1,
	} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%T count = %d, want %d", model, count, want)
		}
	}
	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).
		Where(
			"actor_id = ? AND action = ? AND source = ?",
			"aginex-bootstrap",
			"system:bootstrap",
			"cli",
		).
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("bootstrap audit count = %d, want 2", auditCount)
	}
}

func executeCLI(t *testing.T, args ...string) string {
	t.Helper()
	var output bytes.Buffer
	command := newRootCommand()
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute %v: %v\n%s", args, err, output.String())
	}
	return output.String()
}
