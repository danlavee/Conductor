// Package adapterbundle exposes Conductor's host adapters to the installer.
// Each host's file embeds that host's tree, and no Go source, with the all:
// prefix, which carries the dot directories hosts read and an ordinary embed
// directive would silently drop.
package adapterbundle

import "io/fs"

func mustSub(source fs.FS, directory string) fs.FS {
	result, err := fs.Sub(source, directory)
	if err != nil {
		panic(err)
	}
	return result
}
