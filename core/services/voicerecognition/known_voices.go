package voicerecognition

import (
	"context"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// KnownVoice is one registered voice as it is sent to a backend that matches
// speakers itself.
type KnownVoice struct {
	ID        string
	Name      string
	Embedding []float32
	Model     string
	// Family and Weights fingerprint the encoder that made the embedding, as
	// far as it is known: the embedding space and the "sha256:" identity of the
	// exact weights. Empty for a voice registered without them.
	Family  string
	Weights string
}

// SpeakerModelFromOptions returns the value of a speaker_model:<file> entry in
// a model config's options, or "" when there is none.
func SpeakerModelFromOptions(options []string) string {
	for _, o := range options {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == "speaker_model" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// EncoderTag is how an encoder is identified: the lowercased base name of its
// model file. A voice registered through the voice-detect backend carries the
// backend's model name, which defaults to that base name.
func EncoderTag(modelPath string) string {
	return strings.ToLower(path.Base(filepath.ToSlash(modelPath)))
}

// KnownVoiceSelection is the result of SelectKnownVoices.
type KnownVoiceSelection struct {
	Voices       []KnownVoice
	OtherEncoder int // voices skipped because another encoder made them
	Untagged     int // voices with no encoder tag that were included
}

// SelectKnownVoices selects filename-matching, portable and untagged candidates.
// Registry tags and vector lengths are not trusted encoder metadata: dimension
// filtering belongs to the loaded backend. Tagged candidates precede untagged
// ones, each ordered by registration ID so registry iteration order cannot
// change replay order. The input is not modified.
func SelectKnownVoices(entries []Entry, speakerModelPath string) KnownVoiceSelection {
	tag := EncoderTag(speakerModelPath)
	var sel KnownVoiceSelection
	var untagged []Entry
	for _, e := range entries {
		if e.Metadata.Name == "" || len(e.Embedding) == 0 {
			continue
		}
		switch {
		case e.Metadata.Model == "":
			untagged = append(untagged, e)
			// Hash-tagged portable registrations are checked against the loaded
		// encoder by the backend, never against a filename or dimension alone.
		case strings.HasPrefix(e.Metadata.Model, "sha256:"), EncoderTag(e.Metadata.Model) == tag:
			sel.Voices = append(sel.Voices, knownVoice(e))
		default:
			sel.OtherEncoder++
		}
	}
	sort.Slice(sel.Voices, func(i, j int) bool { return sel.Voices[i].ID < sel.Voices[j].ID })
	sort.Slice(untagged, func(i, j int) bool { return untagged[i].Metadata.ID < untagged[j].Metadata.ID })
	for _, e := range untagged {
		sel.Untagged++
		sel.Voices = append(sel.Voices, KnownVoice{ID: e.Metadata.ID, Name: e.Metadata.Name, Embedding: e.Embedding})
	}
	return sel
}

func knownVoice(e Entry) KnownVoice {
	v := KnownVoice{ID: e.Metadata.ID, Name: e.Metadata.Name, Embedding: e.Embedding, Model: e.Metadata.Model, Family: e.Metadata.EncoderFamily}
	if strings.HasPrefix(e.Metadata.Model, "sha256:") {
		v.Weights = e.Metadata.Model
	}
	return v
}

// KnownVoicesFor lists the registry and selects the voices for a speaker model.
func KnownVoicesFor(ctx context.Context, reg Registry, speakerModelPath string) (KnownVoiceSelection, error) {
	entries, err := reg.List(ctx)
	if err != nil {
		return KnownVoiceSelection{}, err
	}
	return SelectKnownVoices(entries, speakerModelPath), nil
}
