# Current limits

- Conductor coordinates trusted agents under one OS user on one Windows or Linux host. Agents must share a local filesystem with atomic create, replace, and file-lock behavior. Network and multi-host filesystems are unsupported.
- Every registered agent receives every signal. Conductor has no per-topic subscription or private routing.
- Message kind is an unrestricted string. Conductor does not validate a vocabulary, interpret kinds, or enforce collaboration rules.
- Message payload contains only `text`; additional payload fields and arbitrary structured payloads are invalid.
- Atomic publication is limited to one resource. There is no cross-resource transaction.
- Scratching removes a message from current state but does not physically erase immutable history. There is no purge operation.
- A crashed writer is recovered only when a later read or write observes its expired lease and confirms that its process instance is dead. `watch` does not trigger recovery.
- Materialized message caches may remain stale after a crash until eligible recovery completes. Read through the CLI or Go client, which reconstructs publications from history.
- Delivery is at least once around a crash before acknowledgement persistence, so agents must tolerate replay.
- Publication history, signal delivery, cursors, and terminal bindings are not compacted automatically.
- The CLI has no standalone command that lists every resource; registration discovers current resources.
- Conductor does not migrate unversioned state or incompatible protocol versions implicitly.
- Conductor is not an authorization boundary against a malicious or broken same-user process that edits its files directly.
