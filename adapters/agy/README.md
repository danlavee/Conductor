# Antigravity adapter

The [wake adapter](../../docs/design/wake-adapters.md) for Antigravity (`agy`), packaged as a plugin. It makes a conversation wakeable: team work addressed to its agent starts a real turn, with no tool call to remember and no handle to keep.

## Installing

```
conductor adapter agy install <absolute-directory>/adapters/agy
```

That places this tree and puts the executable at `bin/conductor.exe` on Windows, or `bin/conductor` elsewhere, which is the path the hooks name. Installing twice reports `already-installed` and changes nothing.

Then enable it with the host's own command, run again after every upgrade: the [adapter registry](../../docs/integrations/README.md) row. Conductor does not write the host's plugin registry.

After that, the agent's only action is `join`, from inside the conversation. How the hooks work is in [the integration page](../../docs/integrations/agy.md).
