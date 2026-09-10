package configcli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xgtian-root/aginex/server/internal/config"
)

func testRoot(t *testing.T) string {
	t.Helper()
	for _, s := range config.Catalog() {
		name := s.Name
		v, ok := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if ok {
				os.Setenv(name, v)
			} else {
				os.Unsetenv(name)
			}
		})
	}
	t.Setenv("NODE_ENV", "development")
	root := t.TempDir()
	t.Chdir(root)
	for _, d := range []string{"server", "admin", "data"} {
		if e := os.Mkdir(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	return root
}
func ptr(s string) *string { return &s }
func execute(t *testing.T, root string, r Request) Response {
	t.Helper()
	out, err := Execute(context.Background(), root, r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func apply(t *testing.T, root string, changes map[string]*string) Response {
	t.Helper()
	r := Request{Action: "prepare", Target: "server", Changes: changes}
	p := execute(t, root, r)
	r.Action = "commit"
	r.Expected = p.Expected
	r.Confirmed = true
	return execute(t, root, r)
}
func createSQLite(t *testing.T, path string) {
	t.Helper()
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("CREATE TABLE proof (id INTEGER)"); e != nil {
		t.Fatal(e)
	}
}
func TestBatchSourceAuditAndConflict(t *testing.T) {
	root := testRoot(t)
	os.WriteFile("server/.env", []byte("# preserved\nAGINEX_HTTP_ADDRESS=:8080\n"), 0600)
	req := Request{Action: "prepare", Target: "server", Changes: map[string]*string{"AGINEX_HTTP_ADDRESS": ptr(":8081"), "AGINEX_API_PUBLIC_URL": ptr("http://localhost:8081")}}
	p := execute(t, root, req)
	before, _ := os.ReadFile("server/.env")
	if strings.Contains(string(before), "8081") {
		t.Fatal("prepare wrote config")
	}
	req.Action = "commit"
	req.Expected = p.Expected
	r := execute(t, root, req)
	if !r.Saved {
		t.Fatal("not saved")
	}
	after, _ := os.ReadFile("server/.env")
	if !strings.Contains(string(after), "# preserved") {
		t.Fatal("lost comments")
	}
	info, _ := os.Stat("server/.env")
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions")
	}
	audits, _ := filepath.Glob(".cache/aginex/config-audit/*.json")
	if len(audits) != 1 {
		t.Fatal("missing audit")
	}
	if _, e := Execute(context.Background(), root, req); e == nil {
		t.Fatal("accepted stale commit")
	}
}
func TestInvalidBatchAndOverridesDoNotWrite(t *testing.T) {
	root := testRoot(t)
	for _, changes := range []map[string]*string{{"UNKNOWN": ptr("x")}, {"AGINEX_HTTP_ADDRESS": ptr(":8081"), "AGINEX_SESSION_TTL": ptr("-1s")}, {"AGINEX_CSRF_HEADER": ptr("Other")}} {
		if _, e := Execute(context.Background(), root, Request{Action: "prepare", Target: "server", Changes: changes}); e == nil {
			t.Fatal("accepted invalid candidate")
		}
	}
	t.Setenv("AGINEX_HTTP_ADDRESS", ":9000")
	if _, e := Execute(context.Background(), root, Request{Action: "prepare", Target: "server", Changes: map[string]*string{"AGINEX_HTTP_ADDRESS": ptr(":8000")}}); e == nil {
		t.Fatal("accepted process override")
	}
	os.WriteFile("admin/.env.local", []byte("PORT=3100\n"), 0600)
	if _, e := Execute(context.Background(), root, Request{Action: "prepare", Target: "admin", Changes: map[string]*string{"PORT": ptr("3200")}}); e == nil {
		t.Fatal("accepted Next override")
	}
	if _, e := os.Stat("server/.env"); !os.IsNotExist(e) {
		t.Fatal("invalid config wrote file")
	}
}
func TestDatabaseConfirmationAndMissingSQLite(t *testing.T) {
	root := testRoot(t)
	dsn := filepath.Join(root, "data", "candidate.db")
	req := Request{Action: "prepare", Target: "server", Changes: map[string]*string{"AGINEX_DATABASE_DRIVER": ptr("sqlite"), "AGINEX_DATABASE_DSN": &dsn}}
	if _, e := Execute(context.Background(), root, req); e == nil {
		t.Fatal("accepted absent database")
	}
	if _, e := os.Stat(dsn); !os.IsNotExist(e) {
		t.Fatal("connection test created database")
	}
	createSQLite(t, dsn)
	p := execute(t, root, req)
	if !p.DatabaseChanged {
		t.Fatal("database change unrecognized")
	}
	req.Action = "commit"
	req.Expected = p.Expected
	if _, e := Execute(context.Background(), root, req); e == nil {
		t.Fatal("unconfirmed database applied")
	}
	if _, e := os.Stat("server/.env"); !os.IsNotExist(e) {
		t.Fatal("saved before confirmation")
	}
	req.Confirmed = true
	execute(t, root, req)
	listing := execute(t, root, Request{Action: "list", Target: "server"})
	data, _ := json.Marshal(listing)
	if strings.Contains(string(data), dsn) {
		t.Fatal("DSN exposed")
	}
}
func TestManagedDatabaseAndSessionWriteRealSource(t *testing.T) {
	root := testRoot(t)
	a, b := filepath.Join(root, "data", "a.db"), filepath.Join(root, "data", "b.db")
	createSQLite(t, a)
	createSQLite(t, b)
	installation, e := config.NewManagedInstallation(config.Database{Driver: "sqlite", DSN: a}, strings.Repeat("s", 43))
	if e != nil {
		t.Fatal(e)
	}
	if e = config.CommitInstallation("data/aginex-config.json", installation); e != nil {
		t.Fatal(e)
	}
	apply(t, root, map[string]*string{"AGINEX_DATABASE_DSN": &b, "AGINEX_SESSION_SECRET": ptr(strings.Repeat("t", 43))})
	current, e := config.ReadInstallation("data/aginex-config.json")
	if e != nil {
		t.Fatal(e)
	}
	if current.Database.DSN != b || current.Revision != installation.Revision+1 || current.SessionSecret == installation.SessionSecret {
		t.Fatal("installation update incomplete")
	}
	if _, e := os.Stat("server/.env"); !os.IsNotExist(e) {
		t.Fatal("created dotenv shadow for managed fields")
	}
	audits, _ := filepath.Glob(".cache/aginex/config-audit/*.json")
	data, _ := os.ReadFile(audits[0])
	if strings.Contains(string(data), b) || strings.Contains(string(data), current.SessionSecret) {
		t.Fatal("audit leaked secret")
	}
}
func TestRecoveryAndConcurrentEditProtection(t *testing.T) {
	root := testRoot(t)
	dir := filepath.Join(root, ".cache", "aginex")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(root, "server", ".env")
	before := snapshot{path, true, []byte("PORT=1\n")}
	after := snapshot{path, true, []byte("PORT=2\n")}
	replace(after)
	journalPath := filepath.Join(dir, "config-transaction.json")
	writeJournal(journalPath, journal{Before: []snapshot{before}, After: []snapshot{after}})
	if e := Recover(root); e != nil {
		t.Fatal(e)
	}
	got, _ := readSnapshot(path)
	if !equalSnapshot(got, before) {
		t.Fatal("did not restore original")
	}
	writeJournal(journalPath, journal{Before: []snapshot{before}, After: []snapshot{after}})
	os.WriteFile(path, []byte("PORT=3\n"), 0600)
	if e := Recover(root); e == nil {
		t.Fatal("overwrote concurrent edit")
	}
}
func TestDatabaseCancellation(t *testing.T) {
	testRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := testDatabase(ctx, config.Database{Driver: "postgres", DSN: "postgres://user:secret@127.0.0.1:1/db"}); e == nil || strings.Contains(e.Error(), "secret") {
		t.Fatal("cancelled probe succeeded or leaked credential")
	}
}

func TestDatabaseDriverSwitchIntegration(t *testing.T) {
	postgres, mysql := os.Getenv("AGINEX_TEST_POSTGRES_DSN"), os.Getenv("AGINEX_TEST_MYSQL_DSN")
	if postgres == "" || mysql == "" {
		t.Skip("temporary PostgreSQL/MySQL connection strings not provided")
	}
	root := testRoot(t)
	a := filepath.Join(root, "data", "a.db")
	createSQLite(t, a)
	marker, e := config.NewEnvironmentInstallation("sqlite", strings.Repeat("k", 43))
	if e != nil {
		t.Fatal(e)
	}
	if e = config.CommitInstallation("data/aginex-config.json", marker); e != nil {
		t.Fatal(e)
	}
	os.WriteFile("server/.env", []byte("AGINEX_DATABASE_DRIVER=sqlite\nAGINEX_DATABASE_DSN="+a+"\n"), 0600)
	for _, db := range []config.Database{{Driver: "mysql", DSN: mysql}, {Driver: "postgres", DSN: postgres}} {
		apply(t, root, map[string]*string{"AGINEX_DATABASE_DRIVER": &db.Driver, "AGINEX_DATABASE_DSN": &db.DSN})
		current, e := config.ReadInstallation("data/aginex-config.json")
		if e != nil || current.Database.Driver != db.Driver || current.Database.DSN != "" {
			t.Fatalf("driver marker not synchronized: %v", e)
		}
		invalid := db
		invalid.DSN = "bad connection"
		if e := testDatabase(context.Background(), invalid); e == nil {
			t.Fatal("invalid connection accepted")
		}
	}
}
