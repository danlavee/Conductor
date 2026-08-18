package conductor

import "github.com/danlavee/Conductor/internal/state"

// Client is the public Conductor SDK client.
type Client = state.Client

const CurrentProtocolVersion = state.CurrentProtocolVersion

// New opens a Conductor state root. An empty home uses ~/.conductor.
func New(home, agent string) (*Client, error) {
	return state.New(home, agent)
}

// Open constructs a client without reading or initializing the state root.
// It is intended for watch processes that may need to wait through cutover.
func Open(home, agent string) (*Client, error) {
	return state.Open(home, agent)
}
