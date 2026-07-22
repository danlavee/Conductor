# Integration support matrix

| Target | Topology | Lifecycle | Activation | Command |
| --- | --- | --- | --- | --- |
| Codex local task | Host-owned bridge | One signal per call | Native task message | `conductor watch --codex <agent>` |
| Codex local task | External launcher | Process-per-signal | `codex exec resume` | `conductor watch --codex-cli <agent>` |
| Antigravity Desktop conversation | External bridge | Persistent | `agentapi send-message` | `conductor watch --agy <agent>` |
| AGY CLI conversation | External launcher | Process-per-signal | `agy --print` resume | `conductor watch --agy-cli <agent>` |
| Claude Code CLI session | External launcher | Process-per-signal | `claude -p --resume` | `conductor watch --claude-cli <agent>` |
| Open Claude Code CLI session | Host-connected channel | Persistent | Channel notification | `conductor channel claude <agent>` |
| Claude Code Desktop task | No supported bridge | — | No verified external turn activation | None |

See [Integration model](architecture.md) for the classification rules. Host-specific identity and failure boundaries are documented in each integration page.
