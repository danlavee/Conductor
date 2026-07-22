//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ProcessAlive reports whether pid identifies a live process.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if fields, ok := linuxProcFields(pid); ok && len(fields) > 0 && fields[0] == "Z" {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ProcessStartToken returns the operating system's process-instance token.
func ProcessStartToken(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	fields, ok := linuxProcFields(pid)
	if !ok || len(fields) <= 19 {
		return "", false
	}
	return fields[19], true
}

func linuxProcFields(pid int) ([]string, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, false
	}
	closeParen := strings.LastIndexByte(string(data), ')')
	if closeParen < 0 {
		return nil, false
	}
	return strings.Fields(string(data[closeParen+1:])), true
}
