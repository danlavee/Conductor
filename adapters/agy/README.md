# Antigravity adapter

The [wake adapter](../../docs/design/wake-adapters.md) for Antigravity (`agy`), packaged as a plugin. It makes a session wakeable: team work addressed to this agent starts a real turn, with no tool call to remember and no handle to keep.

## Installing

```
conductor adapter agy install <absolute-directory>/adapters/agy
```

That places this tree and puts the executable at `bin/conductor.exe` on Windows, or `bin/conductor` elsewhere — the exact path the hooks name, since they are rewritten to the installed binary path at install time. Staging, hashing, verification and re-running are the same as for the skill: installing twice reports `already-installed` and changes nothing.

Two steps remain, and both are deliberately the user's:

1. Point the host at it and enable the plugin with the host's own commands: `agy plugin validate <the installed adapters/agy directory>` and `agy plugin enable conductor`. Conductor does not write the host's plugin registry.
2. Bind each project to an identity by writing its agent name into a `.conductor-agent` file at the project root. Plain UTF-8 with no byte order mark is easiest, though one is tolerated.

Without step 2 the adapter loads and does nothing, which is the intended behaviour for a project that has not opted in.

For a second agent working in a project that is already bound to a different one, the project file cannot help — it names one identity for every session that opens there. That agent binds its own session instead, once, from inside it:

```
conductor adapter agy bind <agent>
```

The binding is recorded against the session, outranks the project file for that session alone, and is forgotten when the session ends. Two things have to be true first, and both are refused outright rather than accepted into silence: the identity is on the roster, and this session is one the adapter's own `SessionStart` hook has served. A session started before the plugin was installed or enabled has no hook to read the binding, so recording one would be a no-op lasting the whole session — start a new session instead.

After that, the agent's only action is `join`. The plugin holds the stream from then on.

## What it registers

| Hook | Responsibility | Behaviour |
| --- | --- | --- |
| `SessionStart` | Lifecycle trigger | Records that this adapter serves the session, reports the bound identity, and starts the resident that holds the stream. |
| `Stop` | Turn-boundary trigger | While the session is idle, stays until a delivery is ready, then continues the turn with it. A missing receipt is shown again. |

Antigravity does not run a background rewake hook, so the resident and the turn-boundary trigger are two processes. The resident holds the stream. `Stop` is what starts the turn. A delivery's records are in a file. The continuation reason carries a short receipt token and that path. End the continuation with the token as the final nonblank line. The adapter acknowledges only then.
