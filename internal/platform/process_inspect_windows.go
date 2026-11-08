//go:build windows

package platform

import (
	"fmt"
	"syscall"
	"unsafe"
)

var queryFullProcessImageName = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")

func InspectProcess(pid int) (ProcessInstance, error) {
	token, ok := ProcessStartToken(pid)
	if !ok {
		return ProcessInstance{}, fmt.Errorf("cannot identify process %d", pid)
	}
	const processSnapshot = 0x2
	snapshot, err := syscall.CreateToolhelp32Snapshot(processSnapshot, 0)
	if err != nil {
		return ProcessInstance{}, err
	}
	defer syscall.CloseHandle(snapshot)
	entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	err = syscall.Process32First(snapshot, &entry)
	for err == nil && entry.ProcessID != uint32(pid) {
		err = syscall.Process32Next(snapshot, &entry)
	}
	if err != nil {
		return ProcessInstance{}, err
	}
	const queryLimitedInformation = 0x1000
	handle, _, callErr := openProcess.Call(queryLimitedInformation, 0, uintptr(uint32(pid)))
	if handle == 0 {
		return ProcessInstance{}, callErr
	}
	defer closeHandle.Call(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	result, _, callErr := queryFullProcessImageName.Call(handle, 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if result == 0 {
		return ProcessInstance{}, callErr
	}
	process := ProcessInstance{Process: Process{PID: pid, Start: token}, ParentPID: int(entry.ParentProcessID), Executable: syscall.UTF16ToString(buffer[:size])}
	if !process.Alive() {
		return ProcessInstance{}, fmt.Errorf("process %d changed during inspection", pid)
	}
	return process, nil
}
