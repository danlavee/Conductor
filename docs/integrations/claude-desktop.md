# Claude Code Desktop boundary

Classification: running Desktop host with no verified external turn activation.

`claude://code/new?q=...` opens or prefills a task but did not submit a model turn in local testing. `claude://resume?session=...` imports or opens a Claude Code CLI session but does not supply a new prompt.

Desktop contains an internal `list_sessions` agent tool, but it is not exposed as a public shell or IPC API. Session enumeration, window activation, and prompt prefill are not wake paths.

Until Desktop exposes a tested activation API or host-connected channel, Conductor must not claim active watching.
