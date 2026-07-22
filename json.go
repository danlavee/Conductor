package conductor

import (
	"io"

	"github.com/danlavee/Conductor/internal/state"
)

// WriteJSON writes indented JSON followed by a newline.
func WriteJSON(writer io.Writer, value any) error {
	return state.WriteJSON(writer, value)
}
