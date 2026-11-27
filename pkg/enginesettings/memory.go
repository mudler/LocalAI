package enginesettings

import (
	"errors"
	"fmt"
)

// MemoryKind says how the device that runs a model reaches its memory. The
// right batch, KV cache type, slot count and load mode differ between them,
// so the quick setup of an engine can give different values for each.
type MemoryKind string

const (
	// MemoryDiscrete is a GPU with its own memory (VRAM). The weights, the KV
	// cache and the compute buffers compete for it; the prompt cache and the
	// context checkpoints stay in system memory.
	MemoryDiscrete MemoryKind = "discrete"
	// MemoryUnified is a GPU that shares the system memory: GB10 (DGX Spark),
	// Jetson and Thor, Apple silicon, AMD APUs such as Strix Halo, Intel
	// integrated GPUs. One pool holds everything, the host included.
	MemoryUnified MemoryKind = "unified"
	// MemorySystem is no GPU: the model runs on the CPU in system memory.
	MemorySystem MemoryKind = "system"
)

// MemoryKinds lists every kind, in the order a client offers them.
var MemoryKinds = []MemoryKind{MemoryUnified, MemoryDiscrete, MemorySystem}

// ProfileSetConcurrency is the Set of the concurrency profiles: how many
// requests the model serves at once. They are independent of the profiles of
// the default set (batch sizes), so a client shows them as a second choice.
const ProfileSetConcurrency = "concurrency"

// PresetMemory is the part of a profile that depends on the memory kind.
type PresetMemory struct {
	// Settings are merged over the profile's own settings for this kind. A
	// nil value unsets the key.
	Settings map[string]any `json:"settings,omitempty"`
	// Tight is merged over the result when the memory estimate of the
	// profile does not fit the device (Memory.FitFraction of it), for example
	// an 8-bit KV cache when the f16 one would not fit. Optional.
	Tight map[string]any `json:"tight,omitempty"`
	// MinMemory is the smallest device memory, in bytes, the profile is
	// offered for. 0: any.
	MinMemory uint64 `json:"min_memory,omitempty"`
	// Unsupported: the profile is not offered for this kind; Note says why.
	Unsupported bool `json:"unsupported,omitempty"`
	// Note is the trade-off of the profile on this kind. Optional.
	Note string `json:"note,omitempty"`
}

// Memory is the memory-aware part of an engine: what each memory kind means
// and the advice for controls that are not part of a profile. The values of
// each kind live in the profiles (Preset.Memory).
type Memory struct {
	// Fallback is the kind a client assumes when the host is unknown. The
	// discrete rules are the cautious ones.
	Fallback MemoryKind `json:"fallback"`
	// FitFraction is the share of the device memory a profile's estimate may
	// take and still count as fitting. The rest is for the system, other
	// models and estimate error.
	FitFraction float64 `json:"fit_fraction"`
	// Kinds says, for each kind, what it is and what the rules do there.
	Kinds []MemoryKindInfo `json:"kinds"`
	// Advice lists the recommended value of a control per kind.
	Advice []Advice `json:"advice,omitempty"`
}

// MemoryKindInfo describes one memory kind to a person.
type MemoryKindInfo struct {
	Kind  MemoryKind `json:"kind"`
	Label string     `json:"label"`
	Doc   string     `json:"doc"`
}

// Advice is the recommended value of a control on one memory kind. A client
// shows Doc when the control holds one of the Avoid values, with a way to
// set Value.
type Advice struct {
	ID string `json:"id"`
	// Control is a Combined id or a flag key.
	Control string     `json:"control"`
	Kind    MemoryKind `json:"kind"`
	// Value is the recommended choice (Combined) or value (flag).
	Value string   `json:"value"`
	Avoid []string `json:"avoid"`
	Doc   string   `json:"doc"`
}

func knownKind(k MemoryKind) bool {
	for _, x := range MemoryKinds {
		if x == k {
			return true
		}
	}
	return false
}

// SettingsFor returns the settings of a profile on a memory kind: its own
// settings with the kind's merged over them, and with Tight merged too when
// tight is true. The maps of the profile are not changed.
func (p Preset) SettingsFor(kind MemoryKind, tight bool) map[string]any {
	out := make(map[string]any, len(p.Settings))
	for k, v := range p.Settings {
		out[k] = v
	}
	m, ok := p.Memory[kind]
	if !ok {
		return out
	}
	for k, v := range m.Settings {
		out[k] = v
	}
	if tight {
		for k, v := range m.Tight {
			out[k] = v
		}
	}
	return out
}

// validateMemory checks the memory part of the engine and of its profiles.
func (e *Engine) validateMemory(keys map[string]bool) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	checkSettings := func(owner string, set map[string]any) {
		for k, v := range set {
			f := e.flag(k)
			if f == nil {
				bad("engine %s: %s sets the unknown key %s", e.ID, owner, k)
				continue
			}
			if v == nil {
				continue
			}
			if _, err := NormalizeValue(f, fmt.Sprint(v)); err != nil {
				bad("engine %s: %s: %s: %v", e.ID, owner, k, err)
			}
		}
	}
	for _, p := range e.Presets {
		if len(p.Memory) > 0 && !p.Profile {
			bad("engine %s: preset %s has memory rules but is not a profile", e.ID, p.ID)
		}
		if (p.Set != "" || p.Streams > 0) && !p.Profile {
			bad("engine %s: preset %s has a set or streams but is not a profile", e.ID, p.ID)
		}
		if len(p.Memory) > 0 && e.Memory == nil {
			bad("engine %s: profile %s has memory rules but the engine has no memory description", e.ID, p.ID)
		}
		for kind, m := range p.Memory {
			if !knownKind(kind) {
				bad("engine %s: profile %s names the unknown memory kind %q", e.ID, p.ID, kind)
			}
			if m.Unsupported && m.Note == "" {
				bad("engine %s: profile %s is unsupported on %s without a note", e.ID, p.ID, kind)
			}
			checkSettings(fmt.Sprintf("profile %s (%s)", p.ID, kind), m.Settings)
			checkSettings(fmt.Sprintf("profile %s (%s, tight)", p.ID, kind), m.Tight)
		}
	}
	mem := e.Memory
	if mem == nil {
		return errors.Join(errs...)
	}
	if !knownKind(mem.Fallback) {
		bad("engine %s: the memory fallback %q is not a memory kind", e.ID, mem.Fallback)
	}
	if mem.FitFraction <= 0 || mem.FitFraction > 1 {
		bad("engine %s: the memory fit fraction %v is not in (0, 1]", e.ID, mem.FitFraction)
	}
	seen := map[MemoryKind]bool{}
	for _, k := range mem.Kinds {
		if !knownKind(k.Kind) || seen[k.Kind] || k.Label == "" || k.Doc == "" {
			bad("engine %s: memory kind %q needs to be new and known, with a label and a doc", e.ID, k.Kind)
		}
		seen[k.Kind] = true
	}
	for _, k := range MemoryKinds {
		if !seen[k] {
			bad("engine %s: the memory kind %s is not described", e.ID, k)
		}
	}
	combined := map[string]Combined{}
	for _, c := range e.Combined {
		combined[c.ID] = c
	}
	ids := map[string]bool{}
	for _, a := range mem.Advice {
		if a.ID == "" || ids[a.ID] || a.Doc == "" || a.Value == "" || len(a.Avoid) == 0 || !knownKind(a.Kind) {
			bad("engine %s: advice %q needs a new id, a known kind, a value, values to avoid and a doc", e.ID, a.ID)
		}
		ids[a.ID] = true
		c, isCombined := combined[a.Control]
		switch {
		case isCombined:
			values := map[string]bool{}
			for _, ch := range c.Choices {
				values[ch.Value] = true
			}
			for _, v := range append([]string{a.Value}, a.Avoid...) {
				if !values[v] {
					bad("engine %s: advice %s names %q, which is not a choice of %s", e.ID, a.ID, v, a.Control)
				}
			}
		case keys[a.Control]:
			f := e.flag(a.Control)
			for _, v := range append([]string{a.Value}, a.Avoid...) {
				if _, err := NormalizeValue(f, v); err != nil {
					bad("engine %s: advice %s: %s: %v", e.ID, a.ID, a.Control, err)
				}
			}
		default:
			bad("engine %s: advice %s names the unknown control %s", e.ID, a.ID, a.Control)
		}
	}
	return errors.Join(errs...)
}
