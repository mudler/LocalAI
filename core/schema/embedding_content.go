// SPDX-License-Identifier: MIT
package schema

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/embeddingmedia"
)

type EmbeddingInputItem struct {
	Text    *string
	Content []EmbeddingContentPart
}
type EmbeddingContentPart struct {
	Type   string
	Text   string
	Source EmbeddingMediaSource
}
type EmbeddingMediaSource struct {
	Data   string
	URL    string
	Format string
}

func embeddingMalformed() error {
	return &embeddingmedia.Error{Status: 400, Code: "invalid_embedding_input"}
}

// embeddingObject preserves duplicate-key evidence before any map binding.
func embeddingObject(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, embeddingMalformed()
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		t, e = d.Token()
		if e != nil {
			return nil, embeddingMalformed()
		}
		key, ok := t.(string)
		if !ok {
			return nil, embeddingMalformed()
		}
		valid := false
		for _, a := range allowed {
			if key == a {
				valid = true
			}
		}
		if !valid || out[key] != nil {
			return nil, embeddingMalformed()
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, embeddingMalformed()
		}
		out[key] = v
	}
	if _, e = d.Token(); e != nil {
		return nil, embeddingMalformed()
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, embeddingMalformed()
	}
	return out, nil
}
func embeddingString(raw []byte) (string, error) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil || !utf8.Valid(raw) {
		return "", embeddingMalformed()
	}
	return s, nil
}

// ParseEmbeddingInput leaves legacy values to the existing parser. Object
// members of a batch opt the entire batch into strict structured validation.
func ParseEmbeddingInput(raw json.RawMessage) ([]EmbeddingInputItem, bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false, nil
	}
	var entries []json.RawMessage
	structured := raw[0] == '{'
	if raw[0] == '[' {
		if json.Unmarshal(raw, &entries) != nil {
			return nil, false, embeddingMalformed()
		}
		for _, v := range entries {
			if len(v) > 0 && v[0] == '{' {
				structured = true
			}
		}
	} else {
		entries = []json.RawMessage{raw}
	}
	if !structured {
		return nil, false, nil
	}
	if uint64(len(raw)) > embeddingmedia.MaxBodyBytes {
		return nil, true, &embeddingmedia.Error{Status: 413, Code: "embedding_resource_limit"}
	}
	budget := embeddingmedia.NewBudget()
	if err := budget.Consume(embeddingmedia.Consumption{Items: uint64(len(entries))}); err != nil {
		return nil, true, err
	}
	items := make([]EmbeddingInputItem, 0, len(entries))
	for _, entry := range entries {
		item, err := parseEmbeddingItem(entry, &budget)
		if err != nil {
			return nil, true, err
		}
		items = append(items, item)
	}
	return items, true, nil
}

// parseEmbeddingItem parses one string or content object and charges its text,
// parts and media bytes to budget. Rerank queries and documents use it too, so
// they cannot drift from the embedding rules.
func parseEmbeddingItem(entry json.RawMessage, budget *embeddingmedia.Budget) (EmbeddingInputItem, error) {
	var item EmbeddingInputItem
	if len(entry) > 0 && entry[0] == '"' {
		text, err := embeddingString(entry)
		if err != nil {
			return item, err
		}
		item.Text = &text
		if err = budget.Consume(embeddingmedia.Consumption{TextBytes: uint64(len(text)), SourceParts: 1}); err != nil {
			return item, err
		}
		return item, nil
	}
	obj, err := embeddingObject(entry, "content")
	if err != nil {
		return item, err
	}
	var parts []json.RawMessage
	if json.Unmarshal(obj["content"], &parts) != nil || len(parts) == 0 {
		return item, embeddingMalformed()
	}
	if err = budget.Consume(embeddingmedia.Consumption{SourceParts: uint64(len(parts))}); err != nil {
		return item, err
	}
	for _, p := range parts {
		part, err := parseEmbeddingPart(p, budget)
		if err != nil {
			return item, err
		}
		item.Content = append(item.Content, part)
	}
	return item, nil
}
func parseEmbeddingPart(raw []byte, b *embeddingmedia.Budget) (EmbeddingContentPart, error) {
	var p EmbeddingContentPart
	obj, err := embeddingObject(raw, "type", "text", "image_url", "input_audio", "input_video", "video_url")
	if err != nil {
		return p, err
	}
	p.Type, err = embeddingString(obj["type"])
	if err != nil {
		return p, err
	}
	if len(obj) != 2 {
		return p, embeddingMalformed()
	}
	if p.Type == "text" {
		p.Text, err = embeddingString(obj["text"])
		if err != nil {
			return p, err
		}
		return p, b.Consume(embeddingmedia.Consumption{TextBytes: uint64(len(p.Text))})
	}
	switch p.Type {
	case "image_url", "input_audio", "input_video", "video_url":
	default:
		return p, embeddingMalformed()
	}
	allowed := []string{"data", "url", "format"}
	if p.Type == "image_url" {
		allowed = []string{"url"}
	}
	source, err := embeddingObject(obj[p.Type], allowed...)
	if err != nil {
		return p, err
	}
	if (source["data"] == nil) == (source["url"] == nil) {
		return p, embeddingMalformed()
	}
	if v := source["format"]; v != nil {
		p.Source.Format, err = embeddingString(v)
		if err != nil || p.Source.Format == "" {
			return p, embeddingMalformed()
		}
	}
	if v := source["data"]; v != nil {
		p.Source.Data, err = embeddingString(v)
		if err != nil {
			return p, err
		}
		err = validateEmbeddingBase64(p.Source.Data, b)
	} else {
		p.Source.URL, err = embeddingString(source["url"])
		if err != nil {
			return p, err
		}
		if strings.HasPrefix(p.Source.URL, "data:") {
			header, data, ok := strings.Cut(p.Source.URL, ",")
			if !ok || !strings.HasSuffix(header, ";base64") {
				return p, embeddingMalformed()
			}
			err = validateEmbeddingBase64(data, b)
		} else {
			u, e := url.Parse(p.Source.URL)
			if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
				return p, embeddingMalformed()
			}
		}
	}
	if p.Type == "video_url" {
		p.Type = "input_video"
	}
	return p, err
}

// Validate without allocating a second media-sized buffer. The exact decoded
// count (including padding) is charged, not DecodedLen's upper bound.
func validateEmbeddingBase64(s string, b *embeddingmedia.Budget) error {
	if s == "" || strings.ContainsAny(s, "\r\n") {
		return embeddingMalformed()
	}
	n, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(s)))
	if err != nil {
		return embeddingMalformed()
	}
	return b.Consume(embeddingmedia.Consumption{MediaBytes: uint64(n)})
}

// embeddingKeyFold maps a member name to the canonical form encoding/json uses
// for its case-insensitive field match. Names that the binder treats as the
// same field, such as "ſ" for "s" or the Kelvin sign for "k", fold equal here.
func embeddingKeyFold(key string) string {
	return strings.Map(func(r rune) rune {
		for {
			next := unicode.SimpleFold(r)
			if next <= r {
				return next
			}
			r = next
		}
	}, key)
}

// ParseEmbeddingRequest inspects every input occurrence before binding can
// discard duplicates. Strict envelope validation applies only to structured input: duplicate
// keys (case-insensitive, as the binder folds them), input aliases, and non-empty messages.
// Unrelated top-level fields are left to the binder; strictness covers content objects.
func ParseEmbeddingRequest(raw json.RawMessage) ([]EmbeddingInputItem, bool, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false, nil
	}
	seen := map[string]bool{}
	duplicate := false
	alias := false
	messages := false
	inputName := embeddingKeyFold("input")
	messagesName := embeddingKeyFold("messages")
	var items []EmbeddingInputItem
	structured := false
	var parseErr error
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return embeddingEnvelopeError(structured)
		}
		key, ok := token.(string)
		if !ok {
			return embeddingEnvelopeError(structured)
		}
		// The JSON binder matches keys with its own case folding, so fold the
		// same way before looking for duplicates and reject aliases of input.
		folded := embeddingKeyFold(key)
		duplicate = duplicate || seen[folded]
		seen[folded] = true
		alias = alias || (folded == inputName && key != "input")
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return embeddingEnvelopeError(structured)
		}
		if folded == messagesName && !emptyMessages(value) {
			messages = true
		}
		if folded == inputName {
			parsed, isStructured, e := ParseEmbeddingInput(value)
			structured = structured || isStructured
			if isStructured {
				items = parsed
			}
			if e != nil {
				parseErr = e
			}
		}
	}
	if _, err = d.Token(); err != nil {
		return embeddingEnvelopeError(structured)
	}
	if _, err = d.Token(); err != io.EOF {
		return embeddingEnvelopeError(structured)
	}
	if !structured {
		return nil, false, nil
	}
	if duplicate || alias || messages {
		return nil, true, embeddingMalformed()
	}
	return items, true, parseErr
}

// emptyMessages reports whether a messages value is null or an empty array.
// Only a non-empty value conflicts with structured input.
func emptyMessages(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		return true
	}
	var items []json.RawMessage
	return json.Unmarshal(raw, &items) == nil && len(items) == 0
}

// Legacy syntax errors remain the responsibility of the existing binder.
func embeddingEnvelopeError(structured bool) ([]EmbeddingInputItem, bool, error) {
	if !structured {
		return nil, false, nil
	}
	return nil, true, embeddingMalformed()
}

// The types below describe the structured embedding input for the API schema.
// They are documentation only: requests are validated by ParseEmbeddingInput,
// which rejects unknown fields and duplicate keys.

// EmbeddingContentObject is one structured input item. It produces exactly one
// vector from all of its parts, taken in order.
type EmbeddingContentObject struct {
	// Content is a non-empty ordered list of parts.
	Content []EmbeddingContentPartDoc `json:"content"`
}

// EmbeddingContentPartDoc is one part of a content object. Set `type` and the
// member of the same name; no other member is accepted.
type EmbeddingContentPartDoc struct {
	// Type selects the part kind. video_url is an alias of input_video.
	Type string `json:"type" enums:"text,image_url,input_audio,input_video,video_url"`
	// Text is used by type text. It is passed without separators, prefixes or templates.
	Text string `json:"text,omitempty"`
	// ImageURL is used by type image_url: an HTTP(S) URL or a base64 data URI (PNG or JPEG).
	ImageURL *EmbeddingImageSourceDoc `json:"image_url,omitempty"`
	// InputAudio is used by type input_audio (WAV, MP3 or FLAC).
	InputAudio *EmbeddingMediaSourceDoc `json:"input_audio,omitempty"`
	// InputVideo is used by type input_video (MP4 or WebM).
	InputVideo *EmbeddingMediaSourceDoc `json:"input_video,omitempty"`
	// VideoURL is an alias of input_video with the same members.
	VideoURL *EmbeddingMediaSourceDoc `json:"video_url,omitempty"`
}

// EmbeddingImageSourceDoc is the source of an image part.
type EmbeddingImageSourceDoc struct {
	URL string `json:"url"`
}

// EmbeddingMediaSourceDoc is the source of an audio or video part. Exactly one
// of data and url is set.
type EmbeddingMediaSourceDoc struct {
	// Data is raw base64.
	Data string `json:"data,omitempty"`
	// URL is an HTTP(S) URL or a base64 data URI.
	URL string `json:"url,omitempty"`
	// Format is optional and must match the detected media format.
	Format string `json:"format,omitempty"`
}
