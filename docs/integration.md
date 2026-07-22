# Host integrations

Conductor's state and watch protocol are host-neutral. An integration owns the runtime-specific step that turns a Conductor signal into an agent turn.

Integration topology, activation, and lifecycle are separate properties. See the [integration model](integrations/architecture.md).

See the [integration support matrix](integrations/README.md) before selecting a watcher. A watcher is functional only when its documented host binding is present and its startup validation succeeds.

Implemented integrations:

- [Codex](integrations/codex.md)
- [Antigravity](integrations/agy.md), with native Desktop sidecar and process-per-signal CLI transports
- [Claude Code CLI](integrations/claude-cli.md)
- [Claude Code Channel](integrations/claude-channel.md)

Investigated but not externally bindable:

- [Claude Code Desktop boundary](integrations/claude-desktop.md)

All implemented adapters acquire one crash-released watch-ownership lock per Conductor identity. Delivery and acknowledgement are not atomic: a crash after host acceptance but before acknowledgement may replay a signal, so agents must process signals idempotently.
