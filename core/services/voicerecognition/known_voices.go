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

// optionValue returns the trimmed value of the first key:value entry in
// options whose key is key, or "" when there is none.
func optionValue(options []string, key string) string {
	for _, o := range options {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// SpeakerModelFromOptions returns the speaker encoder a model config names, or
// "" when it names none. A speaker_model:<file> entry wins. Without one, a
// speaker_component:<name> entry names a component of the model's own bundle
// file (parakeet-cpp), and its value is returned. A component name is not a
// file name: it must not be used as an encoder tag (see SpeakerTagFromOptions).
func SpeakerModelFromOptions(options []string) string {
	if v := optionValue(options, "speaker_model"); v != "" {
		return v
	}
	return optionValue(options, "speaker_component")
}

// SpeakerTagFromOptions returns the lowercased value of a speaker_tag:<tag>
// entry, or "". It is an extra encoder tag for legacy voices that carry only
// a file-name tag. A bundle has no encoder file name of its own, so such a
// voice matches a bundle's speaker component only through this alias.
func SpeakerTagFromOptions(options []string) string {
	return strings.ToLower(optionValue(options, "speaker_tag"))
}

// SpeakerEncoder is the speaker encoder a model config names, as far as voice
// selection needs it.
type SpeakerEncoder struct {
	// Ref is what the config names: the speaker_model file, else the
	// speaker_component name. Empty when the config names no encoder.
	Ref string
	// File is the speaker_model path. Empty for a speaker_component, which
	// names a part of the model's own bundle file, not an encoder file.
	File string
	// Tags are extra encoder tags from speaker_tag.
	Tags []string
}

// SpeakerEncoderFromOptions reads the speaker encoder from a model config's
// options. See SpeakerModelFromOptions and SpeakerTagFromOptions.
func SpeakerEncoderFromOptions(options []string) SpeakerEncoder {
	enc := SpeakerEncoder{Ref: SpeakerModelFromOptions(options), File: optionValue(options, "speaker_model")}
	if t := SpeakerTagFromOptions(options); t != "" {
		enc.Tags = []string{t}
	}
	return enc
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
//
// extraTags are further tags that count as the loaded encoder (the speaker_tag
// option). They are compared as lowercase names, like EncoderTag.
func SelectKnownVoices(entries []Entry, speakerModelPath string, extraTags ...string) KnownVoiceSelection {
	tags := make(map[string]bool, len(extraTags)+1)
	if speakerModelPath != "" {
		tags[EncoderTag(speakerModelPath)] = true
	}
	for _, t := range extraTags {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			tags[t] = true
		}
	}
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
		case strings.HasPrefix(e.Metadata.Model, "sha256:"), tags[EncoderTag(e.Metadata.Model)]:
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
func KnownVoicesFor(ctx context.Context, reg Registry, speakerModelPath string, extraTags ...string) (KnownVoiceSelection, error) {
	entries, err := reg.List(ctx)
	if err != nil {
		return KnownVoiceSelection{}, err
	}
	return SelectKnownVoices(entries, speakerModelPath, extraTags...), nil
}
