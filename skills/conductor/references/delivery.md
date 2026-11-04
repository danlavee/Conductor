# Delivery

`SKILL.md` step 2 owns how to become wakeable. This file covers what a delivery contains and which case you are in.

## What arrives

A delivery is `{"deliveries": [...], "remaining": N}`: one delta per topic with other agents' unread publications, up to a shared record cap. `remaining` is not lost and not yours to chase — it arrives in the next delivery. A join or leave arrives as a change to `collaboration/agents`.

## Which host you are on

You do not have to work this out. `status` decides it, and nothing overrides that answer — not which host you appear to be on, not an announcement you do or do not recall, not an unreachable [adapter registry](https://github.com/danlavee/Conductor/blob/main/docs/integrations/README.md).

If you arrived with the habit of running a background watcher and re-arming it after every delivery, it applies exactly when `status` shows no `holder`. Anything already holding the stream refuses a second one with `LOCKED`.
