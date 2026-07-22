//go:build !windows && !linux

package install

import "errors"

func publishNoReplace(string, string) error {
	return errors.New("Conductor installation is supported only on Windows and Linux")
}
