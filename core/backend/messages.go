// SPDX-License-Identifier: MIT

package backend

import (
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

func tokenizerMessages(messages schema.Messages, c *config.ModelConfig) []*proto.Message {
	converted := messages.ToProto()
	name := strings.TrimSuffix(c.Backend, "-development")
	if name != "strata" && !strings.HasSuffix(name, "-strata") {
		return converted
	}

	marker := ""
	for _, option := range c.Options {
		key, value, _ := strings.Cut(option, ":")
		if strings.TrimSpace(key) == "media_marker" {
			marker = strings.TrimSpace(value)
		}
	}
	if marker == "" {
		marker = templates.DefaultMultiMediaMarker
	}
	// Unlike llama.cpp, Strata consumes markers in the proto message text to
	// associate the flat image list with the original conversation turns.
	for i, message := range messages {
		if len(message.StringImages) > 0 {
			converted[i].Content = strings.Repeat(marker, len(message.StringImages)) + message.StringContent
		}
	}
	return converted
}
