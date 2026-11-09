# Grok

The Grok adapter is a plugin at [`adapters/grok`](../../adapters/grok). It runs the same holder and watcher as the [Claude adapter](claude-code.md#holder-and-watcher); only the hooks and how a delivery enters a turn are Grok's. Every hook runs `conductor adapter grok <identity|arm|inject|release> --session=${GROK_SESSION_ID}` in exec form, and reads its event from stdin.

| Hook | Runs | Does |
| --- | --- | --- |
| `SessionStart` (`new`, `load`) | `identity` | Serves the session, so a `join` from inside it binds the session and answers `"wake": "adapter"`. |
| `Stop` | `arm` | The watcher. On a main-agent `end_turn` in a bound session it spawns the holder and waits for a delivery, then blocks the stop with the delivery as the next prompt and acknowledges what the block carried. |
| `PostToolUse` | `inject` | Inside a turn, where no watcher runs. In a bound session it hands over what is unread as the hook's `additionalContext`, then acknowledges what it showed. With nothing unread it writes nothing, Grok's no-op. |
| `SessionEnd` | `release` | Forgets the session's binding; the holder and a waiting watcher see it at their next poll and exit. |

Grok has no background hook, so a waiting `Stop` hook is the only way to start a turn in an idle session, and its timeout is set to a day. The stop's parent is the session process, so it is the holder's target. Subagent events, which carry `subagentType`, are ignored by `arm`, `inject` and `release`.

The block's reason is the plain-text wake, which fits the 10,000 characters Grok keeps of a reason; what the wake did not show stays unread for the next stop.

`inject` puts the same wake in `additionalContext`, which Grok also keeps to 10,000 characters and does not count against its 8 continuations. It waits at most two seconds for the session's lock and otherwise shows nothing. Grok drops the context of a hook that exits non-zero, so once the output is written `inject` exits 0 whatever follows; a failed acknowledgement only shows the same records again. Most tool results find nothing, so `inject` keeps the history sizes of its last empty read and skips the read while they are unchanged: a no-op costs about a process start (measured: 6.2 ms against 5.2 ms for `conductor version`, over 14,000 records of history in 23 topics).

Known limits:

- Grok forces a stop after 8 continuations without consulting the hook. The holder then holds with no watcher until the next turn ends. Nothing is lost: what was not shown stays unread.
- A block Grok consults and then ignores loses that one wake; the publication stays readable with `get`.
- If Grok kills a waiting `Stop` hook when the user types, the agent reads unreachable for that turn, and the turn's end arms the next watcher.

Install places this tree and its executable together:

`go run github.com/danlavee/Conductor/cmd/conductor@latest adapter grok install "<absolute path ending in adapters/grok>"`

Pass `--replace` to upgrade, in the same step as the skill. Enabling it and picking up each upgrade are the user's commands, in the [registry](README.md) row. Conductor does not write Grok's plugin registry or trust store.

On November 8 this port passed an isolated native gate at `bfa8211` against a scratch bus: an idle session woke at `end_turn`, `inject` delivered inside a turn, a burst arrived as 5 records then the rest, only what was shown was acknowledged, a second session took over, and a subagent was never shown a wake. Unobserved so far: the detached holder outliving the `Stop` hook (that wake arrived while `arm` still ran), whether the `Stop` hook's parent is the session process, and what the day-long `Stop` timeout does.
