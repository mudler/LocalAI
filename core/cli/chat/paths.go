package chat

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// stateDirMode matches the mode nib uses for the same directory. The directory
// holds an API key, so it stays owner-only.
const stateDirMode = 0o700

// configFileMode keeps the config owner-only: nib stores the user's API key in
// it alongside the keys written here.
const configFileMode = 0o600

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
//
// The config file is machine-managed from here on: nib rewrites it whenever it
// self-configures, so hand-written comments in it do not survive.
func EnsureStateDir(dir, baseURL string) error {
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
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
	if err := writeConfigFile(path, data); err != nil {
		return fmt.Errorf("writing seed agent config: %w", err)
	}
	return nil
}

// PersistModel records the chosen model in the agent config, preserving every
// other key the user may have set, including the api_key nib writes there.
//
// The file is machine-managed: this overlays the model onto the parsed keys and
// re-marshals, which drops comments. That is deliberate rather than an
// oversight, because nib's own save path does the same thing and would erase
// them on its next write regardless.
func PersistModel(dir, model string) error {
	// PersistModel is callable before EnsureStateDir, so it cannot assume the
	// directory exists.
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return fmt.Errorf("creating agent state dir %s: %w", dir, err)
	}
	path := ConfigPath(dir)

	values := map[string]any{}
	// #nosec G304 -- path is the fixed config.yaml name under the user-selected
	// chat state directory; selecting that directory is the documented override.
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
	if err := writeConfigFile(path, out); err != nil {
		return fmt.Errorf("writing agent config: %w", err)
	}
	return nil
}

// writeConfigFile replaces path with data atomically: it writes a temporary
// file next to the target and renames it over the target. Writing the target in
// place would truncate it first, so an interrupted or out-of-disk write would
// leave a half-written config and destroy the api_key nib keeps in the same
// file. The temporary file must share the directory because rename is only
// atomic within one filesystem.
func writeConfigFile(path string, data []byte) error {
	dir := filepath.Dir(path)

	// A randomized name rather than a fixed config.yaml.tmp, so two concurrent
	// writers cannot corrupt each other's temporary file.
	tmp, err := os.CreateTemp(dir, "config.yaml.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			// Leave no litter behind on any failure path.
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpPath, err)
	}
	// Flush before the rename: renaming a file whose contents are still only in
	// the page cache can still lose them across a crash.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpPath, err)
	}
	// CreateTemp already asks for 0600, but the umask can only ever clear bits,
	// so set the mode explicitly rather than inheriting whatever survived.
	if err := os.Chmod(tmpPath, configFileMode); err != nil {
		return fmt.Errorf("setting mode on %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	renamed = true
	return nil
}
