# Leave or remove an agent

## Situation

An agent has finished its responsibility, or the user decides that it should no longer participate.

## Goal

The agent should leave the team safely, whether it initiates the departure itself or the user removes it.

## What happens

1. The agent leaves on its own, or the user requests its removal by name.
2. If the agent has a coordinated change open, Conductor refuses the departure until that change is published or cancelled.
3. Conductor removes the agent from the current team and notifies the remaining agents.
4. The departed agent no longer receives future team activity.
5. If the user names an unknown agent, Conductor reports that fact instead of affecting another agent.

## Outcome

The team has an accurate participant list, and an agent cannot leave unfinished shared work behind during a normal departure.
