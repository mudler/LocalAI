package voicerecognition

import (
	"context"
	"path"
	"path/filepath"
	"strings"
)

// KnownVoice is one registered voice as it is sent to a backend that matches
// speakers itself.
type KnownVoice struct {
	Name      string
	Embedding []float32
	Model     string
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

// SelectKnownVoices picks, from everything registered, the voices that can be
// compared with embeddings from the speaker model at speakerModelPath: those
// with the same encoder tag, then the untagged voices whose size matches the
// tagged ones (all untagged voices when none matched). Voices from another
// encoder are counted and skipped, as are voices without a name or embedding.
// The input is not modified.
func SelectKnownVoices(entries []Entry, speakerModelPath string) KnownVoiceSelection {
	tag := EncoderTag(speakerModelPath)
	var sel KnownVoiceSelection
	matchedDim := 0
	var untagged []Entry
	for _, e := range entries {
		if e.Metadata.Name == "" || len(e.Embedding) == 0 {
			continue
		}
		switch {
		case e.Metadata.Model == "":
			untagged = append(untagged, e)
		case EncoderTag(e.Metadata.Model) == tag:
			if matchedDim == 0 {
				matchedDim = len(e.Embedding)
			}
			sel.Voices = append(sel.Voices, KnownVoice{Name: e.Metadata.Name, Embedding: e.Embedding, Model: e.Metadata.Model})
		default:
			sel.OtherEncoder++
		}
	}
	for _, e := range untagged {
		if matchedDim != 0 && len(e.Embedding) != matchedDim {
			continue
		}
		sel.Untagged++
		sel.Voices = append(sel.Voices, KnownVoice{Name: e.Metadata.Name, Embedding: e.Embedding})
	}
	return sel
}

// KnownVoicesFor lists the registry and selects the voices for a speaker model.
func KnownVoicesFor(ctx context.Context, reg Registry, speakerModelPath string) (KnownVoiceSelection, error) {
	entries, err := reg.List(ctx)
	if err != nil {
		return KnownVoiceSelection{}, err
	}
	return SelectKnownVoices(entries, speakerModelPath), nil
}
