# Antigravity adapter

Status: not yet built. Antigravity uses the [interim fallback](README.md#interim-fallback) until its adapter lands.

## What the adapter must own

The same three responsibilities as every adapter. Antigravity keeps a blocking command alive through its native background-terminal facility and resumes the same attended conversation when that command completes — a wake confirmed live, and the likely basis for the resident component.

What is missing is restoration without the agent: today the resumed turn has to start the next command itself. The adapter has to own that, and needs a host event at session start and at turn boundary to hang it on.

This host needs no conversation ID, sidecar, or `--print` subprocess. It wakes an open conversation; it does not start a closed or headless host.
