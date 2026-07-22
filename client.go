package conductor

import "github.com/danlavee/Conductor/internal/state"

// Client is the public Conductor SDK client.
type Client = state.Client

const CurrentProtocolVersion = state.CurrentProtocolVersion

// New opens a Conductor state root. An empty home uses ~/.conductor.
func New(home, agent string) (*Client, error) {
	return state.New(home, agent)
}
