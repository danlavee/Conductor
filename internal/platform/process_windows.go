//go:build windows

package platform

import (
	"errors"
	"strconv"
	"syscall"
	"unsafe"
)

var (
	openProcess        = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenProcess")
	getExitCodeProcess = syscall.NewLazyDLL("kernel32.dll").NewProc("GetExitCodeProcess")
	getProcessTimes    = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessTimes")
	closeHandle        = syscall.NewLazyDLL("kernel32.dll").NewProc("CloseHandle")
)

// ProcessAlive reports whether pid identifies a live process.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const queryLimitedInformation = 0x1000
	handle, _, callErr := openProcess.Call(queryLimitedInformation, 0, uintptr(uint32(pid)))
	if handle == 0 {
		const errorInvalidParameter = syscall.Errno(87)
		return !errors.Is(callErr, errorInvalidParameter)
	}
	defer closeHandle.Call(handle)
	var exitCode uint32
	ok, _, _ := getExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	const stillActive = 259
	return ok != 0 && exitCode == stillActive
}

type windowsFiletime struct {
	Low  uint32
	High uint32
}

// ProcessStartToken returns the operating system's process-instance token.
func ProcessStartToken(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	const queryLimitedInformation = 0x1000
	handle, _, _ := openProcess.Call(queryLimitedInformation, 0, uintptr(uint32(pid)))
	if handle == 0 {
		return "", false
	}
	defer closeHandle.Call(handle)
	var creation, exit, kernel, user windowsFiletime
	ok, _, _ := getProcessTimes.Call(
		handle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ok == 0 {
		return "", false
	}
	value := uint64(creation.High)<<32 | uint64(creation.Low)
	return strconv.FormatUint(value, 16), true
}
