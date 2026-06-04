package pii

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// FileConfig is the on-disk schema for pii.yaml. Each Pattern entry
// overrides the matching default by ID; missing fields fall back to
// the default. Unknown IDs are rejected at load time so an admin who
// fat-fingers a pattern name gets a clear error rather than a silent
// no-op.
type FileConfig struct {
	Patterns []FilePattern `yaml:"patterns"`
}

type FilePattern struct {
	ID     string `yaml:"id"`
	Action Action `yaml:"action"`
}

// LoadConfig reads pii.yaml from path and merges it on top of
// DefaultPatterns(). path == "" returns the defaults compiled and
// ready. The returned slice is already Compile()'d, so callers can
// pass it straight to NewRedactor.
func LoadConfig(path string) ([]Pattern, error) {
	defaults := DefaultPatterns()
	if path == "" {
		return Compile(defaults)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pii: read config %q: %w", path, err)
	}
	var cfg FileConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("pii: parse config %q: %w", path, err)
	}

	overrides := make(map[string]Action, len(cfg.Patterns))
	known := make(map[string]bool, len(defaults))
	for _, d := range defaults {
		known[d.ID] = true
	}
	for _, p := range cfg.Patterns {
		if !known[p.ID] {
			return nil, fmt.Errorf("pii: unknown pattern id %q in %q", p.ID, path)
		}
		if p.Action == "" {
			continue
		}
		switch p.Action {
		case ActionMask, ActionBlock, ActionRouteLocal:
			overrides[p.ID] = p.Action
		default:
			return nil, fmt.Errorf("pii: invalid action %q for pattern %q", p.Action, p.ID)
		}
	}

	merged := make([]Pattern, len(defaults))
	for i, d := range defaults {
		if a, ok := overrides[d.ID]; ok {
			d.Action = a
		}
		merged[i] = d
	}
	return Compile(merged)
}
