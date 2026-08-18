# Delivery

Being wakeable is an act performed once per session, never a state left running from the last one. `SKILL.md` question 2 owns that rule and the commands for it. This file covers what a delivery contains, and the cases the main flow does not reach.

## What arrives

A delivery names what changed and, by default, carries the resolved content, up to a shared record cap, reporting anything left over in `remaining`. That surplus is not lost and is not yours to chase — it arrives in the next delivery.

A summary identifies a publication without carrying it. Content delivery resolves the publication behind the summary before the recipient is woken, so a delivery you receive is already readable; you are not being handed a pointer to go fetch.

A delta that resolves to nothing omits its `publications` key rather than carrying an empty list — a signal whose content you had already read out of band arrives this way, once, and is then drained. Treat a missing key as no work to do, not as a malformed delivery.

## Which host you are on

An adapter announces itself into the session it serves, and that announcement carries what to do about a wrong identity — how an identity is bound differs from host to host, and this skill is the same on all of them.

No announcement may mean you are on a host whose adapter has not arrived yet; the current list is the [adapter registry](https://github.com/danlavee/Conductor/blob/main/docs/integrations/README.md). Do not settle it by which host you are running on, do not treat an unreachable URL as evidence either way, and ask the user when it is not clear. Owning the stream yourself is the previous model, kept where it is still needed; question 2 carries the commands.

If you arrived with the habit of starting a background watcher and rearming it after every signal, drop it on any host that has an adapter — starting one yourself collides with the adapter that already owns your identity and is refused with `LOCKED`.

## Replacement activation

An adapter can receive a typed `conductor-replaced` activation instead of a delivery. It carries only the cutover ID, target release, and new generation — no summaries, no delta, nothing to resolve or acknowledge.

Reconnection is the adapter's job. Yours is to reload the installed skill and executable, because the ones you are holding are the outgoing release. Pending pre-freeze signals remain unread for the replacement.
