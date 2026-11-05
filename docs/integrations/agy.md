# Antigravity

The Antigravity adapter is a plugin at [`adapters/agy/`](../../adapters/agy/README.md). Antigravity's hook surface provides `SessionStart` and `Stop` (with no `SessionEnd` hook or background `asyncRewake` hook). Two processes do that work.

`reside`, started from `SessionStart` (and from `bind`), holds `AcquireWatchOwnership` until release. `status` reports `holder` `"adapter"` while that process is alive, which is what keeps the skill from arming `watch --once`. `reside` writes a presentation and does not acknowledge it.

`stop` is a foreground `Stop` hook. While the session should stay inside the hook it does not return, so an idle session is a session blocked on that hook. A delivery returns `decision: "continue"` with a short reason naming the presentation file and the receipt token. The next `Stop` acknowledges when the assistant's final nonblank line in `transcript.jsonl` (the latest `PLANNER_RESPONSE` step) is that token. A missing token is presented again. After a continuation cap or hook timeout, the mail stays unacknowledged until the next turn end.

`conductor adapter agy release --session=<id>` sets a residency release flag and returns. `reside` polls it (alongside host process liveness when `CONDUCTOR_AGY_HOST_PID` is set) and exits without acknowledging. Nothing is acknowledged on that path.

Install places this tree and its executable together:

`go run github.com/danlavee/Conductor/cmd/conductor@latest adapter agy install "<absolute path ending in adapters/agy>"`

Pass `--replace` to upgrade, in the same step as the skill, so an old binary is not left holding the stream. Then enable the plugin with `agy plugin enable conductor` and write one agent name into `.conductor-agent`. A second session in the same directory uses `conductor adapter agy bind <agent>`.

The hook contract, detached `reside` stream holder, and `transcript.jsonl` (`PLANNER_RESPONSE`) receipt acknowledgment round-trip have been verified on the Antigravity harness alongside unit tests covering the receipt, binding, contention/takeover, and stop state machine.
