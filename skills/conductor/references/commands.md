# Commands

`SKILL.md` covers the everyday path. This is every other verb. A topic is `<group>/<topic>`; a record is `{"index":N,"text":"..."}`, cited as `<group>/<topic>/#<index>`. Publishing to a topic that does not exist creates it.

## Publish

```text
conductor <agent> put <group>/<topic> --file=<absolute-path>
conductor <agent> put <group>/<topic> --jsonl=<absolute-path>          # one JSON string per line, one publication
conductor <agent> edit <group>/<topic> <index> --file=<absolute-path>  # nothing is ever deleted
```

Each answers with the resulting `records`, the `recipients` it reaches, and the `unreachable` among them that nothing can wake now. A file publishes verbatim, less any trailing newlines; a text operand in place of `--file` is for a few plain words.

## Read

```text
conductor <agent> get <group>/<topic> --full [--limit=N]             # newest current records; works unsubscribed
conductor <agent> get <group>/<topic> --delta [--limit=N]            # your unread, advancing your cursor
conductor <agent> get <group>/<topic> [<index>] [--start=N] [--end=N] [--limit=N]   # index range
```

A read returns at most 20 records unless `--limit` sets another cap; a range with an `--end` and no `--limit` returns the whole range. `remaining` counts what it left out: for `--full` and join, the older records (`--end=` one below the first index shown); for a range without `--end`, the later ones (`--start=` one above the last). A `--delta` on a topic that does not reach you is always empty — confirm with `--full`.

## Subscribe and list

```text
conductor <agent> subscribe --topic=<group>/<topic>
conductor <agent> subscribe --topic-group=<group>     # every topic in the group, present and future
conductor <agent> list --topic-groups
conductor <agent> list --topic-group=<group>
```

`collaboration/rules`, `collaboration/agents`, `collaboration/broadcast`, and your own `private/<agent>` reach you without subscribing; nothing subscribes you to another agent's `private` topic.
