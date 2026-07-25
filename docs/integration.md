# Host integrations

Conductor's state and watch protocol are host-neutral. An integration owns the runtime-specific step that turns a Conductor signal into an agent turn.

Integration topology, activation, and lifecycle are separate properties. See the [integration model](integrations/architecture.md).

See the [integration support matrix](integrations/README.md) before selecting a watcher. A watcher is functional only when its documented host binding is present and its startup validation succeeds.

Implemented integrations:

- [Codex](integrations/codex.md)
- [Antigravity](integrations/agy.md), through its host-managed background terminal
- [Claude Code CLI](integrations/claude-cli.md), headless, process-per-signal resume of a separate session
- [Claude Code CLI or Desktop, interactive session](integrations/claude-interactive.md)

Investigated but not externally bindable:

- [Claude Code Desktop boundary](integrations/claude-desktop.md), no headless mechanism

All implemented adapters acquire one crash-released watch-ownership lock per Conductor identity. Delivery and acknowledgement are not atomic: a crash after host acceptance but before acknowledgement may replay a signal, so agents must process signals idempotently.
