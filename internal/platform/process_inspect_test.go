package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectProcessIdentifiesCurrentInstance(t *testing.T) {
	process, err := InspectProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if process.PID != os.Getpid() || process.ParentPID != os.Getppid() || !filepath.IsAbs(process.Executable) || !process.Alive() {
		t.Fatalf("process=%+v", process)
	}
	process.Start += "wrong"
	if process.Alive() {
		t.Fatal("mismatched process creation token accepted")
	}
}
