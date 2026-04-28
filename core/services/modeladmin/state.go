package modeladmin

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

// ToggleResult is shared by ToggleState and TogglePinned.
type ToggleResult struct {
	Filename string
	Action   Action
}

// ToggleState enables or disables an installed model. action must be
// ActionEnable or ActionDisable. When ml is non-nil and the action is
// ActionDisable, ToggleState calls ml.ShutdownModel — best-effort.
//
// The on-disk YAML is mutated as a generic map so unrelated fields are
// preserved verbatim; we only set or remove the `disabled` key.
func (s *ConfigService) ToggleState(_ context.Context, name string, action Action, ml *model.ModelLoader) (*ToggleResult, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if !action.Valid(ActionEnable, ActionDisable) {
		return nil, fmt.Errorf("%w: must be %q or %q, got %q", ErrBadAction, ActionEnable, ActionDisable, action)
	}
	cfg, exists := s.Loader.GetModelConfig(name)
	if !exists {
		return nil, ErrNotFound
	}
	configPath := cfg.GetModelConfigFile()
	if configPath == "" {
		return nil, ErrConfigFileMissing
	}
	if err := utils.VerifyPath(configPath, s.modelsPath()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
	}
	if err := mutateYAMLBoolFlag(configPath, "disabled", action == ActionDisable); err != nil {
		return nil, err
	}
	if err := s.Loader.LoadModelConfigsFromPath(s.modelsPath(), s.AppConfig.ToConfigLoaderOptions()...); err != nil {
		return nil, fmt.Errorf("reload configs: %w", err)
	}
	if action == ActionDisable && ml != nil {
		// Best-effort: the YAML is saved; shutdown is a courtesy.
		_ = ml.ShutdownModel(name)
	}
	return &ToggleResult{Filename: configPath, Action: action}, nil
}

// mutateYAMLBoolFlag is a small helper shared by ToggleState and
// TogglePinned: read the file as a generic map, set or remove a bool key,
// write back. Setting `set=false` removes the key for a clean YAML.
func mutateYAMLBoolFlag(path, key string, set bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	if set {
		m[key] = true
	} else {
		delete(m, key)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := writeFileAtomic(path, out, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
