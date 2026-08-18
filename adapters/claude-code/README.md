# Claude Code adapter

The [wake adapter](../../docs/design/wake-adapters.md) for Claude Code, packaged as a plugin. It makes a session wakeable: team work addressed to this agent starts a real turn, with no tool call to remember and no handle to keep.

## Installing

1. Copy this directory to wherever the host loads plugins from.
2. Place the Conductor executable at `bin/conductor` inside it (`bin/conductor.exe` on Windows). The hooks are exec-form and name no shell, so this one path is all the wiring there is.
3. Bind the project to an identity by writing its agent name into a `.conductor-agent` file at the project root.

Without step 3 the adapter loads and does nothing, which is the intended behaviour for a project that has not opted in.

## What it registers

| Hook | Responsibility | Behaviour |
| --- | --- | --- |
| `SessionStart` (`startup`, `resume`, `clear`, `compact`, `fork`) | Lifecycle trigger | Reports the bound identity into the session's context, and arms a delivery stream. |
| `Stop` | Resident component and turn-boundary trigger | Arms a delivery stream at every turn end. Backgrounded, so it never delays the turn. |
| `SessionEnd` | Teardown | Ends this session's stream so it does not outlive the session and block the next one. |

Both arming hooks run the same command. That is deliberate: one stream process wakes at most once, and the event that ends the woken turn arms its successor, so residency and re-arming are the same act. Arming when a live stream already owns the identity is a no-op, which is what makes it safe to arm blindly from several events.

## Why the timeout is a day

The host kills a backgrounded hook at its configured `timeout`, silently and without running the process's own exit path. There is no upper clamp, so the stream is given a deliberately long life. If it is killed anyway, the next turn boundary arms a replacement — the consequence is delivery latency, never a lost delivery.

## Identity binding

`.conductor-agent` is a project file the adapter reads for itself. It is not an argument the model supplies, because an identity the model states is one it can get wrong, forget, or lose to a compaction. Plugin-level user configuration cannot be read from project settings on this host, so a project-owned file is the only binding that can vary per project.

One identity holds one stream. Two sessions open on the same project therefore share an identity, and only the first to arm holds it — see [the integration page](../../docs/integrations/claude-code.md) for what that costs.
