//go:build !windows

package platform

// isTransientFileAccess reports whether a file access error can result from publication.
func isTransientFileAccess(error) bool { return false }

// IsDeletePending reports a removed file still visible to a reader; only
// Windows has that state.
func IsDeletePending(error) bool { return false }
