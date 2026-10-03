package scaffoldcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadedDependencyRejectsStalePinAndReplacements(t *testing.T) {
	const backend = "example.com/framework/server"
	const version = "v0.0.0-20260910062837-f6d57f4fe47f"
	base := "module example.com/derived\n\ngo 1.25.0\n\nrequire " + backend + " " + version + "\n"
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"matching pin", base, true},
		{"stale pin", strings.ReplaceAll(base, version, "v0.1.0"), false},
		{"local replacement", base + "replace " + backend + " => ../server\n", false},
		{"remote replacement", base + "replace " + backend + " => example.com/other v0.1.0\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDependency([]byte(tc.raw), backend, version, "")
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestContractComparisonRejectsMissingCaptchaAndFileError(t *testing.T) {
	expected := []byte(`{"paths":{"/api/v1/auth/captcha":{"post":{}},"/api/v1/files/{id}":{"delete":{"responses":{"409":{}}}}}}`)
	if err := CompareOpenAPI(expected, []byte(`{ "paths": {"/api/v1/files/{id}":{"delete":{"responses":{"409":{}}}},"/api/v1/auth/captcha":{"post":{}}}}`)); err != nil {
		t.Fatal(err)
	}
	for _, actual := range []string{
		`{"paths":{"/api/v1/files/{id}":{"delete":{"responses":{"409":{}}}}}}`,
		`{"paths":{"/api/v1/auth/captcha":{"post":{}},"/api/v1/files/{id}":{"delete":{"responses":{}}}}}`,
	} {
		if err := CompareOpenAPI(expected, []byte(actual)); err == nil {
			t.Fatal("accepted incompatible contract")
		}
	}
}

func TestFixturesInstallOnlyInFreshDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := Install(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "scaffoldcheck", "auth_test.go")); err != nil {
		t.Fatal(err)
	}
	if err := Install(directory); err == nil {
		t.Fatal("fixture overwrote an existing directory")
	}
}
