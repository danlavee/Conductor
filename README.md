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

1. Each agent registers an identity and responsibility.
2. Each agent starts a background watcher to listen for publications.
3. The user establishes collaboration rules.
4. Every registered agent wakes and reads them.
5. An agent publishes a decision, question, finding, handoff, or result.
6. Every registered agent wakes and decides whether to act.

Only the result travels. Conversations and working context stay with the agent.

For details on how different AI harnesses wake and watch for publications, see the [watcher documentation](skills/conductor/references/watcher.md).

## Easy Onboarding

- **Install the skill:** Follow the [installation instructions](docs/installation.md) to load the Conductor skill.
- **Load base rules:** Run the `onboarding` mode of the [Conductor skill](skills/conductor/references/onboarding.md) to register your identity and load the recommended base collaboration rules.

## Configuration

The agent name is the one identity ever passed explicitly, as the leading argument to every command. Generic one-shot watchers need no conversation or thread identifier. The optional Claude CLI resume adapter reads `CLAUDE_SESSION_ID`, set automatically by its harness.

One environment variable remains genuinely optional configuration:

- `CONDUCTOR_HOME` — the state root directory, when a team uses one other than the default.

The optional `claude` adapter binary is found automatically — on `PATH` first, then at known install locations — with a clear error if neither resolves.

## Use Conductor

**Agents:** follow the self-contained [Conductor skill](skills/conductor/SKILL.md).

The skill is used in every environment. Harness integration changes only who waits for signals: the agent or its environment.

## Guarantees

- Publications become visible atomically.
- Every registered agent is signaled, including the publisher.
- A crash can replay delivered work; it does not silently skip it.
- Nothing remains running between commands.

## Limits

Conductor targets trusted, same-user agents on one Windows or Linux host. Delivery is at least once around crashes. History is not compacted automatically, and abandoned-transaction recovery occurs on the next operation.

Initial release: `v0.1.0`.

## License

[Apache License 2.0](LICENSE).

[Architecture](docs/design/architecture.md) · [State model](docs/design/state-model.md) · [Runtime boundaries](docs/design/runtime-boundaries.md)

[Protocol](skills/conductor/references/protocol.md) · [Suggested harness wake](docs/integration.md) · [Current limitations](skills/conductor/references/limitations.md) · [Activity diagrams](docs/use-case.md) · [Use cases](docs/use-cases/README.md)
