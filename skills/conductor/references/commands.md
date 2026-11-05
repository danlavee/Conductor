# Commands

`SKILL.md` covers the everyday path. This is every other verb. A topic is `<group>/<topic>`; a record is `{"index":N,"text":"..."}`, cited as `<group>/<topic>/#<index>`. Publishing to a topic that does not exist creates it.

## Publish

```text
conductor <agent> put <group>/<topic> '<text>'
conductor <agent> put <group>/<topic> --file=<absolute-path>    # text containing ' or a newline
conductor <agent> put <group>/<topic> --jsonl=<absolute-path>   # one JSON string per line, one publication
conductor <agent> edit <group>/<topic> <index> '<text>'         # also takes --file; nothing is ever deleted
```

Each answers with the resulting `records` and the `recipients` it wakes. A file publishes verbatim, less one trailing newline.

## Read

```text
conductor <agent> get <group>/<topic> --full                         # current records; works unsubscribed
conductor <agent> get <group>/<topic> --delta [--limit=N]            # your unread, advancing your cursor
conductor <agent> get <group>/<topic> [<index>] [--start=N] [--end=N] [--limit=N]   # index range
```

A read returns at most 20 records unless `--limit` raises it; a non-zero `remaining` means continue with a range read from the next index. A `--delta` on a topic that does not reach you is always empty — confirm with `--full`.

## Subscribe and list

```text
conductor <agent> subscribe --topic=<group>/<topic>
conductor <agent> subscribe --topic-group=<group>     # every topic in the group, present and future
conductor <agent> list --topic-groups
conductor <agent> list --topic-group=<group>
```

`collaboration/rules`, `collaboration/agents`, `collaboration/broadcast`, and your own `private/<agent>` reach you without subscribing; nothing subscribes you to another agent's `private` topic.
