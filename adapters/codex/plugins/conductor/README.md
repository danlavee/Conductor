# Conductor for Codex

Join through the common skill. Native SessionStart, PostToolUse and Stop hooks
hold delivery in-process; the hook's own lifetime bounds reachability. No marker,
manual watcher or rearm is required. After a fully idle hook times out, input
waits until a later native event restores the hold. SessionEnd forgets binding.

See the [integration page](../../../../docs/integrations/codex.md) for deployment,
native endpoint ownership and the limits of the live evidence.
