// SPDX-License-Identifier: MIT

package tokens

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/schema"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

const defaultEncoding = tiktoken.MODEL_CL100K_BASE

// EncodingFor resolves the encoding known by tiktoken and uses cl100k_base
// when the supplied model has no known mapping.
func EncodingFor(model string) string {
	if encoding, ok := tiktoken.MODEL_TO_ENCODING[model]; ok {
		return encoding
	}
	for prefix, encoding := range tiktoken.MODEL_PREFIX_TO_ENCODING {
		if strings.HasPrefix(model, prefix) {
			return encoding
		}
	}
	return defaultEncoding
}

// CountText counts text tokens without interpreting literal special-token
// strings as tokenizer control tokens.
func CountText(text, model string) (int, error) {
	encoding, err := tiktoken.GetEncoding(EncodingFor(model))
	if err != nil {
		return 0, fmt.Errorf("get token encoding: %w", err)
	}
	return len(encoding.EncodeOrdinary(text)), nil
}

// Count counts only textual message content.
func Count(messages []schema.Message, model string) (int, error) {
	total := 0
	for index, message := range messages {
		text, err := messageText(message.Content)
		if err != nil {
			return 0, fmt.Errorf("message %d: %w", index, err)
		}
		count, err := CountText(text, model)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func messageText(content any) (string, error) {
	switch value := content.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case []any:
		var text strings.Builder
		for index, rawPart := range value {
			part, ok := rawPart.(map[string]any)
			if !ok {
				return "", fmt.Errorf("unsupported multimodal content part %d of type %T", index, rawPart)
			}

			if rawText, exists := part["text"]; exists {
				partText, ok := rawText.(string)
				if !ok {
					return "", fmt.Errorf("unsupported multimodal text part %d: text has type %T", index, rawText)
				}
				text.WriteString(partText)
				continue
			}

			partType, ok := part["type"].(string)
			if ok && recognizedMediaType(partType) {
				continue
			}
			return "", fmt.Errorf("unsupported multimodal content part %d", index)
		}
		return text.String(), nil
	case map[string]any:
		if len(value) == 0 {
			return "", nil
		}
		return "", fmt.Errorf("unsupported message content of type %T", content)
	default:
		return "", fmt.Errorf("unsupported message content of type %T", content)
	}
}

func recognizedMediaType(partType string) bool {
	switch partType {
	case "image", "image_url", "audio", "audio_url", "input_audio", "video", "video_url":
		return true
	default:
		return false
	}
}
