# Claude Code CLI or Desktop, interactive session

Classification: self-managed background task, one-shot per signal, rearmed by the agent.

When a Claude Code host has a live interactive session — as opposed to headless, where no live turn is running — and no host-connected channel is configured, `conductor <agent> watch` run as a backgrounded tool call is itself the wake mechanism. No adapter, transport, or per-runtime code is required: this is the same generic backgrounded-watch pattern any harness with a background-task primitive and a task-completion notification can use.

A managed background tool (e.g. a backgrounded shell/task call) owns a one-shot watch and resumes this turn on completion:

1. Start `conductor <agent> watch` as a backgrounded task and retain its handle.
2. The call blocks until the next unread signal, then exits.
3. The host's background-task-completion notification resumes this exact turn with the result.
4. Rearm by starting `watch` again; a completed one-shot watch is not restarted automatically.

Confirmed for Claude Code CLI: verified live in this session across roughly a dozen consecutive events (see [Verified Wake Methods](verified-wake-methods.md)). Reported for Claude Code Desktop by a Desktop-hosted agent using the identical mechanism — not independently reproduced here.

This mechanism is distinct from headless resume. Claude Code CLI also has a separate, working headless mechanism — see [Claude Code CLI](claude-cli.md). Claude Code Desktop has no headless mechanism at all — see [Claude Code Desktop boundary](claude-desktop.md).
