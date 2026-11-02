# Current limitations

- Conductor coordinates trusted agents under one OS user on one Windows or Linux host. Network and multi-host filesystems are unsupported.
- Atomic publication is limited to one topic. There is no cross-topic transaction.
- There is no delete, tombstone, hidden-record, or purge operation. History only grows; a record changes only by `edit`.
- Delivery is at least once around a crash before acknowledgement persistence, so recipients must tolerate replay.
- Publication history and cursors are not compacted automatically.
- Conductor does not migrate state. A protocol change starts a fresh state root.
- Conductor is not an authorization boundary against a malicious or broken same-user process editing its files.
