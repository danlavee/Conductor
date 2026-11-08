package platform

import (
	"fmt"
	"strconv"
	"strings"
)

// Process is one OS process recorded with its start token, so a recycled PID
// reads dead.
type Process struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

// ProcessInstance is a process as inspected, with what it runs and its parent.
type ProcessInstance struct {
	Process
	ParentPID  int
	Executable string
}

// ProcessOf records pid as it is now, or fails when its start token cannot be
// read. Whatever records a process to judge it alive later must refuse to
// record it without one: a bare PID could be recycled and kept alive forever.
// An exited process is refused too, though an open handle can keep its token
// readable on Windows.
func ProcessOf(pid int) (Process, error) {
	start, ok := ProcessStartToken(pid)
	if !ok || !ProcessAlive(pid) {
		return Process{}, fmt.Errorf("cannot read the start token of process %d", pid)
	}
	return Process{PID: pid, Start: start}, nil
}

// Alive reports whether p is still the live process it was recorded as. A
// start that is empty or cannot be read back reads dead: a stream wrongly
// dropped is restored at the next turn boundary, while one wrongly kept for a
// recycled PID is never released.
func (p Process) Alive() bool {
	if p.PID <= 0 || p.Start == "" || !ProcessAlive(p.PID) {
		return false
	}
	current, ok := ProcessStartToken(p.PID)
	return ok && current == p.Start
}

// String is pid:start, which ParseProcess reads back.
func (p Process) String() string { return strconv.Itoa(p.PID) + ":" + p.Start }

// ParseProcess reads a String value back.
func ParseProcess(s string) (Process, error) {
	pid, start, ok := strings.Cut(s, ":")
	n, err := strconv.Atoi(pid)
	if !ok || err != nil || n <= 0 || start == "" {
		return Process{}, fmt.Errorf("process %q is not pid:start", s)
	}
	return Process{PID: n, Start: start}, nil
}
