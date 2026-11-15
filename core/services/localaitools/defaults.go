package localaitools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/wellknown"
)

// The model of a client tool comes from one place: Inference.choose. It runs
// these rules in order:
//
//  1. A model the caller names wins. It is the "use another model" of one
//     request, and it never changes the user's default.
//  2. Otherwise the user's default for the tool's kind of work, from
//     defaults.json, applies (defaultKinds says which key a tool reads):
//     - a model name: that model, when it is installed. When it is not, the tool
//     stops with a message that says so and asks the user. It does not use
//     another model.
//     - "ask": the tool stops and the assistant asks the user which model.
//  3. With no default (a missing key or null), the first installed model that
//     has the use case, as before defaults.json existed.

// defaultKind ties a tool to its key in defaults.json and to the words that
// say it to the user.
type defaultKind struct {
	Key string // the key in defaults.json
	For string // "your default for ..."
}

// defaultKinds lists the tools that read defaults.json. A tool that is not
// here (detect, sound, voice, face, upscale, transform_audio) has no default.
var defaultKinds = map[string]defaultKind{
	ToolGenerateImage:   {"image", "images"},
	ToolSpeak:           {"tts", "speech"},
	ToolTranscribeAudio: {"stt", "transcription"},
	ToolEmbed:           {"embeddings", "embeddings"},
	ToolRerank:          {"rerank", "reranking"},
	ToolGenerateVideo:   {"video", "video"},
	ToolDescribeImage:   {"vision", "image understanding"},
}

// DefaultReader reads the default of the acting user for one key of
// defaults.json. An answer of DefaultUnset means no choice, also when the file
// cannot be read: a tool never fails for it.
type DefaultReader func(ctx context.Context, key string) wellknown.Default

// ModelChecker is the optional second half of a ModelChooser: it says whether
// a model is installed and enabled. Without it, a default is trusted.
type ModelChecker interface {
	HasModel(ctx context.Context, name string) (bool, error)
}

// picked is the answer of choose.
type picked struct {
	Model string
	// Note is a sentence for the result, "Using flux, your default for images."
	// It is empty unless a default decided.
	Note string
}

// AskModelError means the user wants to choose the model each time.
type AskModelError struct{ Key, Kind string }

func (e *AskModelError) Error() string {
	return fmt.Sprintf("The user chose to be asked which %s model to use each time (their %q default is \"ask\"). Nothing was made. "+
		"Ask them which model to use, offering the installed ones from list_models, then call this tool again with the model argument.", e.Kind, e.Key)
}

// DefaultGoneError means the default model is not installed any more.
type DefaultGoneError struct{ Key, Kind, Model string }

func (e *DefaultGoneError) Error() string {
	return fmt.Sprintf("The user's default model for %s, %q, is not installed or is disabled. Nothing was made, and no other model was used. "+
		"Tell the user, ask which installed model to use this time (list_models shows them), then call this tool again with the model argument. "+
		"They can set a new default in Account > Defaults.", e.Kind, e.Model)
}

// fromDefault applies the user's default to a tool. found is false when no
// default decides and the caller keeps its own rule.
func (i *Inference) fromDefault(ctx context.Context, spec ClientToolSpec) (p picked, found bool, err error) {
	kind, ok := defaultKinds[spec.Tool]
	if !ok || i == nil || i.Defaults == nil {
		return picked{}, false, nil
	}
	d := i.Defaults(ctx, kind.Key)
	switch d.Kind {
	case wellknown.DefaultAsk:
		return picked{}, false, &AskModelError{Key: kind.Key, Kind: spec.Capability}
	case wellknown.DefaultModel:
		if mc, ok := i.Models.(ModelChecker); ok {
			has, err := mc.HasModel(ctx, d.Model)
			if err == nil && !has {
				return picked{}, false, &DefaultGoneError{Key: kind.Key, Kind: spec.Capability, Model: d.Model}
			}
		}
		return picked{Model: d.Model, Note: fmt.Sprintf("Using %s, the user's default for %s.", d.Model, kind.For)}, true, nil
	}
	return picked{}, false, nil
}

// choose returns the model a tool uses, by the rules at the top of this file.
func (i *Inference) choose(ctx context.Context, given string, spec ClientToolSpec) (picked, error) {
	if given = strings.TrimSpace(given); given != "" {
		return picked{Model: given}, nil
	}
	if p, found, err := i.fromDefault(ctx, spec); err != nil || found {
		return p, err
	}
	if i == nil || i.Models == nil {
		return picked{}, errNoModelHere
	}
	m, err := i.Models.FirstModel(ctx, spec.Flag)
	if err != nil {
		return picked{}, err
	}
	if m == "" {
		return picked{}, &NoModelError{Capability: spec.Capability}
	}
	return picked{Model: m}, nil
}

var errNoModelHere = errors.New("cannot choose a model here: name one with the model argument")
