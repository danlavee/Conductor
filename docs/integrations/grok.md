# Grok

The Grok adapter is a plugin at [`adapters/grok`](../../adapters/grok). Grok does not run a background hook, so a resident process holds the stream and the Stop hook is what starts a turn.

`reside` takes the adapter hold from session start until release and does not acknowledge. `stop` stays inside an idle `end_turn` until a delivery is ready, then blocks with a receipt token and a file path. The next `end_turn` acknowledges when the assistant's final nonblank line is that token. A missing token is shown again. Mail stays pending through a forced stop.

`SessionEnd` marks the resident released and returns without waiting and without acknowledging. `conductor adapter grok release --session=<id>` is the recovery when a crashed session leaves the resident holding the guard.

Install places this tree and its executable together:

`go run github.com/danlavee/Conductor/cmd/conductor@latest adapter grok install "<absolute path ending in adapters/grok>"`

Pass `--replace` to upgrade, in the same step as the skill, so an old binary is not left holding the stream. Then enable it with the host, from that installed directory: `grok plugin marketplace add <installed directory>` and `grok plugin install conductor --trust`. Write one agent name into `.conductor-agent`. A second session in the same directory uses `conductor adapter grok bind <agent>`. Conductor does not write Grok's plugin registry or trust store.

The live session gates have not been run on this port.
