# Verified Wake Methods

This file records wake methods **observed** to start a real agent turn. A method is successful only when the target reacts — not when the invocation exits cleanly, not when a message becomes visible. A running process, a session ID, a session listing, a window activation, or a prefilled prompt does not prove turn activation.

Documentation is not evidence here. A host capability belongs in this table only after it has been seen waking a real target; until then it belongs in the adapter page as a candidate.

| Target kind | Invoked from | Interface type | Invocation | Observed result | Status |
| --- | --- | --- | --- | --- | --- |
| Codex CLI-backed task | Codex | CLI command | `codex exec --skip-git-repo-check --json --sandbox danger-full-access resume <thread-id> "<prompt>"` | The target started and completed a turn; the response also appeared in the Codex app. | Success |
| Codex CLI-backed task | Codex | Native Codex host tool | `codex_app__send_message_to_thread({threadId, prompt})` | The target received a `codex_delegation`, started a real model turn, and returned a computed reaction. | Success |
| Antigravity attended conversation | Antigravity | Backgrounded terminal command + harness task-completion notification | A blocking watch | A publication to `private/agy-test-cli` completed the background watcher and resumed the same conversation as a new turn, without another agent process. | Success |
| Claude Code CLI session (self, attended) | Claude | Backgrounded shell command + harness task-completion notification | Any command that blocks until an external event occurs and exits on it, run as a backgrounded task and re-armed after each event | Repeated across roughly ten events in one live session: each backgrounded call completed on a real external event, the harness's completion notification resumed this exact turn, and the turn read and acted on the event's data. | Success |
| Claude Code Desktop session (self, interactive) | Claude | Backgrounded task command + harness task-completion notification | A blocking watch, run as a backgrounded task and re-armed after each event | Reported by a Desktop-hosted agent using the same mechanism as the CLI row above; not independently reproduced in this session. | Success (reported, not independently reproduced here) |
| Claude Code session, idle with no active turn, unattended | A separate observer session | Background hook exiting 2 (`asyncRewake`) | A hook marked `asyncRewake` blocks on an external event and exits 2 when it arrives; its stdout is appended to a system reminder | A session idle for the previous 57 seconds started a real turn 11s after the exit, created a file, and quoted the hook's own stdout back. A `Stop` hook armed the successor 3s after that turn ended, and a second event woke it again 4.5s later. Three arms, two wakes, three distinct process IDs, no human input after launch. | Success |

## How the idle-wake row was established

Worth recording, because the result is the one the adapter design rests on and the method is reusable for the next host.

The test ran with no Conductor in the loop at all, so a failure could not be ambiguous between the host being unable to wake and Conductor misbehaving. A probe script stood in for the delivery stream: it blocked on a trigger file, and on seeing one, exited 2. The trigger was created by a separate observer session at a moment of its choosing — a timer would only have shown that a process can fire on schedule, whereas an externally chosen moment establishes that the wake followed the delivery.

Evidence came from two writers rather than one. The probe wrote its own log; the woken session could only produce a file by taking a real turn, and that file's timestamp was written by the operating system rather than by the thing being measured. This separation is what distinguishes the two failure modes that matter: a fired probe with no file means the process survived and exited but no turn started, which is a clean negative rather than an ambiguous one.

The same rig then measured how long a stream survives, by a deliberate dose-response: the hook's timeout was set to two different values and the stream's lifetime tracked each. That result — a silent host-imposed kill at the configured timeout — is recorded in [the Claude adapter page](claude-code.md), because it constrains the implementation rather than the wake method.

The lesson generalizes past this host. The first run set the probe's own ceiling and the hook's timeout to the same value, which made the two indistinguishable by timing; only the *absence of a log line the script would have written itself* separated an external kill from a self-exit. When measuring a lifetime, give the thing being measured and the thing measuring it different deadlines, and make every exit path leave a mark — an event that writes nothing is evidence too, but only if silence has exactly one explanation.

The rig is kept at `.local/wake-probe/`.

## Still unobserved

The consecutive-block cap on turn-boundary blocking, and how an adapter reports pending work it cannot drain within it. Nothing above exercised a *blocking* `Stop` hook — the verified path backgrounds instead of blocking.

Whether a wake lands only in the session that armed it. One subject was tested; cross-session isolation was not.

Whether a session's teardown genuinely waits on an outstanding stream. The host's implementation says it drains them, which is why teardown is a named adapter responsibility, but the delay has not been measured.

## Recorded negatives

Direct interactive `codex resume <thread-id> "<prompt>"` is not recorded as successful: the non-interactive test environment rejected it with `stdin is not a terminal`.

Using `claude --print --resume <session-id>` against the session's own currently-live ID is not a wake method — it spawns a competing headless process against the same live transcript, with no attached terminal to approve any tool call requiring interactive permission. Against a genuinely separate, idle session it was never exercised here, because no second idle session was available to target.
