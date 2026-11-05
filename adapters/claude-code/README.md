# Claude Code adapter

The [wake adapter](../../docs/design/wake-adapters.md) for Claude Code, packaged as a plugin. It makes a session wakeable: team work addressed to this agent starts a real turn, with no tool call to remember and no handle to keep.

## Installing

```
conductor adapter claude install <absolute-directory>/adapters/claude-code
```

That places this tree and puts the executable at `bin/conductor` inside it (`bin/conductor.exe` on Windows) — the exact path the hooks name, since they are exec-form and name no shell. Staging, hashing, verification and re-running are the same as for the skill: installing twice reports `already-installed` and changes nothing.

One step remains, and it is deliberately the user's: point the host at it, once, with `claude plugin marketplace add <the directory above>`, then install `conductor@conductor-adapters`. Conductor does not write the host's plugin registry — that is private, undocumented state, and the [design note](../../docs/design/wake-adapters.md) says why depending on it would be a mistake.

Nothing is configured per project. A session the adapter serves acts as whichever agent joins from inside it: `join` answers `"wake": "adapter"` and binds the session to that name, and the binding is forgotten when the session ends. Until something joins, the adapter loads and does nothing. A session started before the plugin was enabled was never served, so its join answers `"watch"` — start a new session to have the adapter wake it.

## What it registers

| Hook | Responsibility | Behaviour |
| --- | --- | --- |
| `SessionStart` (`startup`, `resume`, `clear`, `compact`, `fork`) | Lifecycle trigger | Marks the session as served, announces the agent it is bound to, if any, and arms a delivery stream. |
| `Stop` | Resident component and turn-boundary trigger | Arms a delivery stream at every turn end. Backgrounded, so it never delays the turn. |
| `SessionEnd` | Teardown | Ends this session's stream so it does not outlive the session and block the next one. |

Both arming hooks run the same command. That is deliberate: one stream process wakes at most once, and the event that ends the woken turn arms its successor, so residency and re-arming are the same act. Arming when a live stream already owns the identity is a no-op, which is what makes it safe to arm blindly from several events.

## Why the timeout is a day

The host kills a backgrounded hook at its configured `timeout`, silently and without running the process's own exit path. There is no upper clamp, so the stream is given a deliberately long life. If it is killed anyway, the next turn boundary arms a replacement — the consequence is delivery latency, never a lost delivery.

## Identity binding

The binding is made by `join`, the one command an agent runs anyway, so there is no second name to state and nothing to get wrong after a compaction: the hooks read the session's binding for themselves. Two sessions in one directory join as two names and hold two streams. Two sessions joining as the *same* name share one stream, and only the first to arm holds it — see [the integration page](../../docs/integrations/claude-code.md) for what that costs.
