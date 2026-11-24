package enginesettings

import (
	"fmt"
	"strings"
	"sync"
)

// BackendMatch says which model backend names an engine serves. A name is
// compared in lower case, with dots read as hyphens ("llama.cpp" is
// "llama-cpp") and without a release-channel suffix ("-development",
// "-quantization").
type BackendMatch struct {
	// Names match exactly.
	Names []string `json:"names,omitempty"`
	// Families match exactly and as the last part of a hardware variant:
	// family "llama-cpp" matches "llama-cpp" and "cuda12-llama-cpp".
	Families []string `json:"families,omitempty"`
	// Excluded names never match, also as the last part of a variant. They
	// are other engines whose names end like a family.
	Excluded []string `json:"excluded,omitempty"`
	// Empty: a model with no backend (auto-detect) resolves to this engine.
	Empty bool `json:"empty,omitempty"`
}

var channelSuffixes = []string{"-development", "-quantization"}

// NormalizeBackend is the form of a backend name that BackendMatch compares.
func NormalizeBackend(backend string) string {
	name := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(backend)), ".", "-")
	for _, s := range channelSuffixes {
		name = strings.TrimSuffix(name, s)
	}
	return name
}

// Matches reports whether a model backend name is one of the engine's.
func (m BackendMatch) Matches(backend string) bool {
	name := NormalizeBackend(backend)
	if name == "" {
		return m.Empty
	}
	for _, x := range m.Excluded {
		if name == x || strings.HasSuffix(name, "-"+x) {
			return false
		}
	}
	for _, n := range m.Names {
		if name == n {
			return true
		}
	}
	for _, f := range m.Families {
		if name == f || strings.HasSuffix(name, "-"+f) {
			return true
		}
	}
	return false
}

// MatchesBackend reports whether the engine serves a model backend name.
func (e *Engine) MatchesBackend(backend string) bool { return e.Backends.Matches(backend) }

// Info is the summary of an engine that GET /api/engines lists.
type Info struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Binary       string       `json:"binary,omitempty"`
	Backends     BackendMatch `json:"backends"`
	Capabilities []Capability `json:"capabilities"`
}

// Info returns the summary of the engine.
func (e *Engine) Info() Info {
	in := Info{ID: e.ID, Name: e.Name, Backends: e.Backends, Capabilities: e.Capabilities()}
	if e.Command != nil {
		in.Binary = e.Command.Binary
	}
	return in
}

// PlacementSchema describes the tensor-placement capability to a client.
type PlacementSchema struct {
	// Option is the options name of a placement entry; Aliases are other
	// names the backend reads for the same entry.
	Option  string          `json:"option"`
	Aliases []string        `json:"aliases,omitempty"`
	Group   Group           `json:"group"`
	Modes   []PlacementMode `json:"modes"`
}

// Schema is everything a client needs to draw the settings of an engine.
type Schema struct {
	Info
	// Example is a sample command for the paste box (command-line only).
	Example         string           `json:"example,omitempty"`
	Flags           []Flag           `json:"flags"`
	Groups          []Group          `json:"groups"`
	Combined        []Combined       `json:"combined"`
	Presets         []Preset         `json:"presets"`
	ReservedOptions []string         `json:"reserved_options"`
	Placement       *PlacementSchema `json:"placement,omitempty"`
}

// Schema returns the settings schema of the engine.
func (e *Engine) Schema() Schema {
	s := Schema{
		Info:            e.Info(),
		Flags:           e.Flags,
		Groups:          e.Groups,
		Combined:        e.Combined,
		Presets:         e.Presets,
		ReservedOptions: e.ReservedOptions,
	}
	if s.Combined == nil {
		s.Combined = []Combined{}
	}
	if s.Presets == nil {
		s.Presets = []Preset{}
	}
	if s.ReservedOptions == nil {
		s.ReservedOptions = []string{}
	}
	if e.Command != nil {
		s.Example = e.Command.Example
	}
	if pl := e.Placement; pl != nil {
		ps := &PlacementSchema{Option: pl.Option, Group: pl.Group, Modes: pl.Modes}
		for _, f := range e.Flags {
			if f.Option == pl.Option {
				ps.Aliases = f.OptionAliases
			}
		}
		s.Placement = ps
	}
	return s
}

// Registry holds the engines LocalAI knows. It is filled at start-up and
// only read after that.
type Registry struct {
	mu      sync.RWMutex
	engines []*Engine
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register validates an engine and adds it. The id must be new.
func (r *Registry) Register(e *Engine) error {
	if err := e.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.engines {
		if x.ID == e.ID {
			return fmt.Errorf("the engine %s is already registered", e.ID)
		}
	}
	r.engines = append(r.engines, e)
	return nil
}

// Get returns the engine with an id.
func (r *Registry) Get(id string) (*Engine, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.engines {
		if e.ID == id {
			return e, true
		}
	}
	return nil, false
}

// ForBackend returns the first registered engine that serves a model
// backend name.
func (r *Registry) ForBackend(backend string) (*Engine, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.engines {
		if e.MatchesBackend(backend) {
			return e, true
		}
	}
	return nil, false
}

// All returns the engines in the order they were registered.
func (r *Registry) All() []*Engine {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*Engine(nil), r.engines...)
}
