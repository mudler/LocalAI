package modeladmin

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/pkg/utils"
)

// SyncPinnedFn lets the caller (HTTP handler or inproc client) propagate a
// pin/unpin to the watchdog without coupling this package to it.
type SyncPinnedFn func()

// TogglePinned pins or unpins a model. action must be ActionPin or
// ActionUnpin. syncPinned, if non-nil, is invoked after a successful
// reload so the watchdog can refresh its eviction-exempt set.
func (s *ConfigService) TogglePinned(_ context.Context, name string, action Action, syncPinned SyncPinnedFn) (*ToggleResult, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if !action.Valid(ActionPin, ActionUnpin) {
		return nil, fmt.Errorf("%w: must be %q or %q, got %q", ErrBadAction, ActionPin, ActionUnpin, action)
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
	if err := mutateYAMLBoolFlag(configPath, "pinned", action == ActionPin); err != nil {
		return nil, err
	}
	if err := s.Loader.LoadModelConfigsFromPath(s.modelsPath(), s.AppConfig.ToConfigLoaderOptions()...); err != nil {
		return nil, fmt.Errorf("reload configs: %w", err)
	}
	if syncPinned != nil {
		syncPinned()
	}
	return &ToggleResult{Filename: configPath, Action: action}, nil
}
