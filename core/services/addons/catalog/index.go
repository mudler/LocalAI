// SPDX-License-Identifier: MIT
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"gopkg.in/yaml.v3"
)

const (
	// MaxIndexBytes and MaxIndexReleases bound one library index document.
	MaxIndexBytes    = 1 << 20
	MaxIndexReleases = 1000
)

// DecodeIndex reads a library index: one YAML document that is a list of
// releases. It is as strict as the JSON reader it replaces: a field that is
// not part of Release, a second document, a duplicate key or a value of the
// wrong type is refused, and so is a document larger than MaxIndexBytes or
// longer than MaxIndexReleases entries.
//
// A JSON array is also a YAML flow sequence, so the reader refuses a
// non-empty list written in flow style at the top level. The index is a block
// list; this keeps the two formats from being accepted by accident.
//
// The YAML is converted to plain values and read through the JSON field names
// of Release, so one set of names and one strict decoder serve the whole type.
func DecodeIndex(data []byte) ([]Release, error) {
	res, err := ReadIndex(data, ReadableKinds...)
	if err != nil {
		return nil, err
	}
	return res.Releases, nil
}

// ReadIndex reads a library index the way DecodeIndex does and applies the
// reader's kinds. An entry carries an optional "kind". A reader keeps the
// entries of the kinds it is given and skips every other entry, without
// reading it any further and without refusing the index; the skipped entries
// come back in the result so the caller can report them. Without kinds the
// reader is an add-on reader. A kind that is not plain text, or not a
// well-formed kind name, makes the index malformed and it is refused.
func ReadIndex(data []byte, known ...Kind) (IndexResult, error) {
	if len(known) == 0 {
		known = []Kind{KindAddon}
	}
	if len(data) > MaxIndexBytes {
		return IndexResult{}, errors.New("index is too large")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return IndexResult{}, err
	}
	if err := dec.Decode(new(yaml.Node)); err != io.EOF {
		return IndexResult{}, errors.New("index holds more than one document")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.SequenceNode {
		return IndexResult{}, errors.New("index must be a list of releases")
	}
	list := doc.Content[0]
	if list.Style&yaml.FlowStyle != 0 && len(list.Content) > 0 {
		return IndexResult{}, errors.New("index must be a YAML block list, not JSON")
	}
	if len(list.Content) > MaxIndexReleases {
		return IndexResult{}, errors.New("index has too many releases")
	}
	var plain []any
	if err := list.Decode(&plain); err != nil {
		return IndexResult{}, err
	}
	res := IndexResult{}
	kept := make([]any, 0, len(plain))
	for i, item := range plain {
		entry, ok := item.(map[string]any)
		if !ok {
			return IndexResult{}, errors.New("index entry must be a mapping")
		}
		if raw, present := entry["kind"]; present {
			kind, ok := raw.(string)
			if !ok || !kindPattern.MatchString(kind) {
				return IndexResult{}, errors.New("index entry has a malformed kind")
			}
			if !slices.Contains(known, Kind(kind)) {
				id, _ := entry["addonId"].(string)
				res.Skipped = append(res.Skipped, SkippedEntry{Position: i, Kind: kind, ID: id})
				continue
			}
		}
		kept = append(kept, entry)
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return IndexResult{}, err
	}
	if err := decode(b, &res.Releases); err != nil {
		return IndexResult{}, err
	}
	return res, nil
}

// EncodeIndex writes releases as a library index. It is the inverse of
// DecodeIndex and exists for fixtures and tools that serve a library.
func EncodeIndex(releases []Release) ([]byte, error) {
	b, err := json.Marshal(releases)
	if err != nil {
		return nil, err
	}
	var plain any
	if err := yaml.Unmarshal(b, &plain); err != nil {
		return nil, err
	}
	return yaml.Marshal(plain)
}

// WriteIndex writes the index of releases to w.
func WriteIndex(w io.Writer, releases []Release) error {
	b, err := EncodeIndex(releases)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
