// SPDX-License-Identifier: MIT
package toolargs

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// args builds an argument object of exactly n bytes with one string property.
// Bind adds `,"tool":"t"` (11 bytes) to what the add-on receives.
func args(n int) json.RawMessage {
	const frame = `{"content":""}`
	return json.RawMessage(`{"content":"` + strings.Repeat("a", n-len(frame)) + `"}`)
}

func TestTheArgumentLimitIsOneMiBAndOneByteMoreIsRefused(t *testing.T) {
	if MaxArgumentBytes != 1<<20 {
		t.Fatalf("limit %d, want 1 MiB", MaxArgumentBytes)
	}
	params := map[string]any{"properties": map[string]any{"content": map[string]any{"type": "string"}}}
	// The limit is on what the add-on receives: the tool key counts.
	const toolKey = len(`,"tool":"t"`)
	_, body, err := Bind("t", params, args(MaxArgumentBytes-toolKey), false)
	if err != nil || len(body) != MaxArgumentBytes {
		t.Fatalf("a body of exactly 1 MiB must be accepted: %v (%d bytes)", err, len(body))
	}
	if _, _, err := Bind("t", params, args(MaxArgumentBytes-toolKey+1), false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a body of 1 MiB + 1 byte must be refused, got %v", err)
	}
	if _, _, err := Bind("t", params, args(MaxArgumentBytes+1), false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an input over 1 MiB must be refused, got %v", err)
	}
}
