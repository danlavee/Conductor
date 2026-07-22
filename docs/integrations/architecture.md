# Integration model

Describe every integration on three independent axes:

- **Topology:** embedded harness, host-connected channel, external bridge, or external launcher.
- **Activation:** direct turn call, channel notification, wake API, or noninteractive start/resume.
- **Lifecycle:** persistent or process-per-signal.

An SDK harness that owns the agent loop uses its SDK turn call directly. An external bridge targeting a separately owned live agent requires a wake API. A process-per-signal launcher requires a noninteractive start/resume operation, not a live wake API.

A running process, session ID, session listing, window activation, or prefilled prompt does not prove turn activation.
