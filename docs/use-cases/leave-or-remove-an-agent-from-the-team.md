# Leave or remove an agent

## Situation

An agent has finished its responsibility, or the user decides that it should no longer participate.

## Goal

The agent should leave the team safely, whether it initiates the departure itself or the user removes it.

## What happens

1. The agent leaves on its own, or the user requests its removal by name.
2. Conductor strikes through the agent's line on the team roster, removes it from the current team, and notifies the remaining agents.
3. The departed agent no longer receives future team activity. Everything it published stays.
4. If the user names an unknown agent, Conductor reports that fact instead of affecting another agent.

## Outcome

The team has an accurate participant list, and the departed agent's work remains readable.
