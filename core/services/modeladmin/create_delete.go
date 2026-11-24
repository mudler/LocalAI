package modeladmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v3"
)

// CreateConfig writes a new model configuration file, reloads the loader and
// applies the semantic revision, the same steps the import endpoint takes for
// a config that needs no download. It fails with ErrConflict when a model of
// that name exists. The caller tells peer replicas about the new model.
func (s *ConfigService) CreateConfig(ctx context.Context, body []byte) (*EditResult, error) {
	var result *EditResult
	err := s.Loader.WithModelConfigMutation(func() error {
		var err error
		result, err = s.createConfig(ctx, body)
		return err
	})
	return result, err
}

func (s *ConfigService) createConfig(ctx context.Context, body []byte) (*EditResult, error) {
	if len(body) == 0 {
		return nil, ErrEmptyBody
	}
	var req config.ModelConfig
	if err := yaml.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name field is required", ErrInvalidConfig)
	}
	if strings.ContainsRune(req.Name, os.PathSeparator) || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
		return nil, ErrPathSeparator
	}
	if valid, vErr := req.Validate(); !valid {
		if vErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, vErr)
		}
		return nil, ErrInvalidConfig
	}
	if err := s.Loader.ValidateAliasTarget(&req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := s.Loader.ValidateFailoverTargets(&req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	modelsPath := s.modelsPath()
	if err := utils.VerifyPath(req.Name+".yaml", modelsPath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
	}
	if _, exists := s.Loader.GetModelConfig(req.Name); exists {
		return nil, fmt.Errorf("%w: %q", ErrConflict, req.Name)
	}
	configPath := filepath.Join(modelsPath, req.Name+".yaml")
	if _, err := os.Stat(configPath); err == nil {
		return nil, fmt.Errorf("%w: a config file for %q already exists on disk", ErrConflict, req.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat new config: %w", err)
	}

	var result *EditResult
	err := s.withMutationRollback([]string{configPath}, func() error {
		if err := writeFileAtomic(configPath, body, 0644); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		if err := s.Loader.LoadModelConfigsFromPath(modelsPath, s.AppConfig.ToConfigLoaderOptions()...); err != nil {
			return fmt.Errorf("reload configs: %w", err)
		}
		if _, ok := s.Loader.GetModelConfig(req.Name); !ok {
			return fmt.Errorf("reload configs: model %q missing", req.Name)
		}
		revision, err := s.Loader.RevisionFor(req.Name, s.AppConfig)
		if err != nil {
			return err
		}
		pending, err := s.applyRevision(ctx, req.Name, req.Name, revision, req.IsDisabled())
		if err != nil {
			return err
		}
		result = &EditResult{
			Filename:       configPath,
			NewName:        req.Name,
			OldName:        req.Name,
			Config:         req,
			ConfigRevision: revision,
			PendingCleanup: pending,
		}
		return nil
	})
	return result, err
}

// DeleteResult is what DeleteConfig returns.
type DeleteResult struct {
	Filename string
	// ConfigRevision is the tombstone revision published for the name.
	ConfigRevision string
	PendingCleanup int
}

// DeleteConfig removes the configuration file of a model that owns no other
// files, drops it from the loader and publishes the deletion. It does not
// touch weights or other assets; use the gallery delete for a model that has
// them. The caller tells peer replicas about the deletion.
func (s *ConfigService) DeleteConfig(ctx context.Context, name string) (*DeleteResult, error) {
	var result *DeleteResult
	err := s.Loader.WithModelConfigMutation(func() error {
		var err error
		result, err = s.deleteConfig(ctx, name)
		return err
	})
	return result, err
}

func (s *ConfigService) deleteConfig(ctx context.Context, name string) (*DeleteResult, error) {
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
	if err := utils.VerifyResolvedPath(configPath, s.modelsPath()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
	}
	revision := DeletedModelConfigRevision(name)
	var result *DeleteResult
	err := s.withMutationRollback([]string{configPath}, func() error {
		if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove config: %w", err)
		}
		s.Loader.RemoveModelConfig(name)
		pending, err := s.applyRevision(ctx, name, name, revision, true)
		if err != nil {
			return err
		}
		result = &DeleteResult{Filename: configPath, ConfigRevision: revision, PendingCleanup: pending}
		return nil
	})
	return result, err
}
