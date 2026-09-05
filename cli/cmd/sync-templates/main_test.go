package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotDriftAndLocalEditProtection(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "cli", "templates")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "sources.json"), `["asset.txt"]`)
	source := filepath.Join(root, "asset.txt")
	write(source, "initial")
	if err := sync(root, true); err == nil {
		t.Fatal("missing snapshot passed check")
	}
	if _, err := os.Stat(filepath.Join(base, "_project")); !os.IsNotExist(err) {
		t.Fatal("check wrote snapshot")
	}
	if err := sync(root, false); err != nil {
		t.Fatal(err)
	}
	if err := sync(root, true); err != nil {
		t.Fatal(err)
	}
	write(source, "updated")
	if err := sync(root, true); err == nil {
		t.Fatal("source drift passed check")
	}
	snapshot := filepath.Join(base, "_project", "asset.txt")
	if got, _ := os.ReadFile(snapshot); string(got) != "initial" {
		t.Fatal("check overwrote snapshot")
	}
	if err := sync(root, false); err != nil {
		t.Fatal(err)
	}
	write(snapshot, "local edit")
	if err := sync(root, false); err == nil || !strings.Contains(err.Error(), "locally modified") {
		t.Fatalf("local edit error = %v", err)
	}
	if got, _ := os.ReadFile(snapshot); string(got) != "local edit" {
		t.Fatal("local edit overwritten")
	}
}

func TestSnapshotRejectsPrivateAssets(t *testing.T) {
	for _, name := range []string{".env", "admin/.env.local", "data/aginex.db", "admin/node_modules/a", "admin/.next/server.js"} {
		if !forbidden(name) {
			t.Errorf("private source allowed: %s", name)
		}
	}
	if forbidden(".env.example") {
		t.Fatal("example environment should be allowed")
	}
}
