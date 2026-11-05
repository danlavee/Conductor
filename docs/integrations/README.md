# Adapter registry

An adapter is one installable unit per host. It owns four responsibilities — a resident component that holds the delivery stream, a turn-boundary trigger that prevents an idle turn while deliveries pend, a lifecycle trigger that serves the session and restores the stream, and teardown that ends the stream with its session. See [the model](../design/model.md) for what they exist to guarantee and [wake adapters](../design/wake-adapters.md) for the requirements they satisfy.

Conductor's core is host-neutral. `join` answers `"wake": "adapter"` exactly when an adapter served the session it runs in, and binds that session to the joining name; everywhere else it answers `"watch"`. `status` answers what holds an identity's stream. Nothing vendor-specific lives in either.

This table is the one place that names each host's adapter. [Installation](../installation.md) and the [update procedure](../../skills/conductor/references/update-skill.md) run whatever its row says.

| Host | `<host>` | Destination ends in | Enable (the user's step) | Status |
| --- | --- | --- | --- | --- |
| [Claude Code](claude-code.md) | `claude` | `adapters/claude-code` | `claude plugin marketplace add <destination>`, then `claude plugin install conductor@conductor-adapters` | Built; join binds the session |
| [Grok](grok.md) | `grok` | `adapters/grok` | `grok plugin marketplace add <destination>`, then `grok plugin install conductor --trust` | Built; join binding pending, live gates not run |
| [Antigravity](agy.md) | `agy` | `adapters/agy` | `agy plugin enable conductor` | Built; join binding pending |
| [Codex](codex.md) | — | — | — | Not yet built: fallback |
| Any other host | — | — | — | Fallback |

Every built adapter installs the same way, placing its plugin tree and the executable its hooks name together:

```bash
go run github.com/danlavee/Conductor/cmd/conductor@latest adapter <host> install "<absolute destination>"
```

Conductor never writes a host's plugin registry or trust store, so enabling stays the user's.

## Fallback

On a host without an adapter, `join` answers `"watch"` and the agent owns the lifecycle: start `conductor <agent> watch` through the host's background facility, retain the handle, process the delivery, start it again. It blocks for one delivery, writes it as one JSON document, and exits; a failed wait exits non-zero with stdout empty and the reason on stderr. That exit is the wake on every host verified so far: the harness notices the backgrounded task finish and resumes the turn.

Adapter lifetime, retry after host acceptance, and behavior when an accepted delivery never reaches an agent are host-specific policy. They live with each adapter, not in the core.
