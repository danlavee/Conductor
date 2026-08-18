# Release process

`go install github.com/danlavee/Conductor/cmd/conductor@latest` — the only source most ordinary users have — resolves to the last tagged release. Nothing tagging a new one means they never see what's since landed, no matter how current the repository itself is.

Whoever has push access should periodically check how far the stable line has drifted past the last tag. Meaningfully ahead — cut a new one. Never tag or push without confirming with the project owner first; this changes what every ordinary user's `@latest` resolves to the moment it happens.
