# Antigravity

The Antigravity adapter is a plugin at [`adapters/agy/`](../../adapters/agy/README.md). It runs the same holder and watcher as the [Claude adapter](claude-code.md#holder-and-watcher); only the hooks, the session process and how a delivery enters a turn are Antigravity's. Each hook runs `conductor adapter agy <identity|arm>` and takes its conversation from the payload on stdin, never from the environment.

| Hook | Runs | Does |
| --- | --- | --- |
| `SessionStart` | `identity` | Serves the conversation, so a `join` from inside it binds the conversation and answers `"wake": "adapter"`. |
| `Stop` | `arm` | The watcher. When the model ended the turn itself (`terminationReason` `NO_TOOL_CALL`) in a bound conversation, it spawns the holder and waits for a delivery, then returns `decision: "continue"` with the delivery batch as JSON in `reason`, and acknowledges it. |

Both hooks write `{}` when they have nothing to say. Antigravity has no background hook, so a waiting `Stop` hook is the only way to start a turn in an idle conversation, and its timeout is set to a day. The host runs each hook command through a shell (`cmd /c` or `sh -c`), so the holder's target is the hook's first ancestor that is not a shell.

Antigravity has no `SessionEnd` hook. A hold ends when the target process dies, when the conversation is rebound by a `join` under another name, or when the waiting `Stop` hook is killed without delivering, which the holder reads from the watcher's waker record.

Known limits:

- Until its live gate, this adapter hands the unread batch over as JSON, up to 20 records, rather than the shared plain-text wake. That is a known gap, to close in code.
- Antigravity caps continuations without consulting the hook. The holder then holds with no watcher until the next turn ends. Nothing is lost: what was not shown stays unread.
- A continuation the host consults and then ignores loses that one wake; the publication stays readable with `get`.

Install places this tree and its executable together:

`go run github.com/danlavee/Conductor/cmd/conductor@latest adapter agy install "<absolute path ending in adapters/agy>"`

Pass `--replace` to upgrade, in the same step as the skill. Then validate and enable the plugin with `agy plugin validate <that path>` and `agy plugin enable conductor`. Conductor does not write Antigravity's plugin registry.

The live session gates have not been run on this mechanism. Unobserved so far: the `Stop` hook's ancestor chain on the CLI and on Desktop; whether that target lives exactly as long as the conversation, and whether closing a conversation or interrupting it kills the waiting `Stop` hook; that a detached holder survives the hook that spawned it; the payload's `conversationId` and `terminationReason` keys and the `NO_TOOL_CALL` value; the size limit on `reason`; and whether commands the model runs inherit `ANTIGRAVITY_CONVERSATION_ID`.
