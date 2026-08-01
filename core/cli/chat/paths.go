package chat

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// StateDir resolves where the chat agent keeps its config, plugins, and
// skills. This is user-scoped rather than server-scoped: chat is a client that
// may target a remote LocalAI, so it does not belong under LOCALAI_CONFIG_DIR.
func StateDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "localai", "chat"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory for the agent state dir: %w", err)
	}
	return filepath.Join(home, ".config", "localai", "chat"), nil
}

// ConfigPath is the agent's config file inside dir.
func ConfigPath(dir string) string { return filepath.Join(dir, "config.yaml") }

// EnsureStateDir creates dir and, on first run only, seeds a config file
// pointing at baseURL. It deliberately does not seed a model: a baked-in model
// name goes stale as soon as the user installs a different one.
func EnsureStateDir(dir, baseURL string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating agent state dir %s: %w", dir, err)
	}
	path := ConfigPath(dir)
	if _, err := os.Stat(path); err == nil {
		return nil // already configured; never overwrite the user's file
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking agent config %s: %w", path, err)
	}

	seed := map[string]string{"base_url": baseURL}
	data, err := yaml.Marshal(seed)
	if err != nil {
		return fmt.Errorf("encoding seed agent config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing seed agent config %s: %w", path, err)
	}
	return nil
}

// PersistModel records the chosen model in the agent config, preserving every
// other key the user may have set.
func PersistModel(dir, model string) error {
	path := ConfigPath(dir)

	values := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading agent config %s: %w", path, err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("parsing agent config %s: %w", path, err)
		}
	}
	values["model"] = model

	out, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("encoding agent config: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("writing agent config %s: %w", path, err)
	}
	return nil
}
