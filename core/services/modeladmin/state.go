package modeladmin

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/pkg/utils"
)

// ToggleResult is shared by ToggleState and TogglePinned.
type ToggleResult struct {
	Filename       string
	Action         Action
	ConfigRevision string
	PendingCleanup int
}

// ToggleState enables or disables an installed model. action must be
// ActionEnable or ActionDisable. The revision lifecycle quarantines existing
// replicas before cleanup when the state changes.
//
// The on-disk YAML is mutated as a generic map so unrelated fields are
// preserved verbatim; we only set or remove the `disabled` key.
func (s *ConfigService) ToggleState(ctx context.Context, name string, action Action) (*ToggleResult, error) {
	var result *ToggleResult
	err := s.Loader.WithModelConfigMutation(func() error {
		var err error
		result, err = s.toggleState(ctx, name, action)
		return err
	})
	return result, err
}

func (s *ConfigService) toggleState(ctx context.Context, name string, action Action) (*ToggleResult, error) {
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
	var result *ToggleResult
	err := s.withMutationRollback([]string{configPath}, func() error {
		if err := mutateYAMLBoolFlag(configPath, "disabled", action == ActionDisable); err != nil {
			return err
		}
		if err := s.Loader.LoadModelConfigsFromPath(s.modelsPath(), s.AppConfig.ToConfigLoaderOptions()...); err != nil {
			return fmt.Errorf("reload configs: %w", err)
		}
		if _, ok := s.Loader.GetModelConfig(name); !ok {
			return fmt.Errorf("reload configs: model %q missing", name)
		}
		// Resolve the revision the way an inference request does. Hashing the
		// stored config instead publishes a value no request will ever carry,
		// because SetDefaults runs again on the request path and is not
		// idempotent for every model, and the edit would leave the model
		// unroutable.
		revision, err := s.Loader.RevisionFor(name, s.AppConfig)
		if err != nil {
			return err
		}
		pending, err := s.applyRevision(ctx, name, name, revision, action == ActionDisable)
		if err != nil {
			return err
		}
		result = &ToggleResult{Filename: configPath, Action: action, ConfigRevision: revision, PendingCleanup: pending}
		return nil
	})
	return result, err
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
