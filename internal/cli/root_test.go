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
