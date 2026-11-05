// Package install places one of Conductor's embedded payloads -- the portable
// skill, or a host adapter -- together with the running executable.
package install

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const manifestName = ".conductor-manifest.json"

// Payload is one installable tree: its embedded files, the two trailing path
// segments its host expects it under, and where the executable sits inside it.
type Payload struct {
	Bundle        fs.FS
	Parent, Name  string
	ExecutableDir string
}

// Check enforces the destination contract without writing anything.
func (p Payload) Check(destination string) error {
	clean := filepath.Clean(destination)
	if !filepath.IsAbs(destination) || !strings.EqualFold(filepath.Base(clean), p.Name) || !strings.EqualFold(filepath.Base(filepath.Dir(clean)), p.Parent) {
		return fmt.Errorf("destination must be an absolute path ending in %s/%s", p.Parent, p.Name)
	}
	return nil
}

func (p Payload) executable() string {
	name := p.ExecutableDir + "/conductor"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// Source is the exact payload and executable being installed.
// Rewrite edits a payload file before it is hashed. Grok's hook command has
// to name this OS's executable, and the published tree is that rewritten one.
type Source struct {
	Payload
	Executable string
	Version    string
	Protocol   int
	Rewrite    func(name string, data []byte) []byte
}

// Result is the install response the CLI writes.
type Result struct {
	Status          string `json:"status"` // "installed" | "already-installed" | "upgraded"
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	Protocol        int    `json:"protocol"`
	InstallPath     string `json:"install_path"`
	BinaryPath      string `json:"binary_path"`
}

// Currency says how destination compares with what Install would place there.
type Currency struct {
	Status           string `json:"status"` // "not-installed" | "unknown" | "current" | "outdated"
	InstalledVersion string `json:"installed_version,omitempty"`
	SourceVersion    string `json:"source_version"`
	Detail           string `json:"detail,omitempty"`
}

// manifest records what was installed. Its digest covers every file but itself,
// so a tree changed after install no longer matches it.
type manifest struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	SHA256   string `json:"sha256"`
}

// Install stages the payload beside destination, proves the staged executable
// runs, and only then renames it into place. A destination already holding
// something else is a conflict unless replace is set; then the old tree is
// moved to ~/.conductor/backups and restored if the rename fails.
func Install(destination string, source Source, replace bool) (Result, error) {
	destination = filepath.Clean(destination)
	files, want, err := source.build(destination)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Status: "installed", Version: want.Version, Protocol: want.Protocol,
		InstallPath: destination, BinaryPath: filepath.Join(destination, filepath.FromSlash(source.executable())),
	}
	present, err := classify(destination, want)
	switch {
	case err != nil:
		return Result{}, err
	case present.Status == "current":
		result.Status = "already-installed"
		return result, nil
	case present.Status != "not-installed" && !replace:
		return Result{}, fmt.Errorf("install conflict: %s is %s (installed %q, this is %q); pass --replace to replace it", destination, present.Status, present.InstalledVersion, want.Version)
	}

	grandparent := filepath.Dir(filepath.Dir(destination))
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return Result{}, err
	}
	stage, err := os.MkdirTemp(grandparent, ".conductor-install-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o755); err != nil {
		return Result{}, err
	}
	for name, data := range files {
		if err := writeFile(filepath.Join(stage, filepath.FromSlash(name)), data, 0o644); err != nil {
			return Result{}, err
		}
	}
	staged := filepath.Join(stage, filepath.FromSlash(source.executable()))
	if err := os.Chmod(staged, 0o755); err != nil {
		return Result{}, err
	}
	manifestData, err := json.Marshal(want)
	if err != nil {
		return Result{}, err
	}
	if err := writeFile(filepath.Join(stage, manifestName), manifestData, 0o644); err != nil {
		return Result{}, err
	}
	if err := smokeCheck(staged, want); err != nil {
		return Result{}, fmt.Errorf("install verify: %w", err)
	}

	backup := ""
	if present.Status != "not-installed" {
		if backup, err = retire(destination); err != nil {
			return Result{}, err
		}
		result.Status, result.PreviousVersion = "upgraded", present.InstalledVersion
	}
	if err := os.Rename(stage, destination); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, destination); restoreErr != nil {
				return Result{}, fmt.Errorf("install publish: %w; the previous installation is at %s: %v", err, backup, restoreErr)
			}
		}
		return Result{}, fmt.Errorf("install publish: %w", err)
	}
	return result, nil
}

// CheckCurrency compares destination with source without writing anything.
func CheckCurrency(destination string, source Source) (Currency, error) {
	destination = filepath.Clean(destination)
	_, want, err := source.build(destination)
	if err != nil {
		return Currency{}, err
	}
	return classify(destination, want)
}

// build collects the payload's files and the manifest they would carry.
func (s Source) build(destination string) (map[string][]byte, manifest, error) {
	if err := s.Check(destination); err != nil {
		return nil, manifest{}, err
	}
	files := map[string][]byte{}
	err := fs.WalkDir(s.Bundle, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		files[name], err = fs.ReadFile(s.Bundle, name)
		return err
	})
	if err != nil {
		return nil, manifest{}, fmt.Errorf("read payload: %w", err)
	}
	if s.Rewrite != nil {
		for name, data := range files {
			files[name] = s.Rewrite(name, data)
		}
	}
	if files[s.executable()], err = os.ReadFile(s.Executable); err != nil {
		return nil, manifest{}, fmt.Errorf("read running executable: %w", err)
	}
	return files, manifest{Version: s.Version, Protocol: s.Protocol, SHA256: digest(files)}, nil
}

func classify(destination string, want manifest) (Currency, error) {
	result := Currency{SourceVersion: want.Version}
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		result.Status = "not-installed"
		return result, nil
	}
	if err != nil {
		return Currency{}, err
	}
	if !info.IsDir() {
		return Currency{}, fmt.Errorf("install conflict: %s is not a directory", destination)
	}
	var have manifest
	data, err := os.ReadFile(filepath.Join(destination, manifestName))
	if err == nil {
		err = json.Unmarshal(data, &have)
	}
	if err != nil {
		result.Status, result.Detail = "unknown", "no readable install manifest"
		return result, nil
	}
	result.InstalledVersion = have.Version
	present, err := readTree(destination)
	if err != nil {
		return Currency{}, err
	}
	switch actual := digest(present); {
	case actual != have.SHA256:
		result.Status, result.Detail = "unknown", "installed content changed since install"
	case actual == want.SHA256:
		result.Status = "current"
	default:
		result.Status = "outdated"
	}
	return result, nil
}

func readTree(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil || relative == manifestName {
			return err
		}
		files[filepath.ToSlash(relative)], err = os.ReadFile(name)
		return err
	})
	return files, err
}

func digest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		fmt.Fprintf(hash, "%s\x00%x\n", name, sha256.Sum256(files[name]))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func writeFile(name string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, data, mode)
}

// smokeCheck runs the staged executable, so a binary that cannot start on
// this machine is rejected before it displaces anything.
func smokeCheck(executable string, want manifest) error {
	output, err := exec.Command(executable, "version").Output()
	if err != nil {
		return fmt.Errorf("run staged executable: %w", err)
	}
	var got manifest
	if err := json.Unmarshal(output, &got); err != nil {
		return fmt.Errorf("decode staged version: %w", err)
	}
	if got.Version != want.Version || got.Protocol != want.Protocol {
		return fmt.Errorf("staged executable reports %s protocol %d, want %s protocol %d", got.Version, got.Protocol, want.Version, want.Protocol)
	}
	return nil
}

// retire moves an installed tree to ~/.conductor/backups/<parent>, out of the
// directory the host scans, and leaves it there.
func retire(destination string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(home, ".conductor", "backups", filepath.Base(filepath.Dir(destination)))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	backup := filepath.Join(directory, filepath.Base(destination)+".pre-upgrade-"+time.Now().Format("20060102150405.000000000"))
	if err := os.Rename(destination, backup); err != nil {
		return "", fmt.Errorf("move %s aside: %w", destination, err)
	}
	return backup, nil
}
