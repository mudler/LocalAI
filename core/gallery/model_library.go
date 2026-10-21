// SPDX-License-Identifier: MIT
package gallery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"gopkg.in/yaml.v3"
)

// The model library, gallery/models/index.yaml, is the source of truth for
// the models. Edits go there. The gallery index, gallery/index.yaml, is a
// generated copy that stays only so releases before the cutover keep
// working; scripts/models/cmd/modelcatalog writes it from the library and it
// is never edited by hand.
//
// An entry is the shared library envelope (catalog.Envelope) plus a body that
// holds what a gallery entry holds beyond the header. Converting a library
// entry to a gallery entry and back gives the same entry, apart from the
// header fields a gallery entry has no place for (see GalleryHeaderLoss). The
// tests check that on every entry of the real library.

const (
	// MaxModelIndexBytes and MaxModelIndexEntries bound one model library
	// document. The model gallery is much larger than an add-on index.
	MaxModelIndexBytes   = 32 << 20
	MaxModelIndexEntries = 20000
	// modelLibraryChannel is the channel the generated entries are on.
	modelLibraryChannel = "gallery"
)

// ModelBody is the kind-specific part of a model library entry.
type ModelBody struct {
	// URL is the config file the model installs from, as in the gallery.
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
	// Backend is the resolved backend engine, when the entry says it.
	Backend string `json:"backend,omitempty" yaml:"backend,omitempty"`
	// Size is an optional size string such as "14.5GB".
	Size  string `json:"size,omitempty" yaml:"size,omitempty"`
	Files []File `json:"files,omitempty" yaml:"files,omitempty"`
	// ConfigFile is the base config of an entry that has no URL.
	ConfigFile map[string]any `json:"configFile,omitempty" yaml:"configFile,omitempty"`
	// Overrides override the config the URL names.
	Overrides map[string]any `json:"overrides,omitempty" yaml:"overrides,omitempty"`
	// Variants name other entries that are alternative builds of this one.
	Variants variantList `json:"variants,omitempty" yaml:"variants,omitempty"`
	// PromptTemplates are the prompt templates of the base config. The gallery
	// carries them inside config_file as prompt_templates; the library lifts
	// them out so a reader can show them without reading the whole config.
	PromptTemplates []PromptTemplate `json:"promptTemplates,omitempty" yaml:"promptTemplates,omitempty"`
}

// variantList keeps an empty list apart from an absent one: 194 gallery entries
// write "variants: []", and the entry must come back as it was written.
type variantList []Variant

// IsZero makes omitempty drop only a list that is absent.
func (v variantList) IsZero() bool { return v == nil }

// ModelLibraryEntry is one entry of the model index.
type ModelLibraryEntry struct {
	catalog.Envelope `json:",inline" yaml:",inline"`
	Model            ModelBody `json:"model" yaml:"model"`
}

const promptTemplatesKey = "prompt_templates"

// ModelLibraryEntryFromGallery converts a gallery entry to a library entry.
// The runtime fields of the gallery entry, the gallery it came from and its
// installed flag, are not part of the entry and are dropped.
func ModelLibraryEntryFromGallery(m GalleryModel) (ModelLibraryEntry, error) {
	if m.Name == "" {
		return ModelLibraryEntry{}, errors.New("gallery entry has no name")
	}
	m = stripRuntime(m)
	digest, err := galleryEntryDigest(m)
	if err != nil {
		return ModelLibraryEntry{}, err
	}
	body := ModelBody{
		URL:        m.URL,
		Backend:    m.Backend,
		Size:       m.Size,
		Files:      m.AdditionalFiles,
		ConfigFile: m.ConfigFile,
		Overrides:  m.Overrides,
		Variants:   variantList(m.Variants),
	}
	body.ConfigFile, body.PromptTemplates = liftPromptTemplates(m.ConfigFile)
	return ModelLibraryEntry{
		Envelope: catalog.Envelope{
			Kind:          catalog.KindModel,
			ID:            m.Name,
			Name:          m.Name,
			Version:       "0.0.0-gallery." + digest[:12],
			Description:   m.Description,
			License:       m.License,
			URLs:          m.URLs,
			Tags:          m.Tags,
			Icon:          m.Icon,
			Channel:       modelLibraryChannel,
			SchemaVersion: catalog.SchemaVersion,
		},
		Model: body,
	}, nil
}

// ToGallery converts a library entry to the gallery entry the installer
// reads. The id of the entry is the name the gallery knows the model by; the
// header name is for display and is not read back.
func (e ModelLibraryEntry) ToGallery() (GalleryModel, error) {
	if err := e.validate(); err != nil {
		return GalleryModel{}, err
	}
	b := e.Model
	return GalleryModel{
		Metadata: Metadata{
			URL:             b.URL,
			Name:            e.ID,
			Description:     e.Description,
			License:         e.License,
			URLs:            e.URLs,
			Icon:            e.Icon,
			Tags:            e.Tags,
			AdditionalFiles: b.Files,
			Size:            b.Size,
			Backend:         b.Backend,
		},
		ConfigFile: restorePromptTemplates(b.ConfigFile, b.PromptTemplates),
		Overrides:  b.Overrides,
		Variants:   []Variant(b.Variants),
	}, nil
}

func (e ModelLibraryEntry) validate() error {
	if e.Kind != catalog.KindModel {
		return fmt.Errorf("entry %q is of kind %q, not model", e.ID, e.Kind)
	}
	if e.ID == "" || len(e.ID) > 256 || strings.ContainsAny(e.ID, "\r\n\x00") {
		return fmt.Errorf("model entry has a malformed id %q", e.ID)
	}
	if e.SchemaVersion < 1 || e.SchemaVersion > catalog.SchemaVersion {
		return fmt.Errorf("model entry %q has schemaVersion %d; this host reads up to %d", e.ID, e.SchemaVersion, catalog.SchemaVersion)
	}
	return nil
}

func stripRuntime(m GalleryModel) GalleryModel {
	m.Gallery = GalleryModel{}.Gallery
	m.Installed = false
	return m
}

// galleryEntryDigest identifies the content of a gallery entry. Maps are
// written with sorted keys, so the digest does not depend on their order.
func galleryEntryDigest(m GalleryModel) (string, error) {
	b, err := yaml.Marshal(m)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// liftPromptTemplates moves prompt_templates out of a base config when every
// template is a plain name and content pair. A config that holds anything else
// under that key is left as it is, so nothing is reshaped on the way.
func liftPromptTemplates(cfg map[string]any) (map[string]any, []PromptTemplate) {
	raw, ok := cfg[promptTemplatesKey]
	if !ok {
		return cfg, nil
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return cfg, nil
	}
	out := make([]PromptTemplate, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok || len(m) != 2 {
			return cfg, nil
		}
		name, okName := m["name"].(string)
		content, okContent := m["content"].(string)
		if !okName || !okContent {
			return cfg, nil
		}
		out = append(out, PromptTemplate{Name: name, Content: content})
	}
	rest := make(map[string]any, len(cfg)-1)
	for k, v := range cfg {
		if k != promptTemplatesKey {
			rest[k] = v
		}
	}
	if len(rest) == 0 {
		rest = nil
	}
	return rest, out
}

func restorePromptTemplates(cfg map[string]any, templates []PromptTemplate) map[string]any {
	if len(templates) == 0 {
		return cfg
	}
	out := make(map[string]any, len(cfg)+1)
	for k, v := range cfg {
		out[k] = v
	}
	list := make([]any, 0, len(templates))
	for _, t := range templates {
		list = append(list, map[string]any{"name": t.Name, "content": t.Content})
	}
	out[promptTemplatesKey] = list
	return out
}

// ModelIndexResult is what a reader made of a model index: the entries it
// understands and a record of those it skipped.
type ModelIndexResult struct {
	Entries []ModelLibraryEntry
	Skipped []catalog.SkippedEntry
}

// DecodeModelIndex reads a model index: one YAML block list of entries. It is
// as strict as the add-on reader: an unknown field, a second document, a
// duplicate key or id, a value of the wrong type or a document over the size
// bounds is refused. An entry of a kind other than model is skipped without
// being read further and is reported in the result, so a shared file can hold
// other kinds later.
func DecodeModelIndex(data []byte) (ModelIndexResult, error) {
	if len(data) > MaxModelIndexBytes {
		return ModelIndexResult{}, errors.New("model index is too large")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return ModelIndexResult{}, err
	}
	if err := dec.Decode(new(yaml.Node)); err != io.EOF {
		return ModelIndexResult{}, errors.New("model index holds more than one document")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.SequenceNode {
		return ModelIndexResult{}, errors.New("model index must be a list of entries")
	}
	list := doc.Content[0]
	if list.Style&yaml.FlowStyle != 0 && len(list.Content) > 0 {
		return ModelIndexResult{}, errors.New("model index must be a YAML block list, not JSON")
	}
	if len(list.Content) > MaxModelIndexEntries {
		return ModelIndexResult{}, errors.New("model index has too many entries")
	}
	res := ModelIndexResult{}
	seen := make(map[string]struct{}, len(list.Content))
	for i, node := range list.Content {
		if node.Kind != yaml.MappingNode {
			return ModelIndexResult{}, errors.New("model index entry must be a mapping")
		}
		kind, id, err := peekKind(node)
		if err != nil {
			return ModelIndexResult{}, err
		}
		if kind != catalog.KindModel {
			res.Skipped = append(res.Skipped, catalog.SkippedEntry{Position: i, Kind: string(kind), ID: id})
			continue
		}
		raw, err := yaml.Marshal(node)
		if err != nil {
			return ModelIndexResult{}, err
		}
		strict := yaml.NewDecoder(bytes.NewReader(raw))
		strict.KnownFields(true)
		var entry ModelLibraryEntry
		if err := strict.Decode(&entry); err != nil {
			return ModelIndexResult{}, fmt.Errorf("model index entry %d: %w", i, err)
		}
		if err := entry.validate(); err != nil {
			return ModelIndexResult{}, err
		}
		if _, dup := seen[entry.ID]; dup {
			return ModelIndexResult{}, fmt.Errorf("model index lists %q twice", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		res.Entries = append(res.Entries, entry)
	}
	return res, nil
}

// peekKind reads the kind of an entry without reading the rest. An entry
// without a kind is an add-on, as in the add-on reader.
func peekKind(node *yaml.Node) (catalog.Kind, string, error) {
	kind, id := catalog.KindAddon, ""
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "kind":
			if val.Kind != yaml.ScalarNode || val.Tag != "!!str" || !catalog.ValidKindName(val.Value) {
				return "", "", errors.New("model index entry has a malformed kind")
			}
			kind = catalog.Kind(val.Value)
		case "id":
			if val.Kind == yaml.ScalarNode && val.Tag == "!!str" {
				id = val.Value
			}
		}
	}
	return kind, id, nil
}

// floatValue writes a float so that it reads back as a float. The YAML encoder
// writes 1.0 as 1, which reads back as an integer and would change the type of
// a number in an override.
type floatValue float64

func (f floatValue) MarshalYAML() (any, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v, nil
	}
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: s}, nil
}

// markFloats copies a config value and wraps its floats in floatValue.
func markFloats(v any) any {
	switch t := v.(type) {
	case float64:
		return floatValue(t)
	case map[string]any:
		if t == nil {
			return t
		}
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = markFloats(x)
		}
		return out
	case map[any]any:
		if t == nil {
			return t
		}
		out := make(map[any]any, len(t))
		for k, x := range t {
			out[k] = markFloats(x)
		}
		return out
	case []any:
		if t == nil {
			return t
		}
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = markFloats(x)
		}
		return out
	}
	return v
}

func markFloatMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return markFloats(m).(map[string]any)
}

// EncodeModelIndex writes entries as a model index.
func EncodeModelIndex(entries []ModelLibraryEntry) ([]byte, error) {
	marked := make([]ModelLibraryEntry, len(entries))
	for i, e := range entries {
		e.Model.ConfigFile = markFloatMap(e.Model.ConfigFile)
		e.Model.Overrides = markFloatMap(e.Model.Overrides)
		marked[i] = e
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(marked); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// galleryFileEntry is a gallery entry as it is written to the gallery file.
// It has the fields and the order of GalleryModel, but keeps an empty variants
// list apart from an absent one, as the gallery file did.
type galleryFileEntry struct {
	Metadata   `yaml:",inline"`
	ConfigFile map[string]any `yaml:"config_file,omitempty"`
	Overrides  map[string]any `yaml:"overrides,omitempty"`
	Variants   variantList    `yaml:"variants,omitempty"`
}

// EncodeGalleryIndex writes gallery entries as the gallery file the host
// reads (gallery/index.yaml). The runtime fields of an entry are not written
// and a float keeps its form, so the file reads back as the entries it was
// written from.
func EncodeGalleryIndex(models []GalleryModel) ([]byte, error) {
	out := make([]galleryFileEntry, len(models))
	for i, m := range models {
		m = stripRuntime(m)
		out[i] = galleryFileEntry{
			Metadata:   m.Metadata,
			ConfigFile: markFloatMap(m.ConfigFile),
			Overrides:  markFloatMap(m.Overrides),
			Variants:   variantList(m.Variants),
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GalleryHeaderLoss names the header fields of a library entry that the
// gallery copy cannot carry and that the entry sets on purpose: a display name
// that differs from the id, and a signature policy. The version, the channel
// and the schema version are never carried; the host does not read any of
// them from a gallery.
func GalleryHeaderLoss(e ModelLibraryEntry) []string {
	var lost []string
	if e.Name != "" && e.Name != e.ID {
		lost = append(lost, "name")
	}
	if !reflect.DeepEqual(e.Policy, catalog.VerificationPolicy{}) {
		lost = append(lost, "policy")
	}
	return lost
}

// ModelsFromLibrary converts the entries of a model index to gallery entries,
// in index order.
func ModelsFromLibrary(entries []ModelLibraryEntry) ([]*GalleryModel, error) {
	out := make([]*GalleryModel, 0, len(entries))
	for _, e := range entries {
		m, err := e.ToGallery()
		if err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, nil
}

// VerifyModelRoundTrip checks that a gallery entry survives the library: it is
// converted to a library entry, written as an index, read back and converted
// to a gallery entry again, and the result must equal the original. It returns
// a description of the first difference.
func VerifyModelRoundTrip(m GalleryModel) error {
	want := stripRuntime(m)
	entry, err := ModelLibraryEntryFromGallery(m)
	if err != nil {
		return err
	}
	raw, err := EncodeModelIndex([]ModelLibraryEntry{entry})
	if err != nil {
		return fmt.Errorf("writing the library entry: %w", err)
	}
	res, err := DecodeModelIndex(raw)
	if err != nil {
		return fmt.Errorf("reading the library entry: %w", err)
	}
	if len(res.Entries) != 1 {
		return fmt.Errorf("the library entry came back as %d entries", len(res.Entries))
	}
	got, err := res.Entries[0].ToGallery()
	if err != nil {
		return err
	}
	if reflect.DeepEqual(got, want) {
		return nil
	}
	return fmt.Errorf("gallery entry changed on the way through the library: %s", firstDifference(want, got))
}

// firstDifference names the first top-level field of two gallery entries that
// differ.
func firstDifference(a, b GalleryModel) string {
	av, bv := reflect.ValueOf(a.Metadata), reflect.ValueOf(b.Metadata)
	for i := 0; i < av.NumField(); i++ {
		if !reflect.DeepEqual(av.Field(i).Interface(), bv.Field(i).Interface()) {
			return fmt.Sprintf("%s: %#v became %#v", av.Type().Field(i).Name, av.Field(i).Interface(), bv.Field(i).Interface())
		}
	}
	switch {
	case !reflect.DeepEqual(a.ConfigFile, b.ConfigFile):
		return fmt.Sprintf("ConfigFile: %#v became %#v", a.ConfigFile, b.ConfigFile)
	case !reflect.DeepEqual(a.Overrides, b.Overrides):
		return fmt.Sprintf("Overrides: %#v became %#v", a.Overrides, b.Overrides)
	case !reflect.DeepEqual(a.Variants, b.Variants):
		return fmt.Sprintf("Variants: %#v became %#v", a.Variants, b.Variants)
	}
	return "unknown field"
}
