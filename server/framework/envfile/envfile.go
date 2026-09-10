// Package envfile reads literal dotenv assignments without executing shell code.
package envfile

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Parse supports one assignment per line, export, quotes and trailing comments.
// Expansion is deliberately rejected: config edits must not change other keys.
func Parse(data []byte) (map[string]string, error) {
	result := map[string]string{}
	for i, line := range strings.Split(string(data), "\n") {
		key, raw, assignment := split(line)
		if !assignment {
			if s := strings.TrimSpace(line); s != "" && !strings.HasPrefix(s, "#") {
				return nil, fmt.Errorf("dotenv line %d: expected assignment", i+1)
			}
			continue
		}
		if !keyPattern.MatchString(key) {
			return nil, fmt.Errorf("dotenv line %d: invalid key", i+1)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("dotenv line %d: duplicate %s", i+1, key)
		}
		value := strings.TrimSpace(raw)
		if strings.HasPrefix(value, "'") || strings.HasPrefix(value, `"`) {
			quote := value[0]
			end := -1
			for j := 1; j < len(value); j++ {
				if value[j] == '\\' && quote == '"' {
					j++
					continue
				}
				if value[j] == quote {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, fmt.Errorf("dotenv line %d: unclosed quote", i+1)
			}
			tail := strings.TrimSpace(value[end+1:])
			if tail != "" && !strings.HasPrefix(tail, "#") {
				return nil, fmt.Errorf("dotenv line %d: invalid suffix", i+1)
			}
			if quote == '\'' {
				value = value[1:end]
			} else {
				var err error
				value, err = strconv.Unquote(strings.ReplaceAll(value[:end+1], `\$`, `$`))
				if err != nil {
					return nil, fmt.Errorf("dotenv line %d: invalid escape", i+1)
				}
			}
		} else if at := strings.Index(value, "#"); at >= 0 {
			value = strings.TrimSpace(value[:at])
		}
		result[key] = value
	}
	return result, nil
}
func split(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "export ")
	if strings.HasPrefix(line, "#") {
		return "", "", false
	}
	k, v, ok := strings.Cut(line, "=")
	return strings.TrimSpace(k), v, ok
}
func Read(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}
func Environment() map[string]string {
	result := map[string]string{}
	for _, s := range os.Environ() {
		k, v, _ := strings.Cut(s, "=")
		result[k] = v
	}
	return result
}
func Merge(base, overrides map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overrides {
		result[k] = v
	}
	return result
}

// Edit preserves unrelated lines and trailing comments on changed assignments.
func Edit(data []byte, changes map[string]*string) ([]byte, error) {
	if _, err := Parse(data); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		key, raw, ok := split(line)
		v, changed := changes[key]
		if !ok || !changed {
			if line != "" || len(data) > 0 {
				lines = append(lines, line)
			}
			continue
		}
		seen[key] = true
		if v != nil {
			suffix := ""
			raw = strings.TrimSpace(raw)
			quoted := byte(0)
			escaped := false
			for i := 0; i < len(raw); i++ {
				c := raw[i]
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' && quoted == '"' {
					escaped = true
					continue
				}
				if quoted != 0 {
					if c == quoted {
						quoted = 0
					}
					continue
				}
				if c == '\'' || c == '"' {
					quoted = c
					continue
				}
				if c == '#' {
					suffix = " " + raw[i:]
					break
				}
			}
			lines = append(lines, key+"="+quote(*v)+suffix)
		}
	}
	keys := []string{}
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !keyPattern.MatchString(k) {
			return nil, fmt.Errorf("invalid dotenv key")
		}
		if !seen[k] && changes[k] != nil {
			lines = append(lines, k+"="+quote(*changes[k]))
		}
	}
	output := []byte(strings.Join(lines, "\n") + "\n")
	if _, err := Parse(output); err != nil {
		return nil, err
	}
	return output, nil
}

func quote(value string) string { return strings.ReplaceAll(strconv.Quote(value), "$", `\$`) }
