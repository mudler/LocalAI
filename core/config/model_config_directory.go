package config

import (
	"path/filepath"
)

// DefinedOutside reports whether this configuration was read from a file that
// is not directly inside dir, for example the list given with --config-file.
// A configuration with no recorded file counts as one from dir, so callers that
// replace dir-backed configurations keep the behaviour they had before.
func (c *ModelConfig) DefinedOutside(dir string) bool {
	if c.modelConfigFile == "" {
		return false
	}
	return !sameDir(filepath.Dir(c.modelConfigFile), dir)
}

func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}

// MergeDirectorySnapshot returns the configuration set that results from
// replacing every configuration in current that was read from dir with
// snapshot, a fresh parse of dir.
//
// A configuration defined outside dir is kept, and it wins over a snapshot
// entry with the same name, as it does at startup, where --config-file is read
// after the models directory. A parse of dir cannot see these files, so
// replacing the whole set with it would drop them.
func MergeDirectorySnapshot(current, snapshot []ModelConfig, dir string) []ModelConfig {
	outside := make(map[string]ModelConfig)
	for _, cfg := range current {
		if cfg.DefinedOutside(dir) {
			outside[cfg.Name] = cfg
		}
	}
	merged := make([]ModelConfig, 0, len(snapshot)+len(outside))
	for _, cfg := range snapshot {
		if _, shadowed := outside[cfg.Name]; !shadowed {
			merged = append(merged, cfg)
		}
	}
	for _, cfg := range outside {
		merged = append(merged, cfg)
	}
	return merged
}
