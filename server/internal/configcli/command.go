// Package configcli implements the private subprocess protocol used by aginex config.
package configcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xgtian-root/aginex/server/framework/envfile"
	"github.com/xgtian-root/aginex/server/internal/config"
)

type Request struct {
	Action    string             `json:"action"`
	Target    string             `json:"target"`
	Changes   map[string]*string `json:"changes,omitempty"`
	Expected  string             `json:"expected,omitempty"`
	Confirmed bool               `json:"confirmed,omitempty"`
}
type Entry struct {
	config.Setting
	Value      string `json:"value"`
	SavedValue string `json:"savedValue"`
	Source     string `json:"source"`
	SaveSource string `json:"saveSource"`
	Explicit   bool   `json:"explicit"`
	Overridden bool   `json:"overridden"`
}
type Response struct {
	Entries         []Entry           `json:"entries,omitempty"`
	Expected        string            `json:"expected,omitempty"`
	DatabaseChanged bool              `json:"databaseChanged,omitempty"`
	DatabaseTarget  string            `json:"databaseTarget,omitempty"`
	Saved           bool              `json:"saved,omitempty"`
	Changes         []string          `json:"changes,omitempty"`
	Target          string            `json:"target,omitempty"`
	Environment     map[string]string `json:"environment,omitempty"`
	Error           string            `json:"error,omitempty"`
	Applied         bool              `json:"applied,omitempty"`
	Message         string            `json:"message,omitempty"`
}
type snapshot struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Data   []byte `json:"data"`
}
type candidate struct {
	before      []snapshot
	after       []snapshot
	env         map[string]string
	stored      *config.Installation
	state       config.State
	dbChanged   bool
	keys        []string
	token       string
	installPath string
}

func Execute(ctx context.Context, root string, req Request) (Response, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Response{}, err
	}
	if req.Target != "server" && req.Target != "admin" && !(req.Action == "list" && req.Target == "") {
		return Response{}, errors.New("target must be server or admin")
	}
	if req.Action == "list" && req.Target == "" {
		var out Response
		for _, t := range []string{"server", "admin"} {
			r, e := Execute(ctx, root, Request{Action: "list", Target: t})
			if e != nil {
				return out, e
			}
			out.Entries = append(out.Entries, r.Entries...)
		}
		return out, nil
	}
	if _, e := os.Stat(filepath.Join(root, ".cache", "aginex", "config-transaction.json")); e == nil {
		return Response{}, errors.New("pending configuration transaction; run aginex config recover first")
	}
	env, sources, files, err := readEnvironment(root, req.Target)
	if err != nil {
		return Response{}, err
	}
	if req.Action == "environment" {
		return Response{Environment: env}, nil
	}
	path := config.InstallationPath(env)
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	var installation *config.Installation
	if req.Target == "server" {
		st, e := readSnapshot(path)
		if e != nil {
			return Response{}, e
		}
		files = append(files, st)
		if st.Exists {
			v, e := config.ReadInstallation(path)
			if e != nil {
				return Response{}, errors.New("installation file is invalid; repair it before using config")
			}
			installation = &v
		}
	}
	entries := entriesFor(req.Target, env, sources, installation, path, files[0])
	if req.Action == "list" {
		return Response{Entries: entries}, nil
	}
	if req.Action != "prepare" && req.Action != "commit" {
		return Response{}, errors.New("unknown config action")
	}
	c, err := prepare(root, req, env, entries, files, installation, path)
	if err != nil {
		return Response{}, err
	}
	result := Response{Expected: c.token, DatabaseChanged: c.dbChanged, Changes: c.keys, Target: req.Target}
	if c.dbChanged {
		result.DatabaseTarget = databaseTarget(c.state.Config.Database)
		if err := testDatabase(ctx, c.state.Config.Database); err != nil {
			return result, err
		}
	}
	if req.Action == "prepare" {
		return result, nil
	}
	if req.Expected == "" || req.Expected != c.token {
		return result, errors.New("configuration changed since preview; run the command again")
	}
	if c.dbChanged && !req.Confirmed {
		return result, errors.New("database change requires confirmation after a successful connection test")
	}
	if err := commit(root, c); err != nil {
		return result, err
	}
	result.Saved = true
	return result, nil
}
func readEnvironment(root, target string) (map[string]string, map[string]string, []snapshot, error) {
	base := filepath.Join(root, target, ".env")
	paths := []string{base}
	if target == "admin" {
		mode := os.Getenv("NODE_ENV")
		if mode == "" {
			mode = "development"
		}
		if mode != "development" && mode != "production" && mode != "test" {
			return nil, nil, nil, errors.New("unsupported NODE_ENV")
		}
		paths = append(paths, filepath.Join(root, target, ".env."+mode))
		if mode != "test" {
			paths = append(paths, filepath.Join(root, target, ".env.local"))
		}
		paths = append(paths, filepath.Join(root, target, ".env."+mode+".local"))
	}
	env := map[string]string{}
	sources := map[string]string{}
	files := []snapshot{}
	for _, p := range paths {
		f, err := readSnapshot(p)
		if err != nil {
			return nil, nil, nil, err
		}
		files = append(files, f)
		if target == "admin" {
			for i, b := range f.Data {
				if b == '$' && (i == 0 || f.Data[i-1] != '\\') {
					return nil, nil, nil, fmt.Errorf("%s uses variable expansion; config commands require literal values with escaped dollars", p)
				}
			}
		}
		values, err := envfile.Parse(f.Data)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		for k, v := range values {
			env[k] = v
			sources[k] = p
		}
	}
	for k, v := range envfile.Environment() {
		env[k] = v
		sources[k] = "process environment"
	}
	return env, sources, files, nil
}
func entriesFor(target string, env, sources map[string]string, stored *config.Installation, path string, base snapshot) []Entry {
	saved, _ := envfile.Parse(base.Data)
	out := []Entry{}
	for _, s := range config.Catalog() {
		if s.Target != target {
			continue
		}
		v, explicit := env[s.Name]
		src := sources[s.Name]
		save := base.Path
		sv := saved[s.Name]
		if stored != nil {
			switch s.Name {
			case "AGINEX_DATABASE_DRIVER", "AGINEX_DATABASE_DSN":
				if stored.Database.Source == config.DatabaseSourceManaged {
					if s.Name == "AGINEX_DATABASE_DRIVER" {
						v = stored.Database.Driver
					} else {
						v = stored.Database.DSN
					}
					src = path
					save = path
					sv = v
					explicit = true
				}
			case "AGINEX_SESSION_SECRET":
				if strings.TrimSpace(v) == "" {
					v = stored.SessionSecret
					sv = v
					src = path
					save = path
					explicit = true
				}
			}
		}

		if stored != nil && strings.HasPrefix(s.Name, "AGINEX_STORAGE_") && s.Name != "AGINEX_STORAGE_LOCAL_ROOT" && s.Name != "AGINEX_STORAGE_ENDPOINT_ALLOWLIST" {
			if _, managedByEnvironment := env["AGINEX_STORAGE_DRIVER"]; !managedByEnvironment {
				if active, ok := config.ActiveStorageProfile(config.StorageProfileSet{ActiveProfileID: stored.ActiveProfileID, Profiles: stored.Profiles}); ok {
					storage := active.StorageConfig()
					values := map[string]string{"AGINEX_STORAGE_DRIVER": storage.Driver, "AGINEX_STORAGE_BUCKET": storage.Bucket, "AGINEX_STORAGE_REGION": storage.Region, "AGINEX_STORAGE_ENDPOINT": storage.Endpoint, "AGINEX_STORAGE_ACCESS_BASE_URL": storage.AccessBaseURL, "AGINEX_STORAGE_ACCESS_KEY_ID": storage.AccessKeyID, "AGINEX_STORAGE_ACCESS_KEY_SECRET": storage.AccessKeySecret}
					if value, ok := values[s.Name]; ok {
						v = value
						src = path + " (console-managed storage)"
						explicit = true
						s.Requirement = "set AGINEX_STORAGE_DRIVER explicitly to use environment storage; console profiles are not edited"
					}
				}
			}
		}
		if src == "" {
			src = "default"
		}
		if s.Name == "AGINEX_SESSION_SECRET" && v == "" {
			v = "[generated at startup]"
			src = "generated at startup"
		}
		if strings.TrimSpace(v) == "" {
			v = s.Default
		}
		if s.Name == "AGINEX_WEB_ORIGINS" && strings.TrimSpace(env[s.Name]) == "" && strings.TrimSpace(env["AGINEX_WEB_ORIGIN"]) != "" {
			v = env["AGINEX_WEB_ORIGIN"]
			src = sources["AGINEX_WEB_ORIGIN"] + " (AGINEX_WEB_ORIGIN)"
		}
		overridden := src != "default" && src != "generated at startup" && src != save && !strings.HasSuffix(src, "(console-managed storage)")
		if s.Secret {
			if v != "" {
				v = "[redacted]"
			}
			if sv != "" {
				sv = "[redacted]"
			}
		}
		out = append(out, Entry{s, v, sv, src, save, explicit, overridden})
	}
	return out
}
func prepare(root string, req Request, env map[string]string, entries []Entry, files []snapshot, stored *config.Installation, path string) (candidate, error) {
	c := candidate{before: files, env: envfile.Merge(env, nil), stored: stored, installPath: path}
	if len(req.Changes) == 0 {
		return c, errors.New("provide at least one setting")
	}
	definitions := map[string]Entry{}
	for _, e := range entries {
		definitions[e.Name] = e
	}
	edits := map[string]*string{}
	installationChanged := false
	if stored != nil {
		copy := *stored
		c.stored = &copy
	}
	oldDriver, oldDSN := env["AGINEX_DATABASE_DRIVER"], env["AGINEX_DATABASE_DSN"]
	if stored != nil && stored.Database.Source == config.DatabaseSourceManaged {
		oldDriver = stored.Database.Driver
		oldDSN = stored.Database.DSN
	}
	for key, value := range req.Changes {
		item, ok := definitions[key]
		if !ok {
			return c, fmt.Errorf("unknown %s setting: %s", req.Target, key)
		}
		if item.Overridden {
			return c, fmt.Errorf("%s is overridden by %s; change or remove that override first", key, item.Source)
		}
		if value != nil {
			if err := item.Check(*value); err != nil {
				return c, err
			}
		}
		if item.SaveSource == path {
			if value == nil || *value == "" {
				return c, fmt.Errorf("%s is required in the installation file", key)
			}
			switch key {
			case "AGINEX_DATABASE_DRIVER":
				c.stored.Database.Driver = *value
			case "AGINEX_DATABASE_DSN":
				c.stored.Database.DSN = *value
			case "AGINEX_SESSION_SECRET":
				c.stored.SessionSecret = *value
			}
			installationChanged = true
		} else {
			edits[key] = value
			if value == nil {
				delete(c.env, key)
			} else {
				c.env[key] = *value
			}
		}
		c.keys = append(c.keys, key)
	}
	sort.Strings(c.keys)
	data, err := envfile.Edit(files[0].Data, edits)
	if err != nil {
		return c, err
	}
	if len(edits) > 0 {
		c.after = append(c.after, snapshot{files[0].Path, true, data})
	}

	if req.Target == "server" {
		nextPath := config.InstallationPath(c.env)
		if !filepath.IsAbs(nextPath) {
			nextPath = filepath.Join(root, nextPath)
		}
		if nextPath != path {
			if installationChanged {
				return c, errors.New("change AGINEX_CONFIG_FILE separately from installation-owned fields")
			}
			f, e := readSnapshot(nextPath)
			if e != nil {
				return c, e
			}
			c.before = append(c.before, f)
			c.stored = nil
			if f.Exists {
				next, e := config.ReadInstallation(nextPath)
				if e != nil {
					return c, errors.New("target installation file is invalid")
				}
				c.stored = &next
			}
			path = nextPath
			c.installPath = path
		}
	}
	if c.stored != nil && c.stored.Database.Source == config.DatabaseSourceEnvironment && c.stored.Database.Driver != c.env["AGINEX_DATABASE_DRIVER"] {
		c.stored.Database.Driver = c.env["AGINEX_DATABASE_DRIVER"]
		installationChanged = true
	}
	if req.Target == "server" && c.stored != nil {
		if _, explicit := c.env["AGINEX_STORAGE_DRIVER"]; !explicit {
			for key := range req.Changes {
				if strings.HasPrefix(key, "AGINEX_STORAGE_") && key != "AGINEX_STORAGE_DRIVER" && req.Changes[key] != nil && key != "AGINEX_STORAGE_LOCAL_ROOT" && key != "AGINEX_STORAGE_ENDPOINT_ALLOWLIST" {
					return c, errors.New("set AGINEX_STORAGE_DRIVER in the same batch to enable environment-managed storage")
				}
			}
		}
	}
	if req.Target == "server" {
		state, e := config.ResolveState(c.env, path, c.stored)
		if e != nil {
			return c, safeError(e, c.env, c.stored)
		}
		c.state = state
		c.dbChanged = oldDriver != state.Config.Database.Driver || oldDSN != state.Config.Database.DSN
		if c.dbChanged && state.Config.Database.Driver == "" {
			return c, errors.New("database settings cannot be removed; use explicit dev reinitialize to return to Setup")
		}
	}
	if installationChanged {
		c.stored.Revision++ /* timestamp set on commit, excluded from fingerprint */
		if err := config.ValidateInstallation(*c.stored); err != nil {
			return c, safeError(err, c.env, c.stored)
		}
		data, err := json.MarshalIndent(c.stored, "", "  ")
		if err != nil {
			return c, err
		}
		c.after = append(c.after, snapshot{path, true, append(data, '\n')})
	}
	fingerprintEnv := map[string]string{}
	for _, s := range config.Catalog() {
		if v, ok := c.env[s.Name]; ok {
			fingerprintEnv[s.Name] = v
		}
	}
	fingerprintEnv["NODE_ENV"] = c.env["NODE_ENV"]
	digest, _ := json.Marshal(struct {
		Before []snapshot
		After  []snapshot
		Env    map[string]string
	}{c.before, c.after, fingerprintEnv})
	sum := sha256.Sum256(digest)
	c.token = hex.EncodeToString(sum[:])
	return c, nil
}
func safeError(err error, env map[string]string, stored *config.Installation) error {
	message := err.Error()
	for _, s := range config.Catalog() {
		if s.Secret && env[s.Name] != "" {
			message = strings.ReplaceAll(message, env[s.Name], "[redacted]")
		}
	}
	if stored != nil {
		for _, v := range []string{stored.Database.DSN, stored.SessionSecret} {
			if v != "" {
				message = strings.ReplaceAll(message, v, "[redacted]")
			}
		}
	}
	return errors.New(message)
}
func readSnapshot(path string) (snapshot, error) {
	f := snapshot{Path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if !info.Mode().IsRegular() {
		return f, fmt.Errorf("configuration path must be a regular file: %s", path)
	}
	f.Exists = true
	f.Data, err = os.ReadFile(path)
	return f, err
}
