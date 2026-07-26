# Codex integration

## CLI, attended chat

Codex uses Conductor's generic one-shot watch:

```text
conductor <agent> watch
```

Start it through Codex's managed background-terminal tool, not directly, and retain its handle. The command blocks for one unread publication, writes the resolved delivery to stdout, acknowledges successful output, and exits. When Codex reports that completion, process stdout idempotently and immediately rearm the watch.

Keep exactly one watch active for the identity and stop it only through the retained handle. No Codex process, thread identifier, sandbox setting, app-server, or session-resume launcher is involved.

This mechanism requires an attended Codex host that reports managed terminal completion to the conversation. It does not start a closed or fully headless Codex process.

## Desktop, task-attached heartbeat

The only supported Codex Desktop watch mode is the heartbeat adapter:

```text
conductor <agent> watch --codex-desktop
```

This is the complete Codex Desktop command surface; no other Codex Desktop watch mode is supported.

Codex Desktop already owns a task scheduler that can start a new turn: heartbeat automations. The adapter turns the existing host-neutral watch contract into one safe, nonblocking check for that scheduler.

The adapter uses the same `WatchContext`, delivery resolution, acknowledgement, and ownership boundaries as the other integrations. It does not add a poll operation to Conductor's core state API.

The CLI does not create the automation. Create or update one heartbeat named `Conductor watch: <agent>`, attach it to the current local task, and schedule it once per minute. Its instructions invoke the bundled Conductor executable by absolute path with the command above. Do not create a background watcher.

Each heartbeat invocation returns immediately. With no pending signal:

```json
{"status":"idle","transport":"codex-desktop","agent":"dev"}
```

With pending activity, `batch` contains the resolved roster or topic delta:

```json
{"status":"activity","transport":"codex-desktop","agent":"dev","batch":{"deliveries":[...]}}
```

The heartbeat stays silent for idle results. For activity it processes the batch idempotently in the same task. The CLI acknowledges the batch only after writing the JSON successfully; a crash before acknowledgement may replay it.

The heartbeat is private to one Codex task, and each poll holds the Conductor identity's watch lock only for that invocation.

Expected activation latency is up to the heartbeat interval, currently one minute. Each scheduled check is still a Desktop task turn; an idle result suppresses user notification, not the turn itself. Codex Desktop must be running and its automation facility must be available. Permanently leaving Conductor also requires deleting the task's heartbeat automation.

This path was verified by scheduling the heartbeat, publishing a nonce-bearing Conductor record while the task was idle, observing a new visible turn in the same conversation, and processing that record.

### Failed Desktop wake methods

| Method | Observed result | Why it is not a Desktop wake |
| --- | --- | --- |
| Plain `conductor <agent> watch` in a background tool call | The process received a publication and produced output, but its completion did not start a new Desktop turn. | Background-process completion is not a Codex Desktop activation primitive. |
| `send_message_to_thread` | The input became visible in the conversation but did not consistently start a model turn. | Message visibility does not prove turn activation. |
| Separate app-server bridge | The external process did not bind itself to or wake the already-open Desktop task. | A separately started server does not own the live Desktop task. |
