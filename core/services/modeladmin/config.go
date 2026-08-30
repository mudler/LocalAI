package modeladmin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/meta"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// ModelRevisionLifecycle applies a persisted model configuration generation to
// the distributed registry. Implementations quarantine stale replicas before
// attempting cleanup.
type ModelRevisionLifecycle interface {
	ApplyConfigRevisions(ctx context.Context, transitions []ModelRevisionTransition) (pendingCleanup int, err error)
}

// ModelRevisionTransition describes one authoritative model identity. Related
// identities, such as both sides of a rename, are published atomically.
type ModelRevisionTransition = config.ModelConfigRevisionTransition

// ConfigService groups operations that read or mutate an installed model's
// configuration on disk. It keeps the side-effect surface (loader reload,
// model shutdown) explicit so callers know what gets touched.
type ConfigService struct {
	Loader    *config.ModelConfigLoader
	AppConfig *config.ApplicationConfig
	Lifecycle ModelRevisionLifecycle
}

// NewConfigService returns a ConfigService bound to the supplied loader and
// app config. The loader and the system state in AppConfig are mandatory.
func NewConfigService(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, lifecycle ...ModelRevisionLifecycle) *ConfigService {
	svc := &ConfigService{Loader: loader, AppConfig: appConfig}
	if len(lifecycle) > 0 {
		svc.Lifecycle = lifecycle[0]
	}
	return svc
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
	Filename       string
	Renamed        bool
	OldName        string
	NewName        string
	Config         config.ModelConfig
	ConfigRevision string
	PendingCleanup int
}

type PatchResult struct {
	config.ModelConfig
	ConfigRevision string
	PendingCleanup int
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
func (s *ConfigService) PatchConfig(ctx context.Context, name string, patch map[string]any) (*PatchResult, error) {
	var result *PatchResult
	err := s.Loader.WithModelConfigMutation(func() error {
		var err error
		result, err = s.patchConfig(ctx, name, patch)
		return err
	})
	return result, err
}

func (s *ConfigService) patchConfig(ctx context.Context, name string, patch map[string]any) (*PatchResult, error) {
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
	if patchedName, ok := patch["name"].(string); ok && patchedName != name {
		return nil, fmt.Errorf("%w: PATCH cannot rename model %q to %q; use the model edit endpoint", ErrInvalidConfig, name, patchedName)
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
	patchMerge(existingMap, patch, mapLeafFieldPaths(), "")
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
	if err := s.Loader.ValidateAliasTarget(&updated); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	var result *PatchResult
	err = s.withMutationRollback([]string{configPath}, func() error {
		if err := writeFileAtomic(configPath, yamlData, 0644); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
		if err := s.Loader.LoadModelConfigsFromPath(s.modelsPath(), s.AppConfig.ToConfigLoaderOptions()...); err != nil {
			return fmt.Errorf("reload configs: %w", err)
		}
		if _, ok := s.Loader.GetModelConfig(updated.Name); !ok {
			return fmt.Errorf("reload configs: model %q missing", updated.Name)
		}
		// Resolve the revision the way an inference request does. Hashing the
		// stored config instead publishes a value no request will ever carry,
		// because SetDefaults runs again on the request path and is not
		// idempotent for every model, and the edit would leave the model
		// unroutable.
		revision, err := s.Loader.RevisionFor(updated.Name, s.AppConfig)
		if err != nil {
			return err
		}
		_ = s.Loader.Preload(s.modelsPath())
		pending, err := s.applyRevision(ctx, name, updated.Name, revision, updated.IsDisabled())
		if err != nil {
			return err
		}
		result = &PatchResult{ModelConfig: updated, ConfigRevision: revision, PendingCleanup: pending}
		return nil
	})
	return result, err
}

// mapLeafFieldPaths returns the set of dotted config paths whose schema type is
// a map that the editor edits as one complete value (e.g.
// pii_detection.entity_actions, roles, engine_args). A PATCH must REPLACE these
// wholesale rather than union them: the deep-merge only adds and overrides
// keys, so a map entry the admin deleted in the editor would otherwise silently
// survive. Derived from the config schema so it stays correct as map fields are
// added. (UIType comes from reflection, independent of any registry override.)
func mapLeafFieldPaths() map[string]struct{} {
	md := meta.BuildConfigMetadata(reflect.TypeFor[config.ModelConfig]())
	out := make(map[string]struct{})
	for _, f := range md.Fields {
		if f.UIType == "map" {
			out[f.Path] = struct{}{}
		}
	}
	return out
}

// patchMerge deep-merges src into dst with the same shape as the previous
// mergo.WithOverride behaviour — scalars and slices replace; nested
// struct-maps (e.g. pii_detection, parameters) recurse so unknown sibling keys
// the editor doesn't model survive — EXCEPT that any path in mapLeaves is
// replaced wholesale, and removed when the patch sets it empty, so deletions
// inside a map field persist to disk.
func patchMerge(dst, src map[string]any, mapLeaves map[string]struct{}, prefix string) {
	for k, sv := range src {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if _, isLeaf := mapLeaves[path]; isLeaf {
			if m, ok := sv.(map[string]any); ok && len(m) == 0 {
				delete(dst, k) // emptied map field -> drop it from the YAML
			} else {
				dst[k] = sv
			}
			continue
		}
		// Recurse into struct-like nesting so dst-only sibling keys survive.
		if sm, ok := sv.(map[string]any); ok {
			if dm, ok2 := dst[k].(map[string]any); ok2 {
				patchMerge(dm, sm, mapLeaves, path)
				continue
			}
		}
		dst[k] = sv
	}
}

// EditYAML replaces the YAML for an installed model, with optional rename
// support, and applies the resulting semantic revision after reload.
func (s *ConfigService) EditYAML(ctx context.Context, name string, body []byte) (*EditResult, error) {
	var result *EditResult
	err := s.Loader.WithModelConfigMutation(func() error {
		var err error
		result, err = s.editYAML(ctx, name, body)
		return err
	})
	return result, err
}

func (s *ConfigService) editYAML(ctx context.Context, name string, body []byte) (*EditResult, error) {
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
	if err := s.Loader.ValidateAliasTarget(&req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	configPath := existing.GetModelConfigFile()
	modelsPath := s.modelsPath()
	if err := utils.VerifyPath(configPath, modelsPath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
	}

	renamed := req.Name != name
	paths := []string{configPath}
	if renamed {
		if strings.ContainsRune(req.Name, os.PathSeparator) || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
			return nil, ErrPathSeparator
		}
		if _, exists := s.Loader.GetModelConfig(req.Name); exists {
			return nil, fmt.Errorf("%w: %q", ErrConflict, req.Name)
		}
		newConfigPath := filepath.Join(modelsPath, req.Name+".yaml")
		paths = append(paths, newConfigPath, filepath.Join(modelsPath, gallery.GalleryFileName(name)), filepath.Join(modelsPath, gallery.GalleryFileName(req.Name)))
		if err := utils.VerifyPath(newConfigPath, modelsPath); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPathNotTrusted, err)
		}
		if _, err := os.Stat(newConfigPath); err == nil {
			return nil, fmt.Errorf("%w: a config file for %q already exists on disk", ErrConflict, req.Name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat new config: %w", err)
		}
	}

	var result *EditResult
	err := s.withMutationRollback(paths, func() error {
		if renamed {
			newConfigPath := filepath.Join(modelsPath, req.Name+".yaml")
			if err := writeFileAtomic(newConfigPath, body, 0644); err != nil {
				return fmt.Errorf("write new config: %w", err)
			}
			if configPath != newConfigPath {
				if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("remove old config: %w", err)
				}
			}
			// Move the gallery metadata file so the delete flow can still find it.
			oldGalleryPath := filepath.Join(modelsPath, gallery.GalleryFileName(name))
			newGalleryPath := filepath.Join(modelsPath, gallery.GalleryFileName(req.Name))
			if _, err := os.Stat(oldGalleryPath); err == nil {
				if err := os.Rename(oldGalleryPath, newGalleryPath); err != nil {
					return fmt.Errorf("rename gallery metadata: %w", err)
				}
			}
			// Drop the stale in-memory entry before reload so we don't surface
			// both names between scan steps.
			s.Loader.RemoveModelConfig(name)
			configPath = newConfigPath
		} else {
			if err := writeFileAtomic(configPath, body, 0644); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
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
		if err := s.Loader.Preload(modelsPath); err != nil {
			return fmt.Errorf("preload after edit: %w", err)
		}
		pending, err := s.applyRevision(ctx, name, req.Name, revision, req.IsDisabled())
		if err != nil {
			return err
		}
		result = &EditResult{
			Filename:       configPath,
			Renamed:        renamed,
			OldName:        name,
			NewName:        req.Name,
			Config:         req,
			ConfigRevision: revision,
			PendingCleanup: pending,
		}
		return nil
	})
	return result, err
}

func (s *ConfigService) applyRevision(ctx context.Context, oldName, newName, revision string, disabled bool) (int, error) {
	if s.Lifecycle == nil {
		return 0, nil
	}
	transitions := []ModelRevisionTransition{{ModelName: newName, ConfigRevision: revision, Disabled: disabled}}
	if oldName != newName {
		transitions = []ModelRevisionTransition{
			{ModelName: oldName, ConfigRevision: DeletedModelConfigRevision(oldName), Disabled: true},
			{ModelName: newName, ConfigRevision: revision, Disabled: disabled},
		}
	}
	pending, err := s.Lifecycle.ApplyConfigRevisions(ctx, transitions)
	if err != nil {
		return pending, fmt.Errorf("apply config revision: %w", err)
	}
	if pending > 0 {
		xlog.Warn("Model configuration saved with cleanup pending", "model", newName, "configRevision", revision, "pendingCleanup", pending)
	}
	return pending, nil
}
