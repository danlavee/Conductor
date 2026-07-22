//go:build !windows

package platform

// IsTransientFileAccess reports whether a file access error can result from publication.
func IsTransientFileAccess(error) bool { return false }
