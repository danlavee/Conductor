# Antigravity

The Antigravity adapter is a plugin at [`adapters/agy/`](../../adapters/agy/README.md). It runs the same holder and watcher as the [Claude adapter](claude-code.md#holder-and-watcher); only the hooks, the session process and how a delivery enters a turn are Antigravity's. Each hook runs `conductor adapter agy <identity|arm>` and takes its conversation from the payload on stdin, never from the environment.

| Hook | Runs | Does |
| --- | --- | --- |
| `SessionStart` | `identity` | Serves the conversation, so a `join` from inside it binds the conversation and answers `"wake": "adapter"`. |
| `Stop` | `arm` | The watcher. When the model ended the turn itself (`terminationReason` `NO_TOOL_CALL`) in a bound conversation, it spawns the holder and waits for a delivery, then returns `decision: "continue"` with the plain-text wake in `reason`, and acknowledges what the wake carried. |

Both hooks write `{}` when they have nothing to say. Antigravity has no background hook, so a waiting `Stop` hook is the only way to start a turn in an idle conversation, and its timeout is set to a day. The host runs each hook command through a shell (`cmd /c` or `sh -c`), so the holder's target is the hook's first ancestor that is not a shell.

The continue decision's `reason` is the plain-text wake, which shows at most 5 records within 10,000 UTF-16 code units; what the wake did not show stays unread for the next stop.

Antigravity has no `SessionEnd` hook. A hold ends when the target process dies, when the conversation is rebound by a `join` under another name, or when the waiting `Stop` hook is killed without delivering, which the holder reads from the watcher's waker record.

Known limits:

- Antigravity caps continuations without consulting the hook. The holder then holds with no watcher until the next turn ends. Nothing is lost: what was not shown stays unread.
- A continuation the host consults and then ignores loses that one wake; the publication stays readable with `get`.

Install places this tree and its executable together:

`go run github.com/danlavee/Conductor/cmd/conductor@latest adapter agy install "<absolute path ending in adapters/agy>"`

Pass `--replace` to upgrade, in the same step as the skill. Enabling it and picking up each upgrade are the user's commands, in the [registry](README.md) row. Conductor does not write Antigravity's plugin registry.

On November 9 this mechanism passed an isolated native gate on CLI 1.5.3 against a scratch bus: join reports the adapter, an idle publication starts a turn, a burst of seven arrives as wakes of 5 and 2 records, a second conversation takes over and gets the next wake, and the session's exit releases the holder. The CLI's ancestor chain, the payload keys, `NO_TOOL_CALL`, and commands inheriting `ANTIGRAVITY_CONVERSATION_ID` were observed there. Unobserved so far: the ancestor chain on Desktop, whether closing or interrupting a conversation kills the waiting `Stop` hook, and the size limit on `reason`.
