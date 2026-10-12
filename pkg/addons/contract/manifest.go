// SPDX-License-Identifier: MIT
// Package contract defines version-one addon manifests and runtime wire DTOs.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

const SchemaVersion = 1

type VersionRange struct {
	Min uint64 `json:"min"`
	Max uint64 `json:"max"`
}

func (r VersionRange) accepts(v uint64) bool {
	return r.Min > 0 && r.Max >= r.Min && v >= r.Min && v <= r.Max
}

type HostCompatibility struct {
	Host       uint64 `json:"host"`
	API        uint64 `json:"api"`
	Protocol   uint64 `json:"protocol"`
	SDK        uint64 `json:"sdk"`
	DataFormat uint64 `json:"dataFormat"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}
type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	ID            string       `json:"id"`
	Version       string       `json:"version"`
	Executable    string       `json:"executable"`
	OS            string       `json:"os"`
	Arch          string       `json:"arch"`
	Host          VersionRange `json:"host"`
	API           VersionRange `json:"api"`
	Protocol      VersionRange `json:"protocol"`
	DataFormat    VersionRange `json:"dataFormat"`
	Capabilities  Capabilities `json:"capabilities"`
}
type Capabilities struct {
	Service bool     `json:"service,omitempty"`
	Tools   []string `json:"tools,omitempty"`
	UI      *UI      `json:"ui,omitempty"`
}
type UI struct {
	Entry       string       `json:"entry"`
	CSS         []string     `json:"css,omitempty"`
	SDK         VersionRange `json:"sdk"`
	Pages       []string     `json:"pages,omitempty"`
	Navigation  []string     `json:"navigation,omitempty"`
	ChatActions []string     `json:"chatActions,omitempty"`
}

var namespace = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var immutableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-z][a-z0-9-]*(\.[a-z0-9-]+)*)?$`)
var assetPath = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)

func relativePath(s string) bool {
	return s != "" && s != "." && s != ".." && assetPath.MatchString(s) && !strings.HasPrefix(s, "/") && path.Clean(s) == s && !strings.HasPrefix(s, "../")
}
func uniqueIDs(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if !namespace.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := strictJSON(data, &m, DefaultLimits().ManifestBytes); err != nil {
		return m, err
	}
	// Parsing validates intrinsic constraints without imposing this machine's platform.
	h := HostCompatibility{Host: m.Host.Min, API: m.API.Min, Protocol: m.Protocol.Min, DataFormat: m.DataFormat.Min, OS: m.OS, Arch: m.Arch}
	if m.Capabilities.UI != nil {
		h.SDK = m.Capabilities.UI.SDK.Min
	}
	return m, ValidateManifest(m, h)
}
func ValidateManifest(m Manifest, h HostCompatibility) error {
	if m.SchemaVersion != SchemaVersion || !namespace.MatchString(m.ID) || !immutableVersion.MatchString(m.Version) || !relativePath(m.Executable) {
		return fmt.Errorf("invalid manifest identity, version or executable")
	}
	if !namespace.MatchString(m.OS) || !namespace.MatchString(m.Arch) || m.OS != h.OS || m.Arch != h.Arch {
		return fmt.Errorf("incompatible platform")
	}
	if !m.Host.accepts(h.Host) || !m.API.accepts(h.API) || !m.Protocol.accepts(h.Protocol) || !m.DataFormat.accepts(h.DataFormat) {
		return fmt.Errorf("incompatible host, API, protocol or data format")
	}
	c := m.Capabilities
	if (!c.Service && len(c.Tools) == 0 && c.UI == nil) || !uniqueIDs(c.Tools) {
		return fmt.Errorf("invalid capabilities")
	}
	if u := c.UI; u != nil {
		if !relativePath(u.Entry) || !u.SDK.accepts(h.SDK) || !uniqueIDs(u.Pages) || !uniqueIDs(u.Navigation) || !uniqueIDs(u.ChatActions) {
			return fmt.Errorf("invalid or incompatible UI")
		}
		seen := map[string]bool{u.Entry: true}
		for _, css := range u.CSS {
			if !relativePath(css) || seen[css] {
				return fmt.Errorf("invalid UI asset")
			}
			seen[css] = true
		}
		pages := map[string]bool{}
		for _, p := range u.Pages {
			pages[p] = true
		}
		for _, n := range u.Navigation {
			if !pages[n] {
				return fmt.Errorf("navigation requires declared page")
			}
		}
	}
	return nil
}

// strictJSON rejects duplicate keys as well as unknown fields. The token walk
// also bounds nesting before the standard decoder materializes wire values.
func strictJSON(b []byte, v any, max int64) error {
	if int64(len(b)) > max || len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return fmt.Errorf("invalid or oversized JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := walkJSON(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func walkJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return e
			}
			k, ok := key.(string)
			if !ok || seen[k] {
				return fmt.Errorf("duplicate JSON key")
			}
			seen[k] = true
			if e = walkJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := walkJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("invalid delimiter")
	}
	_, err = d.Token()
	return err
}
