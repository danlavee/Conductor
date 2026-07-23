// Package install places Conductor's embedded skill and native executable atomically.
package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/danlavee/Conductor/internal/skillcheck"
)

const (
	manifestName   = ".conductor-manifest.json"
	manifestFormat = 2
)

var errDestinationExists = errors.New("installation destination already exists")

// Source identifies the exact bundle and executable being installed.
type Source struct {
	Bundle         fs.FS
	ExecutablePath string
	Version        string
	Protocol       int
	GOOS           string
	GOARCH         string
	SmokeCheck     bool
}

// Result is the deterministic install response written by the CLI.
type Result struct {
	Status       string `json:"status"`
	Version      string `json:"version"`
	Protocol     int    `json:"protocol"`
	SkillPath    string `json:"skill_path"`
	BinaryPath   string `json:"binary_path"`
	FileCount    int    `json:"file_count"`
	ManifestHash string `json:"manifest_hash"`
}

type manifest struct {
	Format         int            `json:"format"`
	Version        string         `json:"version"`
	Protocol       int            `json:"protocol"`
	GOOS           string         `json:"goos"`
	GOARCH         string         `json:"goarch"`
	BundleSHA256   string         `json:"bundle_sha256"`
	DistributionID string         `json:"distribution_id"`
	Executable     string         `json:"executable"`
	Files          []manifestFile `json:"files"`
}

type manifestFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

type payloadFile struct {
	manifestFile
	data []byte
	mode fs.FileMode
}

// Install validates, stages, verifies, and publishes one exact skill directory.
func Install(destination string, source Source) (Result, error) {
	cleanDestination, files, expected, manifestData, err := preflight(destination, source)
	if err != nil {
		return Result{}, fmt.Errorf("install preflight: %w", err)
	}
	if result, found, err := inspectExisting(cleanDestination, expected); found || err != nil {
		return result, err
	}

	parent := filepath.Dir(cleanDestination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Result{}, fmt.Errorf("install stage: create skills directory: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(parent), ".conductor-install-*")
	if err != nil {
		return Result{}, fmt.Errorf("install stage: %w", err)
	}
	defer os.RemoveAll(stage)

	if err := writeInstallation(stage, files, manifestData); err != nil {
		return Result{}, fmt.Errorf("install stage: %w", err)
	}
	if problems := skillcheck.ValidateSkill(os.DirFS(stage)); len(problems) > 0 {
		return Result{}, fmt.Errorf("install verify: invalid staged skill: %s", strings.Join(problems, "; "))
	}
	if _, _, err := verifyInstallation(stage); err != nil {
		return Result{}, fmt.Errorf("install verify: %w", err)
	}
	if source.SmokeCheck {
		if err := smokeCheck(filepath.Join(stage, filepath.FromSlash(expected.Executable)), expected.Version, expected.Protocol); err != nil {
			return Result{}, fmt.Errorf("install verify: %w", err)
		}
	}
	if err := publishNoReplace(stage, cleanDestination); err != nil {
		if result, found, inspectErr := inspectExisting(cleanDestination, expected); found || inspectErr != nil {
			return result, inspectErr
		}
		return Result{}, fmt.Errorf("install publish: %w", err)
	}
	return resultFor("installed", cleanDestination, expected, manifestData), nil
}

// ValidateDestination checks the native host's install-path contract without writing.
func ValidateDestination(destination string) error {
	_, err := validateDestination(destination, runtime.GOOS)
	return err
}

func preflight(destination string, source Source) (string, []payloadFile, manifest, []byte, error) {
	cleanDestination, err := validateDestination(destination, source.GOOS)
	if err != nil {
		return "", nil, manifest{}, nil, err
	}
	if source.Bundle == nil {
		return "", nil, manifest{}, nil, errors.New("skill bundle is missing")
	}
	if source.GOOS != runtime.GOOS || source.GOARCH != runtime.GOARCH {
		return "", nil, manifest{}, nil, errors.New("installer platform does not match the running executable")
	}
	if source.Protocol <= 0 {
		return "", nil, manifest{}, nil, errors.New("protocol version must be positive")
	}
	if problems := skillcheck.ValidateSkill(source.Bundle); len(problems) > 0 {
		return "", nil, manifest{}, nil, fmt.Errorf("invalid embedded skill: %s", strings.Join(problems, "; "))
	}
	bundleFiles, err := collectBundle(source.Bundle)
	if err != nil {
		return "", nil, manifest{}, nil, err
	}
	executableData, err := os.ReadFile(source.ExecutablePath)
	if err != nil {
		return "", nil, manifest{}, nil, fmt.Errorf("read running executable: %w", err)
	}
	if len(executableData) == 0 {
		return "", nil, manifest{}, nil, errors.New("running executable is empty")
	}
	executablePath := "scripts/conductor"
	if source.GOOS == "windows" {
		executablePath += ".exe"
	}
	for _, file := range bundleFiles {
		if file.Path == executablePath || file.Path == manifestName {
			return "", nil, manifest{}, nil, fmt.Errorf("skill bundle reserves installed path %s", file.Path)
		}
	}
	bundleDigest := digestPayload(bundleFiles)
	executableDigest := digest(executableData)
	version := source.Version
	if version == "" {
		version = "(devel)"
	}
	identity := distributionIdentity(manifestFormat, source.Protocol, version, source.GOOS, source.GOARCH, bundleDigest, executableDigest)
	files := append(bundleFiles, payloadFile{
		manifestFile: manifestFile{Path: executablePath, SHA256: executableDigest, Executable: true},
		data:         executableData,
		mode:         0o755,
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	entries := make([]manifestFile, len(files))
	for index := range files {
		entries[index] = files[index].manifestFile
	}
	expected := manifest{
		Format:         manifestFormat,
		Version:        version,
		Protocol:       source.Protocol,
		GOOS:           source.GOOS,
		GOARCH:         source.GOARCH,
		BundleSHA256:   bundleDigest,
		DistributionID: identity,
		Executable:     executablePath,
		Files:          entries,
	}
	manifestData, err := encodeManifest(expected)
	if err != nil {
		return "", nil, manifest{}, nil, err
	}
	return cleanDestination, files, expected, manifestData, nil
}

func validateDestination(destination, goos string) (string, error) {
	if goos != "windows" && goos != "linux" {
		return "", fmt.Errorf("unsupported operating system %q", goos)
	}
	if strings.TrimSpace(destination) == "" {
		return "", errors.New("destination is required")
	}
	if !filepath.IsAbs(destination) {
		return "", errors.New("destination must be an absolute path")
	}
	clean := filepath.Clean(destination)
	if goos == "windows" && isUNCVolume(filepath.VolumeName(clean)) {
		return "", errors.New("UNC destinations are unsupported")
	}
	components := []string{filepath.Base(filepath.Dir(filepath.Dir(clean))), filepath.Base(filepath.Dir(clean)), filepath.Base(clean)}
	want := []string{".agents", "skills", "conductor"}
	for index := range components {
		matches := components[index] == want[index]
		if goos == "windows" {
			matches = strings.EqualFold(components[index], want[index])
		}
		if !matches {
			return "", errors.New("destination must end in .agents/skills/conductor")
		}
	}
	return clean, nil
}

func isUNCVolume(volume string) bool {
	lower := strings.ToLower(volume)
	if strings.HasPrefix(lower, `\\?\unc\`) {
		return true
	}
	return strings.HasPrefix(volume, `\\`) && !strings.HasPrefix(lower, `\\?\`)
}

func collectBundle(bundle fs.FS) ([]payloadFile, error) {
	var files []payloadFile
	err := fs.WalkDir(bundle, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isBundlePath(path) {
			return nil
		}
		data, err := fs.ReadFile(bundle, path)
		if err != nil {
			return err
		}
		files = append(files, payloadFile{
			manifestFile: manifestFile{Path: filepath.ToSlash(path), SHA256: digest(data)},
			data:         data,
			mode:         0o644,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read skill bundle: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func isBundlePath(filePath string) bool {
	return fs.ValidPath(filePath) && filePath != "." && filePath != manifestName && pathExtension(filePath) != ".go"
}

func pathExtension(filePath string) string {
	base := filePath[strings.LastIndex(filePath, "/")+1:]
	index := strings.LastIndex(base, ".")
	if index < 0 {
		return ""
	}
	return base[index:]
}

func digestPayload(files []payloadFile) string {
	hash := sha256.New()
	for _, file := range files {
		hash.Write([]byte(file.Path))
		hash.Write([]byte{0})
		hash.Write([]byte(file.SHA256))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func distributionIdentity(format, protocol int, version, goos, goarch, bundleDigest, executableDigest string) string {
	parts := []string{strconv.Itoa(format), strconv.Itoa(protocol), version, goos, goarch, bundleDigest}
	if version == "(devel)" {
		parts = append(parts, executableDigest)
	}
	return digest([]byte(strings.Join(parts, "\x00")))
}

func encodeManifest(value manifest) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeInstallation(root string, files []payloadFile, manifestData []byte) error {
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := writeFile(path, file.data, file.mode); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(root, manifestName), manifestData, 0o644); err != nil {
		return err
	}
	return os.Chmod(root, 0o755)
}

func writeFile(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written := false
	defer func() {
		if !written {
			file.Close()
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	written = true
	return os.Chmod(path, mode)
}

func inspectExisting(destination string, expected manifest) (Result, bool, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, true, fmt.Errorf("install preflight: inspect destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Result{}, true, errors.New("install conflict: destination is a symbolic link")
	}
	if !info.IsDir() {
		return Result{}, true, errors.New("install conflict: destination is not a directory")
	}
	existing, manifestData, err := verifyInstallation(destination)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			empty, emptyErr := isDirEmpty(destination)
			if emptyErr == nil && !empty {
				backupPath := destination + ".pre-upgrade-" + time.Now().Format("20060102150405")
				log.Printf("Warning: Destination %s has no install manifest. Creating a safety-net backup at %s and overwriting...", destination, backupPath)
				if renameErr := os.Rename(destination, backupPath); renameErr != nil {
					return Result{}, true, fmt.Errorf("install preflight: failed to backup conflicting destination: %w", renameErr)
				}
				return Result{}, false, nil
			}
		}
		return Result{}, true, fmt.Errorf("install conflict: %w", err)
	}
	if existing.DistributionID != expected.DistributionID {
		return Result{}, true, errors.New("install conflict: destination contains a different Conductor installation")
	}
	return resultFor("already-installed", destination, existing, manifestData), true, nil
}

func isDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

func verifyInstallation(root string) (manifest, []byte, error) {
	manifestPath := filepath.Join(root, manifestName)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifest{}, nil, fmt.Errorf("read manifest: %w", err)
	}
	var value manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest{}, nil, errors.New("manifest contains trailing data")
	}
	canonical, err := encodeManifest(value)
	if err != nil || !bytes.Equal(canonical, manifestData) {
		return manifest{}, nil, errors.New("manifest is not canonical")
	}
	if value.Format != manifestFormat || value.Version == "" || value.Protocol <= 0 || value.GOOS == "" || value.GOARCH == "" || value.BundleSHA256 == "" || value.DistributionID == "" {
		return manifest{}, nil, errors.New("manifest metadata is incomplete")
	}
	if value.Executable != executableRelativePath(value.GOOS) {
		return manifest{}, nil, errors.New("manifest executable path is invalid")
	}

	declared := map[string]manifestFile{}
	allowedDirectories := map[string]bool{".": true}
	var bundleFiles []payloadFile
	previous := ""
	for _, entry := range value.Files {
		if !fs.ValidPath(entry.Path) || entry.Path == manifestName || entry.Path <= previous || entry.SHA256 == "" {
			return manifest{}, nil, errors.New("manifest file list is invalid")
		}
		if !isBundlePath(entry.Path) && entry.Path != value.Executable {
			return manifest{}, nil, fmt.Errorf("manifest contains unsupported path %s", entry.Path)
		}
		if entry.Executable != (entry.Path == value.Executable) {
			return manifest{}, nil, fmt.Errorf("manifest executable marker is invalid for %s", entry.Path)
		}
		declared[entry.Path] = entry
		for directory := pathDirectory(entry.Path); directory != "."; directory = pathDirectory(directory) {
			allowedDirectories[directory] = true
		}
		previous = entry.Path
	}
	if _, ok := declared[value.Executable]; !ok {
		return manifest{}, nil, errors.New("manifest omits the executable")
	}
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link is not allowed: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if !allowedDirectories[relative] {
				return fmt.Errorf("unexpected installed directory: %s", relative)
			}
			return nil
		}
		if relative == manifestName {
			return nil
		}
		declaredFile, ok := declared[relative]
		if !ok {
			return fmt.Errorf("unexpected installed file: %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if digest(data) != declaredFile.SHA256 {
			return fmt.Errorf("installed file hash mismatch: %s", relative)
		}
		if declaredFile.Executable && value.GOOS == "linux" {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().Perm()&0o100 == 0 {
				return errors.New("installed Conductor binary is not executable")
			}
		}
		if relative != value.Executable && isBundlePath(relative) {
			bundleFiles = append(bundleFiles, payloadFile{manifestFile: declaredFile})
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return manifest{}, nil, err
	}
	for path := range declared {
		if !seen[path] {
			return manifest{}, nil, fmt.Errorf("installed file is missing: %s", path)
		}
	}
	sort.Slice(bundleFiles, func(i, j int) bool { return bundleFiles[i].Path < bundleFiles[j].Path })
	if digestPayload(bundleFiles) != value.BundleSHA256 {
		return manifest{}, nil, errors.New("installed bundle digest is invalid")
	}
	executableDigest := declared[value.Executable].SHA256
	if distributionIdentity(value.Format, value.Protocol, value.Version, value.GOOS, value.GOARCH, value.BundleSHA256, executableDigest) != value.DistributionID {
		return manifest{}, nil, errors.New("distribution identity is invalid")
	}
	return value, manifestData, nil
}

func pathDirectory(filePath string) string {
	index := strings.LastIndex(filePath, "/")
	if index < 0 {
		return "."
	}
	return filePath[:index]
}

func executableRelativePath(goos string) string {
	if goos == "windows" {
		return "scripts/conductor.exe"
	}
	return "scripts/conductor"
}

func smokeCheck(executablePath, version string, protocol int) error {
	command := exec.Command(executablePath, "version")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("run staged executable: %w", err)
	}
	var response struct {
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("decode staged version: %w", err)
	}
	if response.Version != version {
		return fmt.Errorf("staged executable version %q does not match %q", response.Version, version)
	}
	if response.Protocol != protocol {
		return fmt.Errorf("staged executable protocol %d does not match %d", response.Protocol, protocol)
	}
	return nil
}

func resultFor(status, destination string, value manifest, manifestData []byte) Result {
	return Result{
		Status:       status,
		Version:      value.Version,
		Protocol:     value.Protocol,
		SkillPath:    destination,
		BinaryPath:   filepath.Join(destination, filepath.FromSlash(value.Executable)),
		FileCount:    len(value.Files),
		ManifestHash: digest(manifestData),
	}
}

func digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
