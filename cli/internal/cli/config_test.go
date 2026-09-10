package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func configTestCommand() (*cobra.Command, *bytes.Buffer) {
	out := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd, out
}
func TestDatabaseChangeCannotApplyWithoutTerminal(t *testing.T) {
	cmd, out := configTestCommand()
	commits := 0
	run := func(_ context.Context, r configRequest) (configResponse, error) {
		switch r.Action {
		case "list":
			return configResponse{Entries: []configEntry{{Name: "AGINEX_DATABASE_DSN", Secret: true}}}, nil
		case "prepare":
			return configResponse{DatabaseChanged: true, DatabaseTarget: "sqlite candidate.db", Expected: "token"}, nil
		case "commit":
			commits++
		}
		return configResponse{}, nil
	}
	err := changeConfig(cmd, run, []string{"server", "AGINEX_DATABASE_DSN=candidate.db"}, false)
	if err == nil || commits != 0 {
		t.Fatal("noninteractive database confirmation bypassed")
	}
	if !strings.Contains(out.String(), "Connection test succeeded") {
		t.Fatal("missing connection result")
	}
}
func TestBatchSetAndRestartFailureReport(t *testing.T) {
	cmd, out := configTestCommand()
	seen := map[string]*string{}
	run := func(_ context.Context, r configRequest) (configResponse, error) {
		switch r.Action {
		case "list":
			return configResponse{Entries: []configEntry{{Name: "PORT"}, {Name: "NEXT_PUBLIC_API_URL"}}}, nil
		case "prepare":
			seen = r.Changes
			return configResponse{Expected: "token"}, nil
		case "commit":
			if r.Expected != "token" {
				t.Fatal("missing preview fingerprint")
			}
			return configResponse{Saved: true}, errors.New("admin failed")
		}
		return configResponse{}, nil
	}
	err := changeConfig(cmd, run, []string{"admin", "PORT=3300", "NEXT_PUBLIC_API_URL=https://api.example"}, false)
	if err == nil || len(seen) != 2 || !strings.Contains(out.String(), "saved, but application restart failed") {
		t.Fatal("save and apply states were conflated")
	}
}
func TestNoSupervisorAndControlIsolation(t *testing.T) {
	root := t.TempDir()
	_, online, err := connectDevConfig(root)
	if err != nil || online {
		t.Fatalf("unexpected supervisor: %v", err)
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	directory := controlDirectory(canonical)
	if err := os.Mkdir(directory, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	if _, _, err := connectDevConfig(root); err == nil {
		t.Fatal("accepted public control directory")
	}
}
func TestDevReadyFailsOnChildExitAndHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	done := make(chan struct{})
	s := &devSupervisor{children: map[string]*devChild{"server": {done: done}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := s.ready(ctx, "server", server.URL); err == nil {
		t.Fatal("503 counted as ready")
	}
	close(done)
	if err := s.ready(context.Background(), "server", server.URL); err == nil {
		t.Fatal("exited child counted as ready")
	}
}
func TestConfigJSONKeepsRunningAndSavedSeparate(t *testing.T) {
	cmd, out := configTestCommand()
	r := configResponse{Entries: []configEntry{{Name: "PORT", Value: "3300", SavedValue: "3300"}}, Running: map[string]configEntry{"admin/PORT": {Value: "3000"}}}
	if err := printConfig(cmd, r, true); err != nil {
		t.Fatal(err)
	}
	var decoded configResponse
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Running["admin/PORT"].Value == decoded.Entries[0].Value {
		t.Fatal("lost running distinction")
	}
}
func TestProcessTreeStopsDescendants(t *testing.T) {
	if os.Getenv("AGINEX_TREE_CHILD") == "1" {
		child := exec.Command("sleep", "120")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		os.WriteFile(os.Getenv("AGINEX_TREE_PID"), []byte(strings.TrimSpace(stringInt(child.Process.Pid))), 0600)
		for {
			time.Sleep(time.Second)
		}
	}
	root := t.TempDir()
	pidFile := filepath.Join(root, "pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessTreeStopsDescendants$")
	cmd.Env = append(os.Environ(), "AGINEX_TREE_CHILD=1", "AGINEX_TREE_PID="+pidFile)
	configureProcessTree(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	defer stopProcessTree(cmd, done)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	stopProcessTree(cmd, done)
	// kill -0 tests only the PID recorded by this test child.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("kill", "-0", strings.TrimSpace(string(data))).Run() != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("descendant remained alive")
}
func stringInt(n int) string { var b bytes.Buffer; json.NewEncoder(&b).Encode(n); return b.String() }

func TestWorkerObserverWaitsForCompleteReadinessEvent(t *testing.T) {
	ready, waiting := make(chan struct{}), make(chan struct{})
	observer := &workerObserver{destination: &bytes.Buffer{}, ready: ready, waiting: waiting}
	observer.Write([]byte(`{"msg":"Aginex worker sta`))
	select {
	case <-ready:
		t.Fatal("partial log marked ready")
	default:
	}
	observer.Write([]byte("rted\"}\n"))
	select {
	case <-ready:
	default:
		t.Fatal("readiness not observed")
	}
	observer.Write([]byte("{\"msg\":\"Aginex worker started\"}\n{\"msg\":\"Aginex worker waiting for API initialization\"}\n"))
	select {
	case <-waiting:
	default:
		t.Fatal("waiting state not observed")
	}
}
