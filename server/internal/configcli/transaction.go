package configcli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xgtian-root/aginex/server/internal/config"
)

type journal struct {
	Before           []snapshot `json:"before"`
	After            []snapshot `json:"after"`
	InstallationPath string     `json:"installationPath"`
	Committed        bool       `json:"committed"`
}

func commit(root string, c candidate) error {
	directory := filepath.Join(root, ".cache", "aginex")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	return config.WithInstallationFileLock(filepath.Join(directory, "config"), func() error {
		action := func() error {
			journalPath := filepath.Join(directory, "config-transaction.json")
			if _, err := os.Stat(journalPath); err == nil {
				return errors.New("pending configuration transaction; run aginex config recover first")
			}
			for _, before := range c.before {
				now, err := readSnapshot(before.Path)
				if err != nil {
					return err
				}
				if !equalSnapshot(before, now) {
					return errors.New("configuration changed concurrently; no changes saved")
				}
			}
			id := make([]byte, 16)
			if _, err := rand.Read(id); err != nil {
				return err
			}
			auditPath := filepath.Join(directory, "config-audit", hex.EncodeToString(id)+".json")
			record, _ := json.Marshal(struct {
				Time   time.Time `json:"time"`
				Action string    `json:"action"`
				Keys   []string  `json:"keys"`
			}{time.Now().UTC(), "config.update", c.keys})
			if c.stored != nil {
				for i, f := range c.after {
					if f.Path == c.installPath {
						next := *c.stored
						next.UpdatedAt = time.Now().UTC()
						data, e := json.MarshalIndent(next, "", "  ")
						if e != nil {
							return e
						}
						c.after[i].Data = append(data, '\n')
					}
				}
			}
			before := []snapshot{}
			for _, after := range c.after {
				for _, b := range c.before {
					if b.Path == after.Path {
						before = append(before, b)
						break
					}
				}
			}
			before = append(before, snapshot{Path: auditPath})
			after := append(append([]snapshot{}, c.after...), snapshot{auditPath, true, append(record, '\n')})
			j := journal{Before: before, After: after, InstallationPath: c.installPath}
			if err := writeJournal(journalPath, j); err != nil {
				return err
			}
			for _, f := range after {
				if err := replace(f); err != nil {
					return errors.Join(err, recoverJournal(journalPath))
				}
			}
			j.Committed = true
			if err := writeJournal(journalPath, j); err != nil {
				return errors.Join(err, recoverJournal(journalPath))
			}
			if err := os.Remove(journalPath); err != nil {
				return fmt.Errorf("configuration saved; transaction cleanup failed: %w", err)
			}
			return syncDirectory(directory)
		}
		if c.stored != nil {
			return config.WithInstallationFileLock(c.installPath, action)
		}
		return action()
	})
}
func Recover(root string) error {
	directory := filepath.Join(root, ".cache", "aginex")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	return config.WithInstallationFileLock(filepath.Join(directory, "config"), func() error {
		path := filepath.Join(directory, "config-transaction.json")
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var j journal
		if err := json.Unmarshal(data, &j); err != nil {
			return errors.New("invalid recovery journal")
		}
		action := func() error { return recoverJournal(path) }
		if j.InstallationPath != "" {
			if _, err := os.Stat(j.InstallationPath); err == nil {
				return config.WithInstallationFileLock(j.InstallationPath, action)
			}
		}
		return action()
	})
}
func equalSnapshot(a, b snapshot) bool {
	return a.Path == b.Path && a.Exists == b.Exists && bytes.Equal(a.Data, b.Data)
}
func recoverJournal(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	if len(j.Before) != len(j.After) {
		return errors.New("invalid recovery journal")
	}
	for i, b := range j.Before {
		a := j.After[i]
		if a.Path != b.Path {
			return errors.New("invalid recovery paths")
		}
		now, err := readSnapshot(b.Path)
		if err != nil {
			return err
		}
		if !equalSnapshot(now, b) && !equalSnapshot(now, a) {
			return errors.New("recovery stopped: a configuration file was edited after the interrupted transaction")
		}
	}
	if !j.Committed {
		for _, f := range j.Before {
			if err := replace(f); err != nil {
				return fmt.Errorf("configuration recovery incomplete: %w", err)
			}
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func writeJournal(path string, j journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return replace(snapshot{path, true, data})
}
func replace(f snapshot) error {
	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if !f.Exists {
		if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return syncDirectory(dir)
	}
	file, err := os.CreateTemp(dir, ".aginex-config-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(f.Data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, f.Path); err != nil {
		return err
	}
	return syncDirectory(dir)
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
