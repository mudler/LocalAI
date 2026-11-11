package wellknown

import (
	"fmt"
	"slices"
	"strings"
)

// SoundNames are the sounds a notification event may use. The browser makes
// each one itself, so nothing is downloaded. none is silent.
var SoundNames = []string{"none", "soft-chime", "double-tap", "bell", "pop", "low-tone"}

// SoundEvents are the events that can play a sound.
var SoundEvents = []string{"replyDone", "approval", "taskDone", "error"}

// soundErrors says, in words, what the schema cannot say about the sound of
// ui.json: quiet hours that start and end at the same time.
func soundErrors(raw any) []string {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	var out []string
	if q, ok := m["quietHours"].([]any); ok && len(q) == 2 {
		from, _ := q[0].(string)
		to, _ := q[1].(string)
		if from != "" && from == to {
			out = append(out, fmt.Sprintf("sound.quietHours: from and to are both %s, which is no time at all; leave quietHours out for no quiet hours, or use two different times such as [\"22:00\", \"07:00\"]", from))
		}
	}
	if ev, ok := m["events"].(map[string]any); ok {
		for _, k := range SoundEvents {
			if s, ok := ev[k].(string); ok && !slices.Contains(SoundNames, s) {
				out = append(out, fmt.Sprintf("sound.events.%s: %q is not a sound; use one of %s", k, clip(s, 40), strings.Join(SoundNames, ", ")))
			}
		}
	}
	return out
}

// voiceErrors says what the schema cannot: a voice name with a control character.
func voiceErrors(raw any) []string {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if v, _ := m["voice"].(string); hasControlChar(v) {
		return []string{"voice.voice: a voice name is plain text without control characters"}
	}
	return nil
}
