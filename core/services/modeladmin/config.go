package modeladmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dario.cat/mergo"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
)

// ConfigService groups operations that read or mutate an installed model's
// configuration on disk. It keeps the side-effect surface (loader reload,
// model shutdown) explicit so callers know what gets touched.
type ConfigService struct {
	Loader    *config.ModelConfigLoader
	AppConfig *config.ApplicationConfig
}

// NewConfigService returns a ConfigService bound to the supplied loader and
// app config. The loader and the system state in AppConfig are mandatory; the
// model loader is required only by EditYAML and ToggleState (for Shutdown).
func NewConfigService(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig) *ConfigService {
	return &ConfigService{Loader: loader, AppConfig: appConfig}
}

// ConfigView is the on-disk YAML plus the parsed JSON view, returned by GetConfig.
// The YAML is read from disk (not serialised from the in-memory loader) so
// callers see exactly what the user wrote — no SetDefaults() noise.
type ConfigView struct {
	Name string
	YAML string
	JSON map[string]any
}

// EditResult is what EditYAML returns to its caller.
type EditResult struct {
	Filename string
	Renamed  bool
	OldName  string
	NewName  string
	Config   config.ModelConfig
}

// modelsPath is shorthand for the configured models directory.
func (s *ConfigService) modelsPath() string {
	return s.AppConfig.SystemState.Model.ModelsPath
}

// GetConfig reads the YAML for an installed model from disk and returns it
// alongside the parsed JSON view.
func (s *ConfigService) GetConfig(_ context.Context, name string) (*ConfigView, error) {
	if name == "" {
		return nil, ErrNameRequired
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
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var jsonView map[string]any
	_ = yaml.Unmarshal(data, &jsonView)
	return &ConfigView{Name: name, YAML: string(data), JSON: jsonView}, nil
}

// PatchConfig applies a JSON deep-merge to an installed model's YAML and
// reloads. Returns the merged config that's now in the loader.
//
// Mirrors PatchConfigEndpoint: read raw YAML from disk (not the in-memory
// config — which has SetDefaults applied and would persist runtime defaults
// like top_p/temperature/mirostat), deep-merge the patch, validate, write,
// reload, preload (preload errors are non-fatal — log only).
func (s *ConfigService) PatchConfig(_ context.Context, name string, patch map[string]any) (*config.ModelConfig, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if len(patch) == 0 {
		return nil, ErrEmptyBody
	}
	cfg, exists := s.Loader.GetModelConfig(name)
	if !exists {
		return nil, ErrNotFound
	}
	configPath := cfg.GetModelConfigFile()
	if err := utils.VerifyPath(configPath, s.modelsPath()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
	}
	diskYAML, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var existingMap map[string]any
	if err := yaml.Unmarshal(diskYAML, &existingMap); err != nil {
		return nil, fmt.Errorf("parse existing config: %w", err)
	}
	if existingMap == nil {
		existingMap = map[string]any{}
	}
	if err := mergo.Merge(&existingMap, patch, mergo.WithOverride); err != nil {
		return nil, fmt.Errorf("merge configs: %w", err)
	}
	yamlData, err := yaml.Marshal(existingMap)
	if err != nil {
		return nil, fmt.Errorf("marshal merged YAML: %w", err)
	}
	var updated config.ModelConfig
	if err := yaml.Unmarshal(yamlData, &updated); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if valid, vErr := updated.Validate(); !valid {
		if vErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, vErr)
		}
		return nil, ErrInvalidConfig
	}
	if err := writeFileAtomic(configPath, yamlData, 0644); err != nil {
		return nil, fmt.Errorf("write config file: %w", err)
	}
	if err := s.Loader.LoadModelConfigsFromPath(s.modelsPath(), s.AppConfig.ToConfigLoaderOptions()...); err != nil {
		return nil, fmt.Errorf("reload configs: %w", err)
	}
	// Preload is best-effort — a failure here doesn't undo the patch.
	_ = s.Loader.Preload(s.modelsPath())
	return &updated, nil
}

// EditYAML replaces the YAML for an installed model, with optional rename
// support. ml may be nil; when set, EditYAML calls ml.ShutdownModel(oldName)
// after a successful write so the next inference picks up the new config.
func (s *ConfigService) EditYAML(_ context.Context, name string, body []byte, ml *model.ModelLoader) (*EditResult, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if len(body) == 0 {
		return nil, ErrEmptyBody
	}
	existing, exists := s.Loader.GetModelConfig(name)
	if !exists {
		return nil, ErrNotFound
	}

	var req config.ModelConfig
	if err := yaml.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name field is required", ErrInvalidConfig)
	}
	if valid, _ := req.Validate(); !valid {
		return nil, ErrInvalidConfig
	}

	configPath := existing.GetModelConfigFile()
	modelsPath := s.modelsPath()
	if err := utils.VerifyPath(configPath, modelsPath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
	}

	renamed := req.Name != name
	if renamed {
		if strings.ContainsRune(req.Name, os.PathSeparator) || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
			return nil, ErrPathSeparator
		}
		if _, exists := s.Loader.GetModelConfig(req.Name); exists {
			return nil, fmt.Errorf("%w: %q", ErrConflict, req.Name)
		}
		newConfigPath := filepath.Join(modelsPath, req.Name+".yaml")
		if err := utils.VerifyPath(newConfigPath, modelsPath); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
		}
		if _, err := os.Stat(newConfigPath); err == nil {
			return nil, fmt.Errorf("%w: a config file for %q already exists on disk", ErrConflict, req.Name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat new config: %w", err)
		}
		if err := writeFileAtomic(newConfigPath, body, 0644); err != nil {
			return nil, fmt.Errorf("write new config: %w", err)
		}
		if configPath != newConfigPath {
			// Best-effort: a stale old file is cosmetic, not load-bearing.
			_ = os.Remove(configPath)
		}
		// Move the gallery metadata file so the delete flow can still find it.
		oldGalleryPath := filepath.Join(modelsPath, gallery.GalleryFileName(name))
		newGalleryPath := filepath.Join(modelsPath, gallery.GalleryFileName(req.Name))
		if _, err := os.Stat(oldGalleryPath); err == nil {
			_ = os.Rename(oldGalleryPath, newGalleryPath)
		}
		// Drop the stale in-memory entry before reload so we don't surface
		// both names between scan steps.
		s.Loader.RemoveModelConfig(name)
		configPath = newConfigPath
	} else {
		if err := writeFileAtomic(configPath, body, 0644); err != nil {
			return nil, fmt.Errorf("write config: %w", err)
		}
	}

	if err := s.Loader.LoadModelConfigsFromPath(modelsPath, s.AppConfig.ToConfigLoaderOptions()...); err != nil {
		return nil, fmt.Errorf("reload configs: %w", err)
	}
	// Best-effort shutdown: the config is already written; if shutdown fails
	// the caller can manually reload. The shutdown uses the OLD name because
	// that's what the running instance was started with.
	if ml != nil {
		_ = ml.ShutdownModel(name)
	}
	if err := s.Loader.Preload(modelsPath); err != nil {
		return nil, fmt.Errorf("preload after edit: %w", err)
	}
	return &EditResult{
		Filename: configPath,
		Renamed:  renamed,
		OldName:  name,
		NewName:  req.Name,
		Config:   req,
	}, nil
}
