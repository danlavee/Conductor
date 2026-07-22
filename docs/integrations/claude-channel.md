# Claude Code Channel integration

Classification: host-connected channel, persistent, live-session notification.

The Claude Channel is the native push path for an already-running Claude Code CLI session. Claude Code starts Conductor as a local MCP stdio server. After MCP initialization, Conductor advertises the experimental `claude/channel` capability, holds the agent's watch, and sends `notifications/claude/channel` for each signal.

Configure an MCP server using the installed Conductor executable's absolute path:

```json
{
  "mcpServers": {
    "conductor": {
      "type": "stdio",
      "command": "C:\\absolute\\path\\to\\conductor.exe",
      "args": ["channel", "claude", "tester1"],
      "env": {
        "CONDUCTOR_HOME": "C:\\absolute\\path\\to\\team-state"
      }
    }
  }
}
```

Then start Claude Code with the channel enabled:

```text
claude --dangerously-load-development-channels server:conductor
```

During Anthropic's research preview, a custom channel requires the development flag unless its plugin is on the effective organization allowlist. Team and Enterprise organizations must also enable Channels.

The adapter acknowledges after the notification has been written to Claude Code's MCP transport. It includes only signal location and metadata, not shared-message payload. Claude reads the referenced resource through the installed Conductor skill. If Claude Code exits, the stdio connection closes and the channel stops.
