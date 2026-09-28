package cli

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Each journal entry owns only a path moved by this initialization. Shared
// directories are never journaled, so rollback cannot remove unrelated files.
type scaffoldMove struct {
	path        string
	backup      string
	fingerprint [sha256.Size]byte
}

type scaffoldMerge struct {
	target     string
	backupRoot string
	conflicts  []string
	journal    []scaffoldMove
	publish    func(string, string) error
	parents    map[string]fs.FileInfo
}

func publishIntoCurrentDirectory(stage, target string, publish func(string, string) error, output io.Writer) error {
	if err := requireInitializableDirectory(target); err != nil {
		return fmt.Errorf("publish into current directory: %w", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	merge := scaffoldMerge{target: target, publish: publish, parents: map[string]fs.FileInfo{target: info}}
	if err := merge.directory(stage, target); err != nil {
		rollbackErr := merge.rollback()
		if merge.backupRoot != "" {
			err = fmt.Errorf("%w; recovery backup: %s", err, merge.backupRoot)
		}
		return errors.Join(err, rollbackErr)
	}
	if merge.backupRoot != "" {
		fmt.Fprintf(output, "Backed up conflicting paths to %s:\n", merge.backupRoot)
		for _, name := range merge.conflicts {
			fmt.Fprintf(output, "  %s\n", name)
		}
	}
	return nil
}

// Check shared ancestors before each operation; never traverse a replaced
// directory or a symlink while merging or rolling back.
func (m *scaffoldMerge) checkParents(destination string) error {
	for parent := filepath.Dir(destination); ; parent = filepath.Dir(parent) {
		expected, ok := m.parents[parent]
		if !ok {
			return fmt.Errorf("untracked parent %q", parent)
		}
		actual, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		if !actual.IsDir() || !os.SameFile(expected, actual) {
			return fmt.Errorf("parent directory %q changed during initialization", parent)
		}
		if parent == m.target {
			return nil
		}
	}
}

func (m *scaffoldMerge) directory(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	// The manifest is the final publication, including when .aginex is shared.
	sort.SliceStable(entries, func(i, j int) bool {
		last := ".aginex"
		if destination == filepath.Join(m.target, ".aginex") {
			last = "project.json"
		}
		if entries[i].Name() == last {
			return false
		}
		if entries[j].Name() == last {
			return true
		}
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if err := m.path(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (m *scaffoldMerge) path(source, destination string) error {
	if err := m.checkParents(destination); err != nil {
		return err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	existing, err := os.Lstat(destination)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		if destination == filepath.Join(m.target, "admin") || destination == filepath.Join(m.target, "server") {
			return fmt.Errorf("reserved target path %q appeared while creating the project", destination)
		}
		if sourceInfo.IsDir() && existing.IsDir() {
			m.parents[destination] = existing
			return m.directory(source, destination)
		}
		if err := m.backup(destination); err != nil {
			return err
		}
	}
	fingerprint, err := fingerprintPath(source)
	if err != nil {
		return err
	}
	if err := m.checkParents(destination); err != nil {
		return err
	}
	if err := m.publish(source, destination); err != nil {
		return fmt.Errorf("publish %q: %w", destination, err)
	}
	m.journal = append(m.journal, scaffoldMove{path: destination, fingerprint: fingerprint})
	return nil
}

func (m *scaffoldMerge) backup(destination string) error {
	fingerprint, err := fingerprintPath(destination)
	if err != nil {
		return fmt.Errorf("inspect conflict %q: %w", destination, err)
	}
	if m.backupRoot == "" {
		m.backupRoot, err = os.MkdirTemp(m.target, ".aginex-backup-"+time.Now().UTC().Format("20060102T150405Z")+"-")
		if err != nil {
			return fmt.Errorf("create conflict backup: %w", err)
		}
	}
	relative, err := filepath.Rel(m.target, destination)
	if err != nil {
		return err
	}
	backup := filepath.Join(m.backupRoot, relative)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return err
	}
	if err := m.checkParents(destination); err != nil {
		return err
	}
	if err := m.publish(destination, backup); err != nil {
		return fmt.Errorf("backup %q: %w", relative, err)
	}
	m.journal = append(m.journal, scaffoldMove{path: destination, backup: backup, fingerprint: fingerprint})
	m.conflicts = append(m.conflicts, filepath.ToSlash(relative))
	// Detect a conflict changed while being moved, preserving it for recovery.
	actual, err := fingerprintPath(backup)
	if err != nil {
		return err
	}
	if actual != fingerprint {
		return fmt.Errorf("conflicting path %q changed during backup", destination)
	}
	return nil
}

func (m *scaffoldMerge) rollback() error {
	var failures []error
	for i := len(m.journal) - 1; i >= 0; i-- {
		move := m.journal[i]
		if err := m.checkParents(move.path); err != nil {
			failures = append(failures, err)
			continue
		}
		owned := move.path
		if move.backup != "" {
			owned = move.backup
		}
		actual, err := fingerprintPath(owned)
		if err != nil || actual != move.fingerprint {
			failures = append(failures, fmt.Errorf("preserve changed published path %q (recovery source %q)", move.path, owned))
			continue
		}
		if move.backup != "" {
			// Exclusive publication refuses to replace concurrent user changes.
			err = m.publish(move.backup, move.path)
		} else {
			err = os.RemoveAll(move.path)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("restore %q: %w", move.path, err))
		}
	}
	if len(failures) == 0 && m.backupRoot != "" {
		// Remove only empty backup directories, never unexpected recovery content.
		if err := removeEmptyBackupDirectories(m.backupRoot); err != nil {
			failures = append(failures, err)
		} else {
			m.backupRoot = ""
		}
	}
	return errors.Join(failures...)
}

func removeEmptyBackupDirectories(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("preserve unexpected backup content %q", filepath.Join(root, entry.Name()))
		}
		if err := removeEmptyBackupDirectories(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(root)
}
