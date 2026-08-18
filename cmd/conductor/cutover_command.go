package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	conductor "github.com/danlavee/Conductor"
	"github.com/danlavee/Conductor/internal/cutover"
	"github.com/danlavee/Conductor/internal/state"
)

func runCutoverCommand(args []string) error {
	if len(args) < 2 {
		return cutoverUsageError()
	}
	action, root := args[0], args[1]
	if !filepath.IsAbs(root) {
		return errors.New("cutover root must be absolute")
	}
	switch action {
	case "status":
		if len(args) != 2 {
			return cutoverUsageError()
		}
		current, _, err := cutover.Observe(root)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, current)
	case "freeze":
		id, release, err := cutoverIdentity(args[2:])
		if err != nil {
			return err
		}
		current, err := cutover.Freeze(root, id, release, func() error {
			return state.ValidateCutoverPreflight(root)
		})
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, current)
	case "replace":
		id, err := cutoverID(args[2:])
		if err != nil {
			return err
		}
		current, _, err := cutover.Observe(root)
		if err != nil {
			return err
		}
		if current.Release != currentVersion() {
			return errors.New("replacement command must run from the exact target release")
		}
		if err := state.ValidateReplacementRoot(root); err != nil {
			return err
		}
		current, err = cutover.MarkReplaced(root, id)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, current)
	case "activate":
		id, err := cutoverID(args[2:])
		if err != nil {
			return err
		}
		current, err := cutover.Activate(root, id)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, current)
	case "abort":
		id, err := cutoverID(args[2:])
		if err != nil {
			return err
		}
		current, err := cutover.Abort(root, id)
		if err != nil {
			return err
		}
		return conductor.WriteJSON(os.Stdout, current)
	default:
		return cutoverUsageError()
	}
}

func cutoverIdentity(args []string) (string, string, error) {
	if len(args) != 2 {
		return "", "", cutoverUsageError()
	}
	var id, release string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--id="):
			id = strings.TrimPrefix(arg, "--id=")
		case strings.HasPrefix(arg, "--release="):
			release = strings.TrimPrefix(arg, "--release=")
		default:
			return "", "", cutoverUsageError()
		}
	}
	if strings.TrimSpace(id) == "" || strings.TrimSpace(release) == "" {
		return "", "", cutoverUsageError()
	}
	return id, release, nil
}

func cutoverID(args []string) (string, error) {
	if len(args) != 1 || !strings.HasPrefix(args[0], "--id=") {
		return "", cutoverUsageError()
	}
	id := strings.TrimPrefix(args[0], "--id=")
	if strings.TrimSpace(id) == "" {
		return "", cutoverUsageError()
	}
	return id, nil
}

func cutoverUsageError() error {
	return errors.New("usage: conductor cutover status <absolute-root> | conductor cutover freeze <absolute-root> --id=<id> --release=<release> | conductor cutover replace <absolute-root> --id=<id> | conductor cutover activate <absolute-root> --id=<id> | conductor cutover abort <absolute-root> --id=<id>")
}
