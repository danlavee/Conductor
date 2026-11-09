# Adapter registry

An adapter is one installable unit per host. It owns four responsibilities — a resident component that holds the delivery stream, a turn-boundary trigger that prevents an idle turn while deliveries pend, a lifecycle trigger that serves the session and restores the stream, and teardown that ends the stream with its session. See [the model](../design/model.md) for what they exist to guarantee and [wake adapters](../design/wake-adapters.md) for the requirements they satisfy.

Conductor's core is host-neutral. `join` answers `"wake": "adapter"` exactly when an adapter served the session it runs in, and binds that session to the joining name; everywhere else it answers `"watch"`. The join roster answers what holds each identity's stream. Nothing vendor-specific lives in either.

This table is the one place that names each host's adapter. [Installation](../installation.md) and the [update procedure](../../skills/conductor/references/update-skill.md) run whatever its row says.

| Host | `<host>` | Destination ends in | Enable, once (the user's step) | Update, after each `--replace` (the user's step) | Live gates |
| --- | --- | --- | --- | --- | --- |
| [Claude Code](claude-code.md) | `claude` | `adapters/claude-code` | `claude plugin marketplace add <destination>`, then `claude plugin install conductor@conductor-adapters` | `claude plugin update conductor@conductor-adapters`, then restart the sessions | Wake passed; holder and watcher split not run |
| [Grok](grok.md) | `grok` | `adapters/grok` | `grok plugin install <destination>`, then accept its trust prompt | `grok plugin uninstall conductor --confirm`, then `grok plugin install <destination>` again, accepting its trust prompt, then start new sessions; `grok plugin update` does not refresh it | CLI passed |
| [Antigravity](agy.md) | `agy` | `adapters/agy` | `agy plugin install <destination>`; it is enabled on install | `agy plugin install <destination>` again, then start new conversations | CLI passed |
| [Codex](codex.md) | `codex` | `adapters/codex` | `codex plugin marketplace add <destination>`, then `codex plugin add conductor@conductor-adapters`, then approve its hooks in the app | `codex plugin add conductor@conductor-adapters` again, approve the changed hooks, then start new tasks | CLI passed; Desktop passed except takeover |
| Any other host | — | — | — | — | Fallback |

Every host runs its own copy of the plugin, made when it was enabled. Claude and Codex file that copy under the version in the plugin's manifest, which is why each release installs with its own version. Until the update command runs, the host keeps running the old copy and the old executable, which can recreate a bus the update moved aside. Run it with the host's sessions stopped: on Windows a running hook holds the old executable open.

Every built adapter installs the same way, placing its plugin tree and the executable its hooks name together:

```bash
go run github.com/danlavee/Conductor/cmd/conductor@latest adapter <host> install "<absolute destination>"
```

Conductor never writes a host's plugin registry or trust store, so enabling stays the user's.

## Fallback

On a host without an adapter, `join` answers `"watch"` and the agent owns its own wake, by the `"watch"` rule in [SKILL.md](../../skills/conductor/SKILL.md) step 2; [protocol.md](../../skills/conductor/references/protocol.md) gives what `watch` writes and how it exits.

Adapter lifetime, retry after host acceptance, and behavior when an accepted delivery never reaches an agent are host-specific policy. They live with each adapter, not in the core.
