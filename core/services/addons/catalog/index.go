// SPDX-License-Identifier: MIT
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

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
	if len(data) > MaxIndexBytes {
		return nil, errors.New("index is too large")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(yaml.Node)); err != io.EOF {
		return nil, errors.New("index holds more than one document")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.SequenceNode {
		return nil, errors.New("index must be a list of releases")
	}
	list := doc.Content[0]
	if list.Style&yaml.FlowStyle != 0 && len(list.Content) > 0 {
		return nil, errors.New("index must be a YAML block list, not JSON")
	}
	if len(list.Content) > MaxIndexReleases {
		return nil, errors.New("index has too many releases")
	}
	var plain []any
	if err := list.Decode(&plain); err != nil {
		return nil, err
	}
	for _, item := range plain {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("index entry must be a mapping")
		}
		if err := NormalizeLegacyID(entry); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(plain)
	if err != nil {
		return nil, err
	}
	var releases []Release
	if err := decode(b, &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// LegacyIDField is the name the identifier of an entry had before the shared
// envelope. A reader still accepts it for one release; every writer emits id.
const LegacyIDField = "addonId"

// NormalizeLegacyID moves the legacy addonId of a decoded entry to id, so one
// strict decoder serves both spellings. An entry that holds both is refused
// even when they agree, because it is not the output of any writer.
func NormalizeLegacyID(entry map[string]any) error {
	legacy, ok := entry[LegacyIDField]
	if !ok {
		return nil
	}
	if _, both := entry["id"]; both {
		return errors.New("index entry has both id and " + LegacyIDField)
	}
	entry["id"] = legacy
	delete(entry, LegacyIDField)
	return nil
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
