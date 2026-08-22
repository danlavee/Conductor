# Adapter registry

An adapter is one installable unit per host. It owns four responsibilities — a resident component that holds the delivery stream, a turn-boundary trigger that prevents an idle turn while deliveries pend, a lifecycle trigger that binds identity and restores the stream, and teardown that ends the stream with its session. See [the model](../design/model.md) for what they exist to guarantee and [wake adapters](../design/wake-adapters.md) for the requirements they satisfy.

Conductor's core is host-neutral. `watch` acknowledges the arming act and then delivers on one continuous stream, with identical semantics everywhere, and `status` answers what holds an identity's stream, what reaches it, and what serves the host; nothing vendor-specific lives in either.

| Host | Adapter | Status |
| --- | --- | --- |
| [Claude Code](claude-code.md) | Plugin at [`adapters/claude-code/`](../../adapters/claude-code/README.md): `SessionStart`, `Stop`, and `SessionEnd` hooks over `conductor adapter claude` | Built, coverage unverified |
| [Codex](codex.md) | Not yet built | Interim fallback |
| [Antigravity](agy.md) | Not yet built | Interim fallback |
| Any other host | — | Interim fallback |

Summary delivery leaves a delivery's data for the woken turn to read itself instead of resolving and acknowledging it inline. An adapter chooses it when it arms; there is no flag, because the CLI is not how an adapter holds a stream. See [runtime boundaries](../design/runtime-boundaries.md#the-acceptance-boundary).

## Interim fallback

A host without an adapter uses `conductor <agent> watch --once`, and the agent owns the lifecycle: start it through the host's background facility, retain the handle, process the delivery, start it again. It writes two JSON documents, not one — the participation answer at the moment it acquires ownership, so arming reports what it achieved, and then the single delivery it blocks for before exiting. Parse its stdout as a sequence, and read the exit code with it: a failed wait leaves the arm answer written and reports on stderr, so one document at a non-zero exit is a watch that ended, not a delivery still coming.

`--once` is a lifecycle flag, not a vendor flag — the core still knows nothing about which host is calling it — and it is not merely a transition shim. Every wake verified on an un-adapted host works by the watch *process completing*: the harness notices the backgrounded task finish and resumes the turn. A stream that never exits cannot be woken that way. So `--once` is the primitive for hosts whose wake is process completion, and it outlives the transition even though it is also what the un-migrated path uses.

What does go away as adapters land is the agent owning the lifecycle.

Adapter lifetime, retry after host acceptance, and behavior when an accepted delivery never reaches an agent are host-specific policy. They live with each adapter, not in the core.
