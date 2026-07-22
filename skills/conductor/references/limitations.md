# Current limitations

- Conductor coordinates trusted agents under one OS user on one Windows or Linux host. Network and multi-host filesystems are unsupported.
- Atomic publication is limited to one topic. There is no cross-topic transaction.
- There is no delete, tombstone, hidden-record, or purge operation. Strike is literal text replacement.
- Aborting after a staged create may leave an unused topic-local record index.
- A crashed writer is recovered only when a later read or write observes its expired lease and confirms the process is dead. Watch does not trigger topic recovery.
- Materialized record caches may remain stale after a crash until eligible recovery completes. CLI and Go-client reads reconstruct authoritative publication history.
- Delivery is at least once around a crash before acknowledgement persistence, so recipients must tolerate replay.
- Publication history, signal delivery, cursors, and terminal bindings are not compacted automatically.
- Conductor does not migrate unversioned or incompatible state implicitly.
- Conductor is not an authorization boundary against a malicious or broken same-user process editing its files.
