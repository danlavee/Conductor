// Package adapterbundle exposes Conductor's host adapters to the installer.
package adapterbundle

import (
	"embed"
	"io/fs"
)

// embedded contains the adapter trees and no Go source. The all: prefix is
// what carries `.claude-plugin`, which the host reads and which an ordinary
// embed directive would silently drop for beginning with a dot.
//
//go:embed all:claude-code
var embedded embed.FS

// ClaudeCode is rooted at the plugin manifest the host loads.
var ClaudeCode = mustSub(embedded, "claude-code")

func mustSub(source fs.FS, directory string) fs.FS {
	result, err := fs.Sub(source, directory)
	if err != nil {
		panic(err)
	}
	return result
}
