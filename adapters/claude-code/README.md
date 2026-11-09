# Claude Code adapter

The [wake adapter](../../docs/design/wake-adapters.md) for Claude Code, packaged as a plugin. It makes a session wakeable: team work addressed to this agent starts a real turn, with no tool call to remember and no handle to keep.

## Installing

```
conductor adapter claude install <absolute-directory>/adapters/claude-code
```

That places this tree and puts the executable at `bin/conductor` inside it (`bin/conductor.exe` on Windows) — the exact path the hooks name, since they are exec-form and name no shell. Staging, hashing, verification and re-running are the same as for the skill: installing twice reports `already-installed` and changes nothing.

One step remains, and it is deliberately the user's: point the host at it, once, with `claude plugin marketplace add <the directory above>`, then install `conductor@conductor-adapters`. After every upgrade, `claude plugin update conductor@conductor-adapters` and a restart pick up the new tree. Conductor does not write the host's plugin registry — that is private, undocumented state, and the [design note](../../docs/design/wake-adapters.md) says why depending on it would be a mistake.

Nothing is configured per project. A session the adapter serves acts as whichever agent joins from inside it: `join` answers `"wake": "adapter"` and binds the session to that name, and the binding is forgotten when the session ends. Until something joins, the adapter loads and does nothing. A session started before the plugin was enabled was never served, so its join answers `"watch"` — start a new session to have the adapter wake it.

How the hooks keep the session wakeable — the holder, the watcher, and when each ends — is on [the integration page](../../docs/integrations/claude-code.md).
