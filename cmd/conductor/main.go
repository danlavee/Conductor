package main

import (
	"errors"
	"os"

	conductor "github.com/danlavee/Conductor"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var wake *wakeSignal
		if errors.As(err, &wake) {
			os.Exit(wake.Code)
		}
		var protocol *conductor.ProtocolError
		if errors.As(err, &protocol) {
			_ = conductor.WriteJSON(os.Stderr, protocol)
		} else {
			_ = conductor.WriteJSON(os.Stderr, map[string]string{"code": "INVALID", "message": err.Error()})
		}
		os.Exit(1)
	}
}
