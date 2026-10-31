package egress

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// registriesJSON is the list of package registries that the preset
// "registries" admits. It ships with LocalAI and changes by a reviewed pull
// request that also updates the docs table; nothing updates it at run time.
//
//go:embed registries.json
var registriesJSON []byte

// Registry is one package registry: the exact HTTPS hosts its tools read from.
type Registry struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Hosts []string `json:"hosts"`
}

// Registries is the registries list.
type Registries struct {
	Version int        `json:"version"`
	List    []Registry `json:"registries"`
	hosts   map[string]bool
}

// ParseRegistries reads a registries file.
func ParseRegistries(raw []byte) (*Registries, error) {
	var r Registries
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("egress: the registries list: %w", err)
	}
	r.hosts = map[string]bool{}
	for _, e := range r.List {
		if e.ID == "" || e.Title == "" || len(e.Hosts) == 0 {
			return nil, fmt.Errorf("egress: the registries list: entry %q is incomplete", e.ID)
		}
		for _, h := range e.Hosts {
			if h != strings.ToLower(h) || strings.ContainsAny(h, "*/: ") {
				return nil, fmt.Errorf("egress: the registries list: %q is not an exact host name", h)
			}
			r.hosts[h] = true
		}
	}
	return &r, nil
}

var builtin = func() *Registries {
	r, err := ParseRegistries(registriesJSON)
	if err != nil {
		panic(err)
	}
	return r
}()

// BuiltinRegistries returns the list that ships with LocalAI.
func BuiltinRegistries() *Registries { return builtin }

// Has says whether host is a registry host, of the list or of extra (the
// administrator's chats_egress_extra_registries).
func (r *Registries) Has(host string, extra []string) bool {
	if r != nil && r.hosts[host] {
		return true
	}
	return slices.Contains(extra, host)
}

// Titles returns the titles of the registries, in the order of the list.
func (r *Registries) Titles() []string {
	out := make([]string, 0, len(r.List))
	for _, e := range r.List {
		out = append(out, e.Title)
	}
	return out
}
