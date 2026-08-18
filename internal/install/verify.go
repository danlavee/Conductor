package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
)

func verifyInstallation(root string) (manifest, []byte, error) {
	manifestPath := filepath.Join(root, manifestName)
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifest{}, nil, fmt.Errorf("read manifest: %w", err)
	}
	var installedManifest manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestData))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&installedManifest); err != nil {
		return manifest{}, nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest{}, nil, errors.New("manifest contains trailing data")
	}
	canonical, err := encodeManifest(installedManifest)
	if err != nil || !bytes.Equal(canonical, manifestData) {
		return manifest{}, nil, errors.New("manifest is not canonical")
	}
	if installedManifest.Format != manifestFormat || installedManifest.Version == "" || installedManifest.Protocol <= 0 || installedManifest.GOOS == "" || installedManifest.GOARCH == "" || installedManifest.BundleSHA256 == "" || installedManifest.DistributionID == "" {
		return manifest{}, nil, errors.New("manifest metadata is incomplete")
	}
	if installedManifest.Executable != executableRelativePath(installedManifest.GOOS) {
		return manifest{}, nil, errors.New("manifest executable path is invalid")
	}

	declaredFiles := map[string]manifestFile{}
	allowedDirectories := map[string]bool{".": true}
	var bundleFiles []payloadFile
	previous := ""
	for _, manifestEntry := range installedManifest.Files {
		if !fs.ValidPath(manifestEntry.Path) || manifestEntry.Path == manifestName || manifestEntry.Path <= previous || manifestEntry.SHA256 == "" {
			return manifest{}, nil, errors.New("manifest file list is invalid")
		}
		if !isBundlePath(manifestEntry.Path) && manifestEntry.Path != installedManifest.Executable {
			return manifest{}, nil, fmt.Errorf("manifest contains unsupported path %s", manifestEntry.Path)
		}
		if manifestEntry.Executable != (manifestEntry.Path == installedManifest.Executable) {
			return manifest{}, nil, fmt.Errorf("manifest executable marker is invalid for %s", manifestEntry.Path)
		}
		declaredFiles[manifestEntry.Path] = manifestEntry
		for directory := path.Dir(manifestEntry.Path); directory != "."; directory = path.Dir(directory) {
			allowedDirectories[directory] = true
		}
		previous = manifestEntry.Path
	}
	if _, ok := declaredFiles[installedManifest.Executable]; !ok {
		return manifest{}, nil, errors.New("manifest omits the executable")
	}
	seenFiles := map[string]bool{}
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link is not allowed: %s", filePath)
		}
		relative, err := filepath.Rel(root, filePath)
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
		declaredFile, ok := declaredFiles[relative]
		if !ok {
			return fmt.Errorf("unexpected installed file: %s", relative)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if digest(data) != declaredFile.SHA256 {
			return fmt.Errorf("installed file hash mismatch: %s", relative)
		}
		if declaredFile.Executable && installedManifest.GOOS == "linux" {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().Perm()&0o100 == 0 {
				return errors.New("installed Conductor binary is not executable")
			}
		}
		if relative != installedManifest.Executable && isBundlePath(relative) {
			bundleFiles = append(bundleFiles, payloadFile{manifestFile: declaredFile})
		}
		seenFiles[relative] = true
		return nil
	})
	if err != nil {
		return manifest{}, nil, err
	}
	for declaredPath := range declaredFiles {
		if !seenFiles[declaredPath] {
			return manifest{}, nil, fmt.Errorf("installed file is missing: %s", declaredPath)
		}
	}
	sort.Slice(bundleFiles, func(i, j int) bool { return bundleFiles[i].Path < bundleFiles[j].Path })
	if digestPayload(bundleFiles) != installedManifest.BundleSHA256 {
		return manifest{}, nil, errors.New("installed bundle digest is invalid")
	}
	executableDigest := declaredFiles[installedManifest.Executable].SHA256
	if distributionIdentity(installedManifest.Format, installedManifest.Protocol, installedManifest.Version, installedManifest.GOOS, installedManifest.GOARCH, installedManifest.BundleSHA256, executableDigest) != installedManifest.DistributionID {
		return manifest{}, nil, errors.New("distribution identity is invalid")
	}
	return installedManifest, manifestData, nil
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
