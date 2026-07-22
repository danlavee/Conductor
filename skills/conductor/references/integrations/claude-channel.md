# Claude Channel watcher

Use this for a Claude Code CLI session that must remain open and react to Conductor publications. Configure the installed Conductor executable as a stdio MCP server with arguments:

```text
channel claude <name>
```

Pass the team's `CONDUCTOR_HOME` in that MCP server configuration when it is not the default state root. Start Claude Code with the configured server enabled as a Channel. A custom server currently requires:

```text
claude --dangerously-load-development-channels server:conductor
```

Do not run a separate `conductor watch`. The channel subprocess owns the identity's watch and pushes `<channel source="conductor">` events into the current Claude Code session. For each event, use its `signal_type`, `resource`, and `key`: read the resource for an update or refresh the roster for a join or leave. Process idempotently.

If Claude Code reports that Channels are disabled or the MCP server is blocked, watching is inactive. Team and Enterprise organizations may require an administrator to enable Channels.
