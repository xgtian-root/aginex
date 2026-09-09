package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

func archiveName(version string, p platform) string {
	ext := ".tar.gz"
	if p.OS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("aginex_%s_%s_%s%s", strings.TrimPrefix(version, "v"), p.OS, p.Arch, ext)
}

func pack(dir, root string, m manifest, p platform, binary string) (artifact, error) {
	a := artifact{OS: p.OS, Arch: p.Arch, Name: archiveName(m.Version, p)}
	stamp, err := time.Parse(time.RFC3339, m.BuildDate)
	if err != nil {
		return a, err
	}
	var buffer bytes.Buffer
	var tw *tar.Writer
	var zw *zip.Writer
	var gz *gzip.Writer
	if p.OS == "windows" {
		zw = zip.NewWriter(&buffer)
	} else {
		gz = gzip.NewWriter(&buffer)
		tw = tar.NewWriter(gz)
	}
	for _, source := range []string{binary, filepath.Join(root, "LICENSE"), filepath.Join(root, "NOTICE")} {
		data, err := os.ReadFile(source)
		if err != nil {
			return a, err
		}
		mode := int64(0o644)
		if source == binary {
			mode = 0o755
		}
		if tw != nil {
			err = tw.WriteHeader(&tar.Header{Name: filepath.Base(source), Mode: mode, Size: int64(len(data)), ModTime: stamp, Format: tar.FormatUSTAR})
			if err == nil {
				_, err = tw.Write(data)
			}
		} else {
			header := &zip.FileHeader{Name: filepath.Base(source), Method: zip.Deflate, Modified: stamp}
			header.SetMode(os.FileMode(mode))
			var writer io.Writer
			writer, err = zw.CreateHeader(header)
			if err == nil {
				_, err = writer.Write(data)
			}
		}
		if err != nil {
			return a, err
		}
	}
	if tw != nil {
		if err = tw.Close(); err != nil {
			return a, err
		}
		err = gz.Close()
	} else {
		err = zw.Close()
	}
	if err != nil {
		return a, err
	}
	a.SHA256 = digest(buffer.Bytes())
	return a, os.WriteFile(filepath.Join(dir, a.Name), buffer.Bytes(), 0o644)
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func checksums(m manifest) []byte {
	var result strings.Builder
	for _, a := range m.Artifacts {
		fmt.Fprintf(&result, "%s  %s\n", a.SHA256, a.Name)
	}
	return []byte(result.String())
}

func bundleFiles(m manifest) []string {
	files := []string{"release.json", "SHA256SUMS"}
	for _, a := range m.Artifacts {
		files = append(files, a.Name)
	}
	if m.Tag != "" && semver.Prerelease(m.Version) == "" {
		files = append(files, "aginex.rb")
	}
	return files
}

func readManifest(path string) (manifest, error) {
	var m manifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return m, err
	}
	if d.Decode(new(any)) != io.EOF {
		return m, errors.New("trailing manifest data")
	}
	return m, validateManifest(m)
}

func validateManifest(m manifest) error {
	if err := validateVersion(m.Version); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, m.BuildDate); err != nil {
		return err
	}
	if m.Tag != "" {
		if m.Tag != "cli/"+m.Version {
			return errors.New("manifest tag/version mismatch")
		}
		if len(m.Commit) != 40 {
			return errors.New("release commit must be a full Git SHA")
		}
		if _, err := hex.DecodeString(m.Commit); err != nil {
			return err
		}
		if err := validateBackend(m.Version, m.BackendVersion); err != nil {
			return err
		}
	}
	if len(m.Artifacts) != len(platforms) {
		return errors.New("bundle must contain exactly six platforms")
	}
	for i, p := range platforms {
		a := m.Artifacts[i]
		if a.OS != p.OS || a.Arch != p.Arch || a.Name != archiveName(m.Version, p) {
			return fmt.Errorf("invalid artifact at position %d", i)
		}
		if decoded, err := hex.DecodeString(a.SHA256); err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("invalid checksum for %s", a.Name)
		}
	}
	return nil
}

func verifyBundle(dir string) (manifest, error) {
	m, err := readManifest(filepath.Join(dir, "release.json"))
	if err != nil {
		return m, err
	}
	for _, a := range m.Artifacts {
		raw, err := os.ReadFile(filepath.Join(dir, a.Name))
		if err != nil {
			return m, err
		}
		if digest(raw) != a.SHA256 {
			return m, fmt.Errorf("checksum mismatch: %s", a.Name)
		}
		binary, err := archiveBinary(raw, a.OS)
		if err != nil {
			return m, fmt.Errorf("%s: %w", a.Name, err)
		}
		if err = inspectBinary(binary, platform{a.OS, a.Arch}); err != nil {
			return m, err
		}
	}
	for name, want := range map[string][]byte{"SHA256SUMS": checksums(m)} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return m, err
		}
		if !bytes.Equal(raw, want) {
			return m, fmt.Errorf("%s differs from manifest", name)
		}
	}
	if m.Tag != "" && semver.Prerelease(m.Version) == "" {
		raw, err := os.ReadFile(filepath.Join(dir, "aginex.rb"))
		if err != nil {
			return m, err
		}
		if !bytes.Equal(raw, formula(m)) {
			return m, errors.New("Homebrew formula differs from manifest")
		}
	}
	return m, nil
}

// Never extract paths supplied by an archive; only accept three plain files.
func archiveBinary(raw []byte, goos string) ([]byte, error) {
	binaryName := "aginex"
	if goos == "windows" {
		binaryName += ".exe"
	}
	seen := map[string]bool{}
	var binary []byte
	read := func(name string, mode os.FileMode, reader io.Reader) error {
		if seen[name] || name != binaryName && name != "LICENSE" && name != "NOTICE" || !mode.IsRegular() {
			return fmt.Errorf("unexpected archive entry %q", name)
		}
		seen[name] = true
		data, err := io.ReadAll(io.LimitReader(reader, 256<<20+1))
		if err != nil {
			return err
		}
		if len(data) > 256<<20 {
			return errors.New("archive entry exceeds size limit")
		}
		if name == binaryName {
			if mode.Perm()&0o111 == 0 {
				return errors.New("binary has no executable permission")
			}
			binary = data
		}
		return nil
	}
	if goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return nil, err
		}
		for _, file := range zr.File {
			r, err := file.Open()
			if err != nil {
				return nil, err
			}
			err = read(file.Name, file.Mode(), r)
			r.Close()
			if err != nil {
				return nil, err
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if err = read(h.Name, h.FileInfo().Mode(), tr); err != nil {
				return nil, err
			}
		}
	}
	if len(seen) != 3 || len(binary) == 0 {
		return nil, errors.New("archive is missing binary, LICENSE or NOTICE")
	}
	return binary, nil
}

func inspectBinary(binary []byte, p platform) error {
	ok := false
	switch p.OS {
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(binary))
		if err != nil {
			return err
		}
		defer f.Close()
		ok = p.Arch == "amd64" && f.Cpu == macho.CpuAmd64 || p.Arch == "arm64" && f.Cpu == macho.CpuArm64
	case "linux":
		f, err := elf.NewFile(bytes.NewReader(binary))
		if err != nil {
			return err
		}
		defer f.Close()
		ok = p.Arch == "amd64" && f.Machine == elf.EM_X86_64 || p.Arch == "arm64" && f.Machine == elf.EM_AARCH64
	case "windows":
		f, err := pe.NewFile(bytes.NewReader(binary))
		if err != nil {
			return err
		}
		defer f.Close()
		ok = p.Arch == "amd64" && f.Machine == pe.IMAGE_FILE_MACHINE_AMD64 || p.Arch == "arm64" && f.Machine == pe.IMAGE_FILE_MACHINE_ARM64
	}
	if !ok {
		return fmt.Errorf("binary architecture differs from %s/%s", p.OS, p.Arch)
	}
	return nil
}

func smoke(o options) error {
	m, err := verifyBundle(o.dist)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "aginex-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for _, a := range m.Artifacts {
		if a.OS != runtime.GOOS || a.Arch != runtime.GOARCH {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(o.dist, a.Name))
		if err != nil {
			return err
		}
		binary, err := archiveBinary(raw, a.OS)
		if err != nil {
			return err
		}
		name := "aginex"
		if a.OS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(dir, name)
		if err = os.WriteFile(path, binary, 0o755); err != nil {
			return err
		}
		out, err := command(dir, nil, nil, path, "--version")
		if err != nil {
			return err
		}
		if !strings.Contains(string(out), m.Version) || !strings.Contains(string(out), m.Commit) {
			return fmt.Errorf("unexpected version output: %s", out)
		}
		if _, err = command(dir, nil, nil, path, "--help"); err != nil {
			return err
		}
		args := []string{"new", "smoke-project"}
		if m.Tag == "" {
			args = append(args, "--aginex-path", o.root)
		}
		if _, err = command(dir, nil, nil, path, args...); err != nil {
			return err
		}
		goMod, err := os.ReadFile(filepath.Join(dir, "smoke-project", "server", "go.mod"))
		if err != nil {
			return err
		}
		if !strings.Contains(string(goMod), backendModule+" "+m.BackendVersion) {
			return errors.New("scaffold backend version mismatch")
		}
		fmt.Printf("Smoke passed: %s/%s %s\n", a.OS, a.Arch, m.Version)
		return nil
	}
	return fmt.Errorf("no artifact for host %s/%s", runtime.GOOS, runtime.GOARCH)
}
