package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed catalog.json
var catalogJSON []byte

type Setting struct {
	Name        string   `json:"name"`
	Target      string   `json:"target"`
	Type        string   `json:"type"`
	Default     string   `json:"default"`
	Description string   `json:"description"`
	Allowed     []string `json:"allowed"`
	Secret      bool     `json:"secret"`
	Fixed       bool     `json:"fixed"`
	Requirement string   `json:"requirement"`
	Effect      string   `json:"effect"`
}

func Catalog() []Setting {
	var items []Setting
	if err := json.Unmarshal(catalogJSON, &items); err != nil {
		panic(err)
	}
	return items
}
func (s Setting) Check(v string) error {
	fail := func() error { return fmt.Errorf("%s: invalid %s value", s.Name, s.Type) }
	if strings.ContainsAny(v, "\x00\r\n") {
		return fail()
	}
	if s.Fixed && v != "" && v != s.Default {
		return fmt.Errorf("%s is fixed to %s", s.Name, s.Default)
	}
	if v == "" {
		return nil
	}
	switch s.Type {
	case "enum":
		for _, a := range s.Allowed {
			if v == a {
				return nil
			}
		}
		return fail()
	case "boolean":
		if _, err := strconv.ParseBool(v); err != nil {
			return fail()
		}
	case "duration":
		if n, err := time.ParseDuration(v); err != nil || n <= 0 {
			return fail()
		}
	case "integer", "port":
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n == 0 || (s.Type == "port" && n > 65535) {
			return fail()
		}
	case "address":
		_, p, err := net.SplitHostPort(v)
		n, e := strconv.Atoi(p)
		if err != nil || e != nil || n < 1 || n > 65535 {
			return fail()
		}
	case "url", "origins":
		for _, raw := range strings.Split(v, ",") {
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return fail()
			}
			if s.Type == "origins" && u.Path != "" && u.Path != "/" {
				return fail()
			}
		}
	}
	return nil
}

// WithInstallationFileLock shares serialization with console installation writers.
// Only local maintenance uses it; the HTTP storage update contract stays immutable.
func WithInstallationFileLock(path string, action func() error) error {
	unlock, err := lockInstallationUpdate(path)
	if err != nil {
		return err
	}
	defer unlock()
	return action()
}
