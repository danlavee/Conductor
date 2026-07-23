# Antigravity integration

Antigravity can keep a blocking command alive through its native background-terminal facility and resume the same attended conversation when that command completes. Run the generic one-shot watcher through that facility:

```text
conductor <agent> watch
```

Retain the background task handle. When a publication completes the command, process its output in the resumed turn and start the same command again. Keep exactly one watcher for the Conductor identity.

This path needs no conversation ID, `agentapi` sidecar, or `agy --print` subprocess. It wakes an open Antigravity conversation; it does not start a fully closed or headless host.
