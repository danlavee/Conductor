# Conductor

## Conductor — talk to each AI agent individually while they work as a team

![A developer working with multiple individual AI agents while a single ephemeral Conductor pulls the shared web of light between their screens](docs/assets/conductor-team-hero.png)

Each agent keeps its own conversation, context, and domain memory. They share what matters. When one publishes something the team needs, Conductor wakes the others.

No lead agent. No board. No broker. No server.

Everything is on disk. When no agent is active, neither is Conductor.

## One task. Three contexts.

![A developer collaborating across Architecture and Skills while the ephemeral Conductor delivers each handoff to Development](docs/assets/conductor-three-contexts-comic.png)

Three separate chats: Architecture, Development, and Skills. The user tells Architecture to start Development but hold the commit. Architecture publishes the handoff; the team wakes, and Development writes, then waits. The user tells Skills to finish the dependency. Skills publishes that it is ready; the team wakes again, and Development applies it and commits. Only the handoffs cross. Each agent keeps its own conversation and memories.

## How it works

1. Each agent registers an identity and responsibility. That is the last thing it does to stay connected.
2. Its host's Conductor adapter keeps it reachable from then on, and restores itself after a restart, resume, or compaction.
3. The user establishes collaboration rules.
4. Every registered agent wakes and reads them.
5. An agent publishes a decision, question, finding, handoff, or result.
6. Every registered agent wakes and decides whether to act.

Only the result travels. Conversations and working context stay with the agent.

For how each host turns a publication into an agent turn, see the [adapter registry](docs/integrations/README.md).

## Easy Onboarding

- **Install the skill:** Follow the [installation instructions](docs/installation.md) to load the Conductor skill.
- **Load base rules:** Run the `onboarding` mode of the [Conductor skill](skills/conductor/references/onboarding.md) to register your identity and load the recommended base collaboration rules.

## Updating

Before updating, check [the update notes](updates.md) for release-specific out-of-band steps, then follow the normal update procedure in the Conductor skill.

## Configuration

The agent name is the one identity ever passed explicitly, as the leading argument to every command. An adapter resolves it from configuration the session carries, since it acts without the agent's participation.

One environment variable remains genuinely optional configuration:

- `CONDUCTOR_HOME` — the state root directory, when a team uses one other than the default.

## Use Conductor

**Agents:** follow the self-contained [Conductor skill](skills/conductor/SKILL.md).

The skill is used in every environment. The host adapter changes only how a publication becomes an agent turn.

## Guarantees

- Publications become visible atomically.
- Every registered agent is signaled, including the publisher.
- A crash can replay delivered work; it does not silently skip it.
- Nothing remains running between commands beyond an adapter's own delivery stream.
- The roster reports which agents are currently wakeable, not merely registered.

## Limits

Conductor targets trusted, same-user agents on one Windows or Linux host. Delivery is at least once around crashes. History is not compacted automatically, and abandoned-transaction recovery occurs on the next operation.

Initial release: `v0.1.0`. Latest release: `v0.4.0`.

## License

[Apache License 2.0](LICENSE).

[Architecture](docs/design/architecture.md) · [State model](docs/design/state-model.md) · [Runtime boundaries](docs/design/runtime-boundaries.md)

[Protocol](skills/conductor/references/protocol.md) · [Wake adapters](docs/design/wake-adapters.md) · [Host adapters](docs/host-adapters.md) · [Current limitations](skills/conductor/references/limitations.md) · [Activity diagrams](docs/activity-diagrams.md) · [Use cases](docs/use-cases/README.md)
