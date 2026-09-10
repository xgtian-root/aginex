package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerDotenvReloadAndProcessPrecedence(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	os.Mkdir("server", 0700)
	os.WriteFile("server/go.mod", nil, 0600)
	os.WriteFile(".env", []byte("AGINEX_HTTP_ADDRESS=:9999\n"), 0600)
	os.WriteFile(filepath.Join("server", ".env"), []byte("AGINEX_HTTP_ADDRESS=:8088\n"), 0600)
	e, err := RuntimeEnvironment()
	if err != nil || e["AGINEX_HTTP_ADDRESS"] != ":8088" {
		t.Fatalf("dotenv not read: %v", err)
	}
	os.WriteFile("server/.env", []byte("AGINEX_HTTP_ADDRESS=:8089\n"), 0600)
	e, _ = RuntimeEnvironment()
	if e["AGINEX_HTTP_ADDRESS"] != ":8089" {
		t.Fatal("cached dotenv")
	}
	t.Setenv("AGINEX_HTTP_ADDRESS", ":8090")
	e, _ = RuntimeEnvironment()
	if e["AGINEX_HTTP_ADDRESS"] != ":8090" {
		t.Fatal("process environment lost precedence")
	}
}
