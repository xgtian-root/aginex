package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

type devEnvelope struct {
	Root    string        `json:"root"`
	Request configRequest `json:"request"`
}

func controlDirectory(root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(os.TempDir(), "aginex-dev-"+strconv.Itoa(os.Getuid())+"-"+hex.EncodeToString(sum[:12]))
}
func canonicalRoot(root string) (string, error) { return filepath.EvalSymlinks(root) }
func connectDevConfig(root string) (configRunner, bool, error) {
	root, err := canonicalRoot(root)
	if err != nil {
		return nil, false, err
	}
	directory := controlDirectory(root)
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	if err := privateControlDirectory(directory, false); err != nil {
		return nil, false, err
	}
	socket := filepath.Join(directory, "control.sock")
	connection, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		if deadControlSocket(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cannot reach development supervisor: %w", err)
	}
	connection.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Minute}
	run := func(ctx context.Context, req configRequest) (configResponse, error) {
		reader, writer := io.Pipe()
		go func() { err := json.NewEncoder(writer).Encode(devEnvelope{root, req}); writer.CloseWithError(err) }()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://aginex/config", reader)
		if err != nil {
			return configResponse{}, err
		}
		defer reader.Close()
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return configResponse{}, fmt.Errorf("development supervisor request failed; inspect configuration before retrying: %w", err)
		}
		defer response.Body.Close()
		var result configResponse
		if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result); err != nil {
			return result, errors.New("invalid development supervisor response")
		}
		if result.Error != "" {
			return result, errors.New(result.Error)
		}
		return result, nil
	}
	return run, true, nil
}

type devChild struct {
	cmd     *exec.Cmd
	done    chan struct{}
	err     error
	ready   chan struct{}
	waiting chan struct{}
}
type devSupervisor struct {
	mu       sync.Mutex
	cmd      *cobra.Command
	root     string
	ctx      context.Context
	run      configRunner
	binaries map[string]string
	children map[string]*devChild
	running  map[string]configEntry
	adminURL string
	apiURL   string
}

func runDev(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	cmd.SetContext(ctx)
	root, err := projectRoot(".")
	if err != nil {
		return err
	}
	root, err = canonicalRoot(root)
	if err != nil {
		return err
	}
	directory := controlDirectory(root)
	if err := privateControlDirectory(directory, true); err != nil {
		return err
	}
	unlock, err := lockDevControl(directory)
	if err != nil {
		return err
	}
	defer unlock()
	socket := filepath.Join(directory, "control.sock")
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	run, cleanup, err := backendConfigRunner(cmd.Context(), cmd, root)
	if err != nil {
		return err
	}
	defer cleanup()
	s := &devSupervisor{cmd: cmd, root: root, ctx: cmd.Context(), run: run, binaries: map[string]string{}, children: map[string]*devChild{}, running: map[string]configEntry{}}
	defer s.stopAll()
	control := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/config" {
			http.NotFound(w, r)
			return
		}
		var envelope devEnvelope
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&envelope) != nil || envelope.Root != root {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		// Finish an accepted commit even if its requesting terminal disconnects.
		ctx, cancel := context.WithTimeout(s.ctx, 4*time.Minute)
		defer cancel()
		result, err := s.run(ctx, envelope.Request)
		if err == nil && result.Saved {
			err = s.restart(ctx, result.Target, result.Changes)
			result.Applied = err == nil
		}
		if err != nil {
			result.Error = err.Error()
		}
		if envelope.Request.Action == "list" {
			result.Running = s.running
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- control.Serve(listener) }()
	defer control.Close()
	s.mu.Lock()
	err = s.restart(cmd.Context(), "all", nil)
	s.mu.Unlock()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Development startup failed: %v\nSupervisor remains available for config changes.\n", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Development supervisor is active. Use aginex config in another terminal; Ctrl+C stops all managed processes.")
	select {
	case <-cmd.Context().Done():
		return nil
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func (s *devSupervisor) restart(ctx context.Context, target string, keys []string) error {
	restartServer, restartAdmin := target == "server" || target == "all", target == "admin" || target == "all"
	for _, key := range keys {
		switch key {
		case "PORT", "AGINEX_HTTP_ADDRESS", "AGINEX_API_PUBLIC_URL", "AGINEX_WEB_ORIGINS", "AGINEX_WEB_ORIGIN", "AGINEX_API_INTERNAL_URL", "NEXT_PUBLIC_API_URL":
			restartServer = true
			restartAdmin = true
		}
	}
	// A previously failed component must be retryable with the next valid update.
	if p := s.children["server"]; p == nil || p.exited() {
		restartServer = true
	}
	if p := s.children["admin"]; p == nil || p.exited() {
		restartAdmin = true
	}
	serverResult, err := s.run(ctx, configRequest{Action: "environment", Target: "server"})
	if err != nil {
		return err
	}
	adminResult, err := s.run(ctx, configRequest{Action: "environment", Target: "admin"})
	if err != nil {
		return err
	}
	serverEnv, adminEnv := serverResult.Environment, adminResult.Environment
	// Validate before stopping any working service.
	if _, err := s.run(ctx, configRequest{Action: "list", Target: "server"}); err != nil {
		return err
	}
	if restartServer {
		s.stop("worker")
		s.stop("server")
	}
	if restartAdmin {
		s.stop("admin")
	}
	var reservation net.Listener
	if restartAdmin {
		port, held, err := reserveDevAdminPort(adminEnv["PORT"])
		if err != nil {
			return err
		}
		reservation = held
		defer reservation.Close()
		adminEnv["PORT"] = strconv.Itoa(port)
		s.adminURL = "http://localhost:" + strconv.Itoa(port)
	}
	address := serverEnv["AGINEX_HTTP_ADDRESS"]
	if address == "" {
		address = ":8080"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("invalid API listen address")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	s.apiURL = "http://" + net.JoinHostPort(host, port)
	if strings.TrimSpace(serverEnv["AGINEX_WEB_ORIGINS"]) == "" && strings.TrimSpace(serverEnv["AGINEX_WEB_ORIGIN"]) == "" {
		serverEnv["AGINEX_WEB_ORIGINS"] = s.adminURL
		serverEnv["AGINEX_WEB_ORIGIN"] = s.adminURL
	}
	if strings.TrimSpace(serverEnv["AGINEX_API_PUBLIC_URL"]) == "" {
		serverEnv["AGINEX_API_PUBLIC_URL"] = s.apiURL
	}
	if strings.TrimSpace(adminEnv["AGINEX_API_INTERNAL_URL"]) == "" {
		adminEnv["AGINEX_API_INTERNAL_URL"] = s.apiURL
	}
	if restartServer {
		if err := s.startBackend(ctx, "server", serverEnv); err != nil {
			return err
		}
		if err := s.ready(ctx, "server", s.apiURL+"/health/ready"); err != nil {
			return err
		}
		if serverEnv["AGINEX_JOBS_DRIVER"] == "postgres" {
			if err := s.startBackend(ctx, "worker", serverEnv); err != nil {
				return err
			}
		}
		if err := s.capture(ctx, "server", serverEnv); err != nil {
			return err
		}
	}
	if restartAdmin {
		if err := reservation.Close(); err != nil {
			return err
		}
		p := exec.Command("pnpm", "dev:admin", "--hostname", "localhost", "--port", adminEnv["PORT"])
		p.Dir = s.root
		p.Env = environmentSlice(adminEnv)
		if err := s.start("admin", p); err != nil {
			return err
		}
		if err := s.ready(ctx, "admin", s.adminURL); err != nil {
			return err
		}
		if err := s.capture(ctx, "admin", adminEnv); err != nil {
			return err
		}
	}
	if worker := s.children["worker"]; worker != nil {
		mode, err := s.apiMode(ctx)
		if err != nil {
			return err
		}
		signal := worker.ready
		if mode == "setup" {
			signal = worker.waiting
		}
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		select {
		case <-worker.done:
			return errors.New("worker exited before becoming ready")
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("worker did not report readiness")
		case <-signal:
		}
	}

	fmt.Fprintf(s.cmd.OutOrStdout(), "Aginex ready: admin %s, API %s\n", s.adminURL, s.apiURL)
	return nil
}
func environmentSlice(env map[string]string) []string {
	keys := []string{}
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []string{}
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
func (s *devSupervisor) startBackend(ctx context.Context, name string, env map[string]string) error {
	binary := s.binaries[name]
	if binary == "" {
		p, cleanup, err := backendProcess(ctx, s.cmd, s.root, name)
		if err != nil {
			return err
		}
		binary = p.Path
		s.binaries[name] = binary
		// Temporary build directories live for the supervisor lifetime.
		go func() { <-s.ctx.Done(); cleanup() }()
	}
	p := exec.Command(binary)
	p.Dir = s.root
	p.Env = environmentSlice(env)
	return s.start(name, p)
}
func (s *devSupervisor) start(name string, p *exec.Cmd) error {
	child := &devChild{cmd: p, done: make(chan struct{}), ready: make(chan struct{}), waiting: make(chan struct{})}
	p.Stdout = s.cmd.OutOrStdout()
	p.Stderr = s.cmd.ErrOrStderr()
	if name == "worker" {
		p.Stdout = &workerObserver{destination: p.Stdout, ready: child.ready, waiting: child.waiting}
	}
	configureProcessTree(p)
	if err := p.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	s.children[name] = child
	go func() { child.err = p.Wait(); cleanupExitedProcessTree(p); close(child.done) }()
	return nil
}

type workerObserver struct {
	destination            io.Writer
	pending                string
	ready, waiting         chan struct{}
	readyOnce, waitingOnce sync.Once
}

func (w *workerObserver) Write(data []byte) (int, error) {
	n, err := w.destination.Write(data)
	w.pending += string(data)
	for {
		line, rest, ok := strings.Cut(w.pending, "\n")
		if !ok {
			break
		}
		w.pending = rest
		var event struct {
			Message string `json:"msg"`
		}
		if json.Unmarshal([]byte(line), &event) == nil {
			switch event.Message {
			case "Aginex worker started":
				w.readyOnce.Do(func() { close(w.ready) })
			case "Aginex worker waiting for API initialization":
				w.waitingOnce.Do(func() { close(w.waiting) })
			}
		}
	}
	if len(w.pending) > 1<<20 {
		w.pending = ""
	}
	return n, err
}
func (s *devSupervisor) apiMode(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+"/api/v1/system/mode", nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var result struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Mode != "setup" && result.Mode != "application" {
		return "", errors.New("API mode is not ready")
	}
	return result.Mode, nil
}

func (p *devChild) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
func (s *devSupervisor) stop(name string) {
	p := s.children[name]
	if p == nil {
		return
	}
	stopProcessTree(p.cmd, p.done)
	delete(s.children, name)
	for key := range s.running {
		if strings.HasPrefix(key, name+"/") {
			delete(s.running, key)
		}
	}
}
func (s *devSupervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop("worker")
	s.stop("server")
	s.stop("admin")
}
func (s *devSupervisor) ready(ctx context.Context, name, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if p := s.children[name]; p == nil || p.exited() {
			return fmt.Errorf("%s exited before becoming ready", name)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not become ready before the deadline", name)
		case <-ticker.C:
		}
	}
}
func (s *devSupervisor) capture(ctx context.Context, target string, env map[string]string) error {
	result, err := s.run(ctx, configRequest{Action: "list", Target: target})
	if err != nil {
		return err
	}
	for _, e := range result.Entries {
		if v, ok := env[e.Name]; ok && v != "" {
			if e.Secret {
				v = "[redacted]"
			}
			if e.Value != v {
				e.Source = "dev automatic"
			}
			e.Value = v
		}
		s.running[target+"/"+e.Name] = e
	}
	return nil
}
