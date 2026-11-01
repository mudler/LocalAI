// SPDX-License-Identifier: MIT
package agents

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

type CollectionGrant struct {
	Collection    string `json:"collection"`
	Search        bool   `json:"search"`
	DeleteEntries bool   `json:"delete_entries"`
}

type CollectionPermissions struct {
	Version int               `json:"version"`
	Grants  []CollectionGrant `json:"grants"`
}

type MemorySettings struct {
	Version            int    `json:"version"`
	AddMemory          bool   `json:"add_memory"`
	Destination        string `json:"destination"`
	ToolStorage        string `json:"tool_storage"`
	AutomaticIngestion string `json:"automatic_ingestion"`
}

// strictPolicy rejects duplicate keys as well as unknown fields, including in grants.
func strictPolicy(raw []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func() error
	value = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if t == nil {
			return ErrInvalid
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return err
				}
				key := k.(string)
				switch key {
				case "version", "grants", "collection", "search", "delete_entries", "add_memory", "destination", "tool_storage", "automatic_ingestion":
				default:
					return ErrInvalid
				}
				if seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if err := value(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if len(raw) == 0 || raw[0] != '{' {
		return ErrInvalid
	}
	if err := value(); err != nil {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return ErrInvalid
	}
	return nil
}
func (p *CollectionPermissions) UnmarshalJSON(raw []byte) error {
	type wire CollectionPermissions
	var v wire
	if err := strictPolicy(bytes.TrimSpace(raw), &v); err != nil {
		return err
	}
	if err := validPermissions(CollectionPermissions(v)); err != nil {
		return err
	}
	*p = CollectionPermissions(v)
	return nil
}
func (m *MemorySettings) UnmarshalJSON(raw []byte) error {
	type wire MemorySettings
	v := wire{ToolStorage: "content", AutomaticIngestion: "off"}
	if err := strictPolicy(bytes.TrimSpace(raw), &v); err != nil {
		return err
	}
	if err := validMemory(MemorySettings(v)); err != nil {
		return err
	}
	*m = MemorySettings(v)
	return nil
}
func validPermissions(p CollectionPermissions) error {
	if p.Version != 1 || len(p.Grants) > MaxListEntries {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, g := range p.Grants {
		if strings.TrimSpace(g.Collection) == "" || utf8.RuneCountInString(g.Collection) > MaxEntryRunes || seen[g.Collection] {
			return ErrInvalid
		}
		seen[g.Collection] = true
	}
	return nil
}
func validMemory(m MemorySettings) error {
	if m.Version != 1 {
		return ErrInvalid
	}
	switch m.ToolStorage {
	case "content", "summary":
	default:
		return ErrInvalid
	}
	switch m.AutomaticIngestion {
	case "off", "incoming_only", "incoming_and_replies", "summary":
	default:
		return ErrInvalid
	}
	return nil
}

// NormalizeMemory migrates names-only definitions without retaining hidden mutation
// grants. A supplied versioned policy is authoritative, never merged with old grants.
func NormalizeMemory(a Agent) (Agent, error) {
	a = a.clone()
	if a.CollectionPermissions == nil {
		a.CollectionPermissions = &CollectionPermissions{Version: 1, Grants: []CollectionGrant{}}
		for _, name := range a.Collections {
			a.CollectionPermissions.Grants = append(a.CollectionPermissions.Grants, CollectionGrant{Collection: name, Search: true})
		}
		// Names alone can never carry mutation settings.
		if a.Memory != nil && (a.Memory.AddMemory || (a.Memory.AutomaticIngestion != "" && a.Memory.AutomaticIngestion != "off")) {
			return Agent{}, ErrInvalid
		}
	}
	if err := validPermissions(*a.CollectionPermissions); err != nil {
		return Agent{}, err
	}
	projection := []string{}
	associated := false
	for _, grant := range a.CollectionPermissions.Grants {
		if grant.Search {
			projection = append(projection, grant.Collection)
		}
		if a.Memory != nil && grant.Collection == a.Memory.Destination {
			associated = true
		}
	}
	if a.Collections != nil && !slices.Equal(a.Collections, projection) {
		return Agent{}, ErrInvalid
	}
	a.Collections = projection
	if a.Memory == nil {
		a.Memory = &MemorySettings{Version: 1, ToolStorage: "content", AutomaticIngestion: "off"}
	}
	if err := validMemory(*a.Memory); err != nil {
		return Agent{}, err
	}
	if a.Memory.Destination != "" && !associated {
		return Agent{}, ErrInvalid
	}
	if (a.Memory.AddMemory || a.Memory.AutomaticIngestion != "off") && (a.Memory.Destination == "" || !associated) {
		return Agent{}, ErrInvalid
	}
	return a, nil
}
