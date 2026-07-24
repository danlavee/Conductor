package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/danlavee/Conductor/internal/skillcheck"
)

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
	return fs.ValidPath(filePath) && filePath != "." && filePath != manifestName && path.Ext(filePath) != ".go"
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

func executableRelativePath(goos string) string {
	if goos == "windows" {
		return "scripts/conductor.exe"
	}
	return "scripts/conductor"
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
