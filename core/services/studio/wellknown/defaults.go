package wellknown

import (
	"context"
	"fmt"
	"strings"
)

// DefaultCapabilities are the keys of defaults.json: one per kind of work.
var DefaultCapabilities = []string{"chat", "image", "tts", "stt", "embeddings", "rerank", "video", "vision"}

// Ask is the value of a default that means "ask the person each time".
const Ask = "ask"

// DefaultKind says what a default asks for.
type DefaultKind string

const (
	// DefaultUnset: the person made no choice. The caller keeps its own
	// behaviour (a tool picks an installed model itself).
	DefaultUnset DefaultKind = ""
	// DefaultModel: use the model in Default.Model.
	DefaultModel DefaultKind = "model"
	// DefaultAsk: ask the person which model to use, every time.
	DefaultAsk DefaultKind = "ask"
)

// Default is the person's choice for one kind of work.
type Default struct {
	Kind  DefaultKind
	Model string
	// From is the file the choice came from: "defaults.json", or "ui.json"
	// for the chat default that only the older defaultModel key holds.
	From string
}

// DefaultFor reads the default of a capability from the data of defaults.json
// and ui.json. A missing key and a null are the same: unset. For "chat" only,
// an unset default falls back to defaultModel of ui.json, which is only read,
// never written, so a file from before defaults.json keeps working.
func DefaultFor(defaults, ui map[string]any, capability string) Default {
	if v, ok := defaults[capability].(string); ok {
		switch v = strings.TrimSpace(v); {
		case v == "":
		case strings.EqualFold(v, Ask):
			return Default{Kind: DefaultAsk, From: "defaults.json"}
		default:
			return Default{Kind: DefaultModel, Model: v, From: "defaults.json"}
		}
	}
	if capability == "chat" {
		if v, ok := ui["defaultModel"].(string); ok && strings.TrimSpace(v) != "" {
			return Default{Kind: DefaultModel, Model: strings.TrimSpace(v), From: "ui.json"}
		}
	}
	return Default{}
}

// DefaultFor reads the default of a capability of the user. A file that cannot
// be read leaves the default unset: a tool never fails for it.
func (s *Service) DefaultFor(ctx context.Context, principal, capability string) Default {
	if !s.Available() {
		return Default{}
	}
	var defaults, ui map[string]any
	if doc, err := s.Read(ctx, principal, Defaults, ""); err == nil {
		defaults = doc.Data
	}
	if capability == "chat" {
		if doc, err := s.Read(ctx, principal, UI, ""); err == nil {
			ui = doc.Data
		}
	}
	return DefaultFor(defaults, ui, capability)
}

// defaultsErrors says what the schema cannot: a blank name, or a name with a
// control character. A value is a model name, "ask" or null.
func defaultsErrors(obj map[string]any) []string {
	var errs []string
	for _, key := range DefaultCapabilities {
		v, ok := obj[key].(string)
		if !ok {
			continue
		}
		if strings.TrimSpace(v) == "" || hasControlChar(v) {
			errs = append(errs, fmt.Sprintf("%s: use the name of an installed model, \"ask\" or null; %q is neither", key, clip(v, 80)))
		}
	}
	return errs
}
