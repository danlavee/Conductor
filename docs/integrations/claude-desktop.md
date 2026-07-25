# Claude Code Desktop boundary

Classification: no headless mechanism.

Desktop's interactive-session case (a live agent already running in it) is not a separate boundary — it shares the same mechanism as Claude Code CLI's interactive-session case, see [Claude Code CLI or Desktop, interactive session](claude-interactive.md).

This page covers only what remains: Desktop has no headless mechanism. Claude Code CLI can resume a separate session per signal while headless (`claude --print --resume`, a fresh process spawned per signal that exits when the turn completes; see [Claude Code CLI](claude-cli.md)). Desktop has no equivalent — there is no way to start a new Desktop turn without an already-open interactive session.

`claude://code/new?q=...` opens or prefills a task but did not submit a model turn in local testing. `claude://resume?session=...` imports or opens a Claude Code CLI session but does not supply a new prompt.

Desktop contains an internal `list_sessions` agent tool, but it is not exposed as a public shell or IPC API. Session enumeration, window activation, and prompt prefill are not wake paths.

Until Desktop exposes a tested activation API, a host-connected channel, or a headless mechanism, Conductor must not claim active watching outside an interactive session.
