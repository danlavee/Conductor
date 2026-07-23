// Package skillbundle exposes Conductor's canonical skill payload to the installer.
package skillbundle

import (
	"embed"
	"io/fs"
)

// embedded contains the portable skill and no Go source.
//
//go:embed all:conductor/SKILL.md all:conductor/references all:conductor/assets
var embedded embed.FS

// Files is rooted at the portable skill's SKILL.md.
var Files = mustSub(embedded, "conductor")

func mustSub(source fs.FS, directory string) fs.FS {
	result, err := fs.Sub(source, directory)
	if err != nil {
		panic(err)
	}
	return result
}
