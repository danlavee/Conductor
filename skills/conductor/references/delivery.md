# Delivery

Being wakeable is an act performed once per session, never a state left running from the last one. `SKILL.md` question 2 owns that rule and the commands for it. This file covers what a delivery contains, and the cases the main flow does not reach.

## What arrives

A delivery names what changed and, by default, carries the resolved content, up to a shared record cap, reporting anything left over in `remaining`. That surplus is not lost and is not yours to chase — it arrives in the next delivery.

A summary identifies a publication without carrying it. Content delivery resolves the publication behind the summary before the recipient is woken, so a delivery you receive is already readable; you are not being handed a pointer to go fetch.

A delta that resolves to nothing omits its `publications` key rather than carrying an empty list — a signal whose content you had already read out of band arrives this way, once, and is then drained. Treat a missing key as no work to do, not as a malformed delivery.

## Which host you are on

You do not have to work this out. `conductor <agent> status` reports what holds your stream, what serves this session, and what reaches you; `SKILL.md` question 2 carries what each answer means and the command for it. Nothing overrides that answer — not which host you appear to be on, not an announcement you do or do not recall seeing, not an unreachable [adapter registry](https://github.com/danlavee/Conductor/blob/main/docs/integrations/README.md) URL.

An adapter does announce itself into the session it serves, and that announcement is worth reading for the one thing `status` cannot tell you: what to do when the identity it bound is wrong. That differs from host to host, while this skill stays the same on all of them.

If you arrived with the habit of starting a background watcher and rearming it after every signal, what decides whether it applies is whether anything holds your stream — never which host you are on. Anything already holding it refuses a second one with `LOCKED`, whether that is an adapter or a watcher some other session armed. Where nothing holds it, the habit is the mechanism: a watcher lasts one delivery, and re-arming after each is how the identity stays reachable. `SKILL.md` question 2 carries which case you are in.

## Replacement activation

An adapter can receive a typed `conductor-replaced` activation instead of a delivery. It carries only the cutover ID, target release, and new generation — no summaries, no delta, nothing to resolve or acknowledge.

Reconnection is the adapter's job. Yours is to reload the installed skill and executable, because the ones you are holding are the outgoing release. Pending pre-freeze signals remain unread for the replacement.
