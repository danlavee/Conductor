//go:build linux

package platform

import (
	"fmt"
	"os"
	"strconv"
)

func InspectProcess(pid int) (ProcessInstance, error) {
	fields, ok := linuxProcFields(pid)
	if !ok || len(fields) <= 19 {
		return ProcessInstance{}, fmt.Errorf("cannot identify process %d", pid)
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcessInstance{}, err
	}
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ProcessInstance{}, err
	}
	process := ProcessInstance{Process: Process{PID: pid, Start: fields[19]}, ParentPID: parent, Executable: executable}
	if !process.Alive() {
		return ProcessInstance{}, fmt.Errorf("process %d changed during inspection", pid)
	}
	return process, nil
}
