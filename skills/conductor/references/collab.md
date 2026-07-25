# Collaboration basics

## Topics and records

A topic is a `<topic-group>/<topic>` address such as `collaboration/rules`. Topic groups and topics carry the team's semantics.

A topic contains records. Every record has exactly a positive numeric `index` and plain string `text`. The index is assigned by Conductor, is local to that topic, and remains the record's key for its lifetime. Two topics may each contain record index 1.

Create allocates a new index. Edit replaces the entire text at an existing index. Strike replaces that text with `~~` plus the current text plus `~~`. Strike is literal, not hidden state: the record remains present, and repeated strikes add marker pairs.

One publication may carry several resulting records from one topic transaction. There is no cross-topic transaction.

## Registry and identity

An agent is a registered name and declared responsibility. Joining adds it to the roster and publishes a join signal. Re-joining is done by omitting the responsibility argument (which automatically reloads it). To change responsibility, leave and join again under the same name.

Registration is disk-persisted and outlives the process that created it: a crash, restart, or brand-new session for the same identity finds it still on the roster. Only the watcher — not the join — needs restarting after any of those; see [the watcher](watcher.md).

Leaving removes the roster entry and publishes a leave signal. It is refused while the agent has an open transaction. Prior publications are unaffected.

Identifiers are lowercase, at most 64 characters, begin and end with a letter or digit, may contain dots, underscores, and hyphens, and exclude reserved Windows device names. Responsibility is nonempty text.

## Reading

- **Delta** returns publications after the reader's acknowledged cursor.
- **Range** returns current records in an inclusive record-index range without moving a cursor.
- **Full** reconstructs current records without moving a cursor.

A numeric record filter has its own delta cursor. Publication sequences order publications; they are not record indexes. A record in any view still contains only its stable index and text.

A summary signal is a location hint, not a record. Content delivery resolves publications through the summary's sequence before waking the recipient.
