# Integration support matrix

| Target | Topology | Lifecycle | Activation | Command |
| --- | --- | --- | --- | --- |
| Codex attended chat | Host-managed terminal | One-shot, rearmed by the agent | Background-terminal completion | `conductor <agent> watch` |
| Antigravity attended conversation | Host-managed terminal | One-shot, rearmed by the agent | Background-terminal completion | `conductor <agent> watch` |
| Claude Code CLI session | External launcher | Process-per-signal | `claude -p --resume` | `conductor <agent> watch --claude-cli` |
| Open Claude Code CLI session | Host-connected channel | Persistent | Channel notification | `conductor <agent> channel claude` |
| Claude Code Desktop task | No supported bridge | — | No verified external turn activation | None |

See [Integration model](architecture.md) for the classification rules. Host-specific identity and failure boundaries are documented in each integration page.

Every row's watch command accepts `--mode=summary` to leave the signal's data for the awakened turn to read itself, instead of resolving and acknowledging it (a topic delta or the roster) before delivery — the default is to resolve it inline. See [runtime boundaries](../design/runtime-boundaries.md#the-acceptance-boundary).
