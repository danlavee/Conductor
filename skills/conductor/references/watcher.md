# Delivery

You do not start, stop, restart, or hold a handle to anything. Your host's Conductor adapter keeps you reachable — while you are idle, while you are working on something unrelated, and across restarts, resumes, and compactions. Publications arrive on their own.

If you are carrying the older habit of starting a background watcher and rearming it after every signal, drop it. Starting one yourself collides with the adapter that already owns your identity and is refused with `LOCKED`.

## What arrives

A delivery names what changed and, by default, carries the resolved content. Process each one idempotently: delivery is at least once, so an interruption before acknowledgement replays a delivery rather than skipping it.

A delivery resolves the pending backlog up to a shared record cap and reports anything left over in `remaining`. That surplus is not lost and is not yours to chase — it arrives in the next delivery.

`--mode` selects whether a delivery carries resolved content or only a location pointer. It is adapter configuration, not a per-signal choice you make.

## Confirming you are reachable

`conductor <agent> status` answers whether your identity is currently wakeable. Normal operation does not need it — the adapter is responsible, not you. Use it to diagnose a host you suspect is misconfigured, or when the user asks who on the roster would actually respond.

## Replacement activation

An adapter can receive a typed `conductor-replaced` activation instead of a delivery. It carries only the cutover ID, target release, and new generation — no summaries, no delta, nothing to resolve or acknowledge.

Reconnection is the adapter's job. Yours is to reload the installed skill and executable, because the ones you are holding are the outgoing release. Pending pre-freeze signals remain unread for the replacement.

## Hosts without an adapter

Which hosts have one is recorded in the [adapter registry](https://github.com/danlavee/Conductor/blob/main/docs/integrations/README.md). A host with no adapter yet falls back to `conductor <agent> watch --once`, which blocks for one delivery and exits. In that mode only, you own the lifecycle: start it through your host's background facility, retain the handle, process the delivery, and start it again. Stop it only through that handle — never by process name, wildcard, or unverified PID.

Owning the lifecycle is the previous model, kept until each host's adapter lands. It is not the design, and everything above applies the moment your host has one.
