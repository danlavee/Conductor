// Package platform provides the operating-system primitives used by Conductor.
package platform

import "errors"

// ErrMutexTimeout reports that a crash-released file mutex stayed unavailable.
var ErrMutexTimeout = errors.New("state mutex did not become available")
