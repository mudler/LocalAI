package tokens

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/schema"
)

// CountMessages returns a stable OpenAI-compatible estimate that includes
// roles, multimodal text, tool calls, and tool results. Exact backend counts
// are model-specific; the safety margin comes from the configurable trigger.
func CountMessages(messages []schema.Message) (int, error) {
	total := 0
	for _, message := range messages {
		payload, err := json.Marshal(message)
		if err != nil {
			return 0, fmt.Errorf("encode %s message: %w", message.Role, err)
		}
		// Use a conservative offline estimate. A vocabulary download in the
		// request path can hang firewalled installations, while LocalAI must
		// decide whether to compress before any model is loaded. One token per
		// JSON byte is a safe upper-bound estimate for byte-fallback tokenizers.
		// It intentionally triggers compression early instead of risking a late
		// backend context rejection for code, identifiers, or multilingual text.
		total += 4 + len(payload)
	}
	return total + 2, nil
}

// CountPayload estimates non-message request data such as tool schemas.
func CountPayload(value any) (int, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("encode token payload: %w", err)
	}
	if string(payload) == "{}" || string(payload) == "null" {
		return 0, nil
	}
	return len(payload), nil
}
