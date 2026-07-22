# Antigravity watchers

Antigravity has two separate loops. Do not assume a Desktop conversation and an AGY CLI conversation are the same live session.

## Active Interactive Sessions (CLI/TUI, Desktop, or IDE)

For any **active, interactive session** (where you are currently chatting in the shell, TUI, or Desktop and want incoming signals to "wake up" the live chat), use the native message delivery watcher:

```text
conductor watch --agy <name>
```

This watcher utilizes `agentapi send-message` to deliver signals directly as new turns into your active conversation without attempting to start separate background CLI processes (which would fail due to conversation directory locks).

* **Auto-Discovery/Fallback**: Set `CONDUCTOR_AGY_CONVERSATION_ID` to your target conversation ID. If this variable is empty, the watcher automatically falls back to your active CLI/TUI session ID in `ANTIGRAVITY_CONVERSATION_ID`.
* **Execution**: Run this command from an enabled Antigravity environment where `agentapi` is available on your `PATH`.

## Idle Persisted CLI Sessions

For **idle, background, or persisted CLI conversations** where no live shell is currently active in that conversation, use:

```text
conductor watch --agy-cli <name>
```

* This starts a new, non-interactive `agy --print --conversation` process for each signal. 
* It acknowledges only after a successful exit with a non-empty response. A failed or timed-out delivery remains unread. 
* **Warning**: Never run `--agy-cli` on a conversation that is currently active/locked in your live terminal, as the background process will fail to acquire the conversation directory lock. Use `--agy` instead.
* When `CONDUCTOR_AGY_DELIVERY=1`, process the supplied signal with this skill; do not register again or start another watcher.

AGY CLI has no noninteractive list command. Use its interactive `/resume` picker to select a persisted CLI conversation. CLI import clones Desktop history, so this transport is not a bridge into an existing Desktop or IDE session.
