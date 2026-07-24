package install

import (
	"io/fs"
	"os"
	"path/filepath"
)

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
