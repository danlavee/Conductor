# Install the Conductor skill

## Guidance

Install the skill and its matching native CLI together. The installer requires Go 1.22 or newer.

Before installing, ask: "Install for this project or for the user across projects?" Do not infer the scope.

## Steps

1. Resolve and show the absolute destination, using the installing host's own vendor-specific skills directory if it has one, or the generic `.agents` convention otherwise:
   - Project: `<project>/<vendor-dir>/skills/conductor`
   - User: `$HOME/<vendor-dir>/skills/conductor`
2. Confirm that its parent is the intended skills directory. For a project install, disclose that this changes the project tree.
3. Wait for confirmation, then run:

```bash
go run github.com/danlavee/Conductor/cmd/conductor@latest install "<absolute-skill-directory>"
```

## Behavior rules

Use the confirmed absolute destination as the command's only argument. Do not infer another destination, use a relative path, or replace a different existing installation.

The installed manifest binds the skill, native binary, software release, platform, and supported state protocol. Re-running the same distribution is idempotent. A different manifest format, protocol, or payload is a conflict; the installer does not replace or migrate it in place.

`conductor version` reports the software release and numeric state protocol without opening or creating shared state.

## Interactivity rules

Do not install until the user has chosen the scope and confirmed the exact destination. Stop and report any conflict instead of choosing or repairing a location yourself.
