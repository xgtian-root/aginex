package cli

import (
	"os"
	"path/filepath"
	"testing"
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

func TestRootCommandDoesNotExposeRuntimeInitialization(t *testing.T) {
	commands := make(map[string]struct{})
	for _, command := range newRootCommand().Commands() {
		commands[command.Name()] = struct{}{}
	}

	for _, forbidden := range []string{"bootstrap", "migrate"} {
		if _, ok := commands[forbidden]; ok {
			t.Fatalf("runtime initialization command %q is still registered", forbidden)
		}
	}
	for _, required := range []string{"check", "dev", "doctor", "generate", "new", "skills"} {
		if _, ok := commands[required]; !ok {
			t.Fatalf("developer command %q is not registered", required)
		}
	}
	dev, _, err := newRootCommand().Find([]string{"dev"})
	if err != nil {
		t.Fatal(err)
	}
	if reinitialize, _, err := dev.Find([]string{"reinitialize"}); err != nil ||
		reinitialize.Name() != "reinitialize" {
		t.Fatalf("dev reinitialize command = %#v, error = %v", reinitialize, err)
	}
	if reconcile, _, err := dev.Find([]string{"reconcile-storage-presentation"}); err != nil ||
		reconcile.Name() != "reconcile-storage-presentation" {
		t.Fatalf("dev reconcile command = %#v, error = %v", reconcile, err)
	}
}

func TestDevStartsWorkerOnlyForPostgresJobs(t *testing.T) {
	t.Setenv("AGINEX_JOBS_DRIVER", "disabled")
	if durableWorkerConfigured() {
		t.Fatal("disabled jobs unexpectedly start the durable worker")
	}
	t.Setenv("AGINEX_JOBS_DRIVER", "postgres")
	if !durableWorkerConfigured() {
		t.Fatal("postgres jobs did not start the durable worker")
	}
}
