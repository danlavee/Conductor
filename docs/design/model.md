# The model

The invariants. Everything else in this repository derives from them, and a change of model is a change here first.

Conductor is a publish-subscribe bus. A publication goes to a topic and accumulates for its subscribers; nothing is addressed to an agent.

Registering a name is durable and outlives every session. Publications to a registered name accumulate whether or not anything is listening.

Holding a stream is not durable, and is not a state anything maintains. It is an **act**, performed whenever a session and an identity are first paired — at session start, at restart, and at the moment a session joins as an identity it did not have. It lasts exactly as long as that session. It is never inherited, never assumed, and never outlives the process that declared it: a stream that survived its session would go on consuming deliveries and acknowledging them, putting work beyond redelivery. Scoping the act to the session makes that unrepresentable rather than guarded against.

**The promise: while a stream is held for an identity, a delivery reaches a turn.** An adapter makes that promise, by whatever means its host offers. An adapter that can no longer keep it releases the stream rather than holding one it cannot honour.

The core knows a stream is held. It never learns what holds it or what a stream would wake — a bus with no addressee has no vocabulary for either, and no reader for them.

There are no classes of host. The mechanism varies; the promise does not. A host that cannot keep a process alive between turns needs a waker outside the session, not a category of its own. Whether a host's adapter has landed yet is a difference in tense, not a class: it says how far the work has got, never what kind of host that is.

Where an adapter holds an agent's identity, the agent's obligations end at joining. Where none has landed, owning the stream is the explicit exception, and the only case in which reachability is the agent's business at all.

## Where the rest lives

| | |
| --- | --- |
| [wake-adapters.md](wake-adapters.md) | The requirements this model imposes, and what each host's adapter was found to need |
| [../host-adapters.md](../host-adapters.md) | The contract, for someone building one |
| [../integrations/](../integrations/) | Per-host mechanism, measured limits, and observed wakes |
| [the skill](../../skills/conductor/SKILL.md) | What a joined agent must know — only what is true on every host |
| an adapter's own announcement | Everything host-specific, said by the party that knows it |

The last two are what keeps slipping, in two directions. Test a sentence in the skill by asking whether it could be false on some host; if it could, it belongs in the announcement or an integration page. Naming a host's identity file, command spelling, or binding precedence is the recurring form of saying too much. Stating the promise without its antecedent — writing "your adapter delivers" where only "where your host has an adapter, it delivers" is true — is the recurring form of the same mistake wearing general words, and it is the harder one to see.
