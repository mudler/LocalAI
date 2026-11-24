package middleware

import "github.com/mudler/LocalAI/core/schema"

// imagePartTypes are the content-part types that carry an image in the
// OpenAI chat (image_url), OpenAI Responses (input_image) and Anthropic
// (image) message formats.
var imagePartTypes = map[string]bool{"image_url": true, "input_image": true, "image": true}

// requestHasImages reports whether a chat request carries image input in any
// message. It reads the raw message content as decoded from JSON, before the
// multimodal staging buffers are filled, so it can steer alias resolution.
func requestHasImages(input schema.LocalAIRequest) bool {
	req, ok := input.(*schema.OpenAIRequest)
	if !ok || req == nil {
		return false
	}
	for _, m := range req.Messages {
		if len(m.StringImages) > 0 {
			return true
		}
		parts, ok := m.Content.([]any)
		if !ok {
			continue
		}
		for _, p := range parts {
			if part, ok := p.(map[string]any); ok {
				if t, _ := part["type"].(string); imagePartTypes[t] {
					return true
				}
			}
		}
	}
	return false
}
