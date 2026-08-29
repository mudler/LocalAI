package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/safefile"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// ArtifactMaterializer resolves and commits a model artifact into local storage.
type ArtifactMaterializer interface {
	Ensure(context.Context, string, modelartifacts.Spec) (modelartifacts.Result, error)
}

// ModelConfigLoaderOption customizes a ModelConfigLoader at construction time.
type ModelConfigLoaderOption func(*ModelConfigLoader)

// WithArtifactMaterializer sets the controller-side artifact acquisition boundary.
func WithArtifactMaterializer(materializer ArtifactMaterializer) ModelConfigLoaderOption {
	return func(loader *ModelConfigLoader) {
		if materializer != nil {
			loader.artifactMaterializer = materializer
		}
	}
}

// WithPreloadDisplay configures terminal rendering without reading process state.
func WithPreloadDisplay(renderMode string, disableColor bool) ModelConfigLoaderOption {
	return func(loader *ModelConfigLoader) {
		if renderMode != "" {
			loader.preloadRenderMode = renderMode
		}
		loader.disablePreloadColor = disableColor
	}
}

type ModelConfigLoader struct {
	configs              map[string]ModelConfig
	modelPath            string
	artifactMaterializer ArtifactMaterializer
	preloadRenderMode    string
	disablePreloadColor  bool
	mutationMu           sync.Mutex
	sync.Mutex
}

// WithModelConfigMutation serializes filesystem-backed configuration changes
// and their lifecycle publication for every service sharing this loader. It is
// deliberately separate from the loader's map lock: callbacks reload and
// replace loader state and would deadlock if that lock were held here.
func (bcl *ModelConfigLoader) WithModelConfigMutation(fn func() error) error {
	bcl.mutationMu.Lock()
	defer bcl.mutationMu.Unlock()
	return fn()
}

func NewModelConfigLoader(modelPath string, options ...ModelConfigLoaderOption) *ModelConfigLoader {
	loader := &ModelConfigLoader{
		configs:              make(map[string]ModelConfig),
		modelPath:            modelPath,
		artifactMaterializer: modelartifacts.NewDefaultManager(),
		preloadRenderMode:    "dark",
	}
	for _, option := range options {
		option(loader)
	}
	return loader
}

type LoadOptions struct {
	modelPath        string
	debug            bool
	threads, ctxSize int
	f16              bool
	galleryFiles     map[string]struct{}
}

func LoadOptionDebug(debug bool) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.debug = debug
	}
}

func LoadOptionThreads(threads int) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.threads = threads
	}
}

func LoadOptionContextSize(ctxSize int) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.ctxSize = ctxSize
	}
}

func ModelPath(modelPath string) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.modelPath = modelPath
	}
}

func LoadOptionF16(f16 bool) ConfigLoaderOption {
	return func(o *LoadOptions) {
		o.f16 = f16
	}
}

// LoadOptionGalleryFiles identifies local gallery sources that can legitimately
// live in the models directory. Exact paths provide provenance; document shape
// validation alone cannot distinguish an overrides-only gallery entry from a
// malformed runtime model configuration.
func LoadOptionGalleryFiles(galleries ...Gallery) ConfigLoaderOption {
	return func(o *LoadOptions) {
		if o.galleryFiles == nil {
			o.galleryFiles = map[string]struct{}{}
		}
		for _, configured := range galleries {
			for _, raw := range append([]string{configured.URL}, configured.Mirrors...) {
				parsed, err := url.Parse(raw)
				if err != nil || parsed.Scheme != "file" || parsed.Path == "" {
					continue
				}
				absolute, err := filepath.Abs(filepath.FromSlash(parsed.Path))
				if err == nil {
					o.galleryFiles[absolute] = struct{}{}
				}
			}
		}
	}
}

type ConfigLoaderOption func(*LoadOptions)

func (lo *LoadOptions) Apply(options ...ConfigLoaderOption) {
	for _, l := range options {
		l(lo)
	}
}

// readModelConfigsFromFile reads a config file that may contain either a single
// ModelConfig or an array of ModelConfigs. It tries to unmarshal as an array first,
// then falls back to a single config if that fails.
func readModelConfigsFromFile(file string, opts ...ConfigLoaderOption) ([]*ModelConfig, error) {
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("readModelConfigsFromFile cannot read config file %q: %w", file, err)
	}

	// Try to unmarshal as array first
	var configs []*ModelConfig
	if err := yaml.Unmarshal(f, &configs); err == nil && len(configs) > 0 {
		for _, cc := range configs {
			cc.modelConfigFile = file
			// Stamp before SetDefaults: the revision describes what is on disk.
			// SetDefaults folds in the GGUF guess, hardware defaults and
			// app-level options, none of which are persisted configuration, and
			// the GGUF guess in particular depends on whether the model file
			// parses at that moment.
			if err := cc.StampPersistedConfigRevision(); err != nil {
				return nil, fmt.Errorf("stamping config revision for %q: %w", cc.Name, err)
			}
			cc.SetDefaults(opts...)
			cc.syncKnownUsecasesFromString()
		}
		return configs, nil
	}

	// Fall back to single config
	c := &ModelConfig{}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("readModelConfigsFromFile cannot unmarshal config file %q: %w", file, err)
	}

	c.modelConfigFile = file
	c.syncKnownUsecasesFromString()
	if err := c.StampPersistedConfigRevision(); err != nil {
		return nil, fmt.Errorf("stamping config revision for %q: %w", c.Name, err)
	}
	c.SetDefaults(opts...)

	return []*ModelConfig{c}, nil
}

// Load a config file for a model
func (bcl *ModelConfigLoader) LoadModelConfigFileByName(modelName, modelPath string, opts ...ConfigLoaderOption) (*ModelConfig, error) {

	// Load a config file if present after the model name
	cfg := &ModelConfig{
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{
				Model: modelName,
			},
		},
	}

	cfgExisting, exists := bcl.GetModelConfig(modelName)
	if exists {
		cfg = &cfgExisting
	} else {
		// Try loading a model config file
		modelConfig := filepath.Join(modelPath, modelName+".yaml")
		if _, err := os.Stat(modelConfig); err == nil {
			if err := bcl.ReadModelConfig(
				modelConfig, opts...,
			); err != nil {
				return nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = bcl.GetModelConfig(modelName)
			if exists {
				cfg = &cfgExisting
			}
		}
	}

	// Stamp before SetDefaults, and only when this config did not come from
	// disk already carrying one (a name with no config file on disk is
	// synthesized above). Re-stamping a loaded config here would hash it after
	// SetDefaults and reintroduce the dependency on the GGUF guess.
	if cfg.PersistedConfigRevision() == "" {
		if err := cfg.StampPersistedConfigRevision(); err != nil {
			return nil, fmt.Errorf("stamping config revision for %q: %w", modelName, err)
		}
	}

	cfg.SetDefaults(append(opts, ModelPath(modelPath))...)

	return cfg, nil
}

func (bcl *ModelConfigLoader) LoadModelConfigFileByNameDefaultOptions(modelName string, appConfig *ApplicationConfig) (*ModelConfig, error) {
	return bcl.LoadModelConfigFileByName(modelName, appConfig.SystemState.Model.ModelsPath,
		LoadOptionDebug(appConfig.Debug),
		LoadOptionThreads(appConfig.Threads),
		LoadOptionContextSize(appConfig.ContextSize),
		LoadOptionF16(appConfig.F16),
		ModelPath(appConfig.SystemState.Model.ModelsPath))
}

// LoadResolvedModelConfig loads a model config by name and follows a single
// alias hop, so a caller that references an alias (e.g. a pipeline with
// `llm: default`) gets the alias target's full config (Backend, Model, ...)
// rather than the alias stub with an empty Backend. Without this the alias
// survives unresolved into model loading and fails downstream — notably in
// distributed mode with "backend name is empty". Mirrors the top-level alias
// resolution in core/http/middleware/request.go.
func (bcl *ModelConfigLoader) LoadResolvedModelConfig(modelName, modelPath string, opts ...ConfigLoaderOption) (*ModelConfig, error) {
	cfg, err := bcl.LoadModelConfigFileByName(modelName, modelPath, opts...)
	if err != nil {
		return nil, err
	}
	resolved, _, err := bcl.ResolveAlias(cfg)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

// This format is currently only used when reading a single file at startup, passed in via ApplicationConfig.ConfigFile
func (bcl *ModelConfigLoader) LoadMultipleModelConfigsSingleFile(file string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	c, err := readModelConfigsFromFile(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		if valid, err := cc.Validate(); valid {
			bcl.configs[cc.Name] = *cc
		} else {
			xlog.Warn("skipping invalid model config", "name", cc.Name, "error", err)
		}
	}
	return nil
}

func (bcl *ModelConfigLoader) ReadModelConfig(file string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	configs, err := readModelConfigsFromFile(file, opts...)
	if err != nil {
		return fmt.Errorf("ReadModelConfig cannot read config file %q: %w", file, err)
	}
	if len(configs) == 0 {
		return fmt.Errorf("ReadModelConfig: no configs found in file %q", file)
	}
	if len(configs) > 1 {
		xlog.Warn("ReadModelConig: read more than one config from file, only using first", "file", file, "configs", len(configs))
	}

	c := configs[0]
	if valid, err := c.Validate(); valid {
		bcl.configs[c.Name] = *c
	} else {
		if err != nil {
			return fmt.Errorf("model config %q is not valid: %w. Ensure the YAML file has a valid 'name' field and correct syntax. See https://localai.io/docs/getting-started/customize-model/ for config reference", file, err)
		}
		return fmt.Errorf("model config %q is not valid. Ensure the YAML file has a valid 'name' field and correct syntax. See https://localai.io/docs/getting-started/customize-model/ for config reference", file)
	}

	return nil
}

func (bcl *ModelConfigLoader) GetModelConfig(m string) (ModelConfig, bool) {
	bcl.Lock()
	defer bcl.Unlock()
	v, exists := bcl.configs[m]
	return v, exists
}

func (bcl *ModelConfigLoader) GetAllModelsConfigs() []ModelConfig {
	bcl.Lock()
	defer bcl.Unlock()
	var res []ModelConfig
	for _, v := range bcl.configs {
		res = append(res, v)
	}

	slices.SortStableFunc(res, func(a, b ModelConfig) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return res
}

func (bcl *ModelConfigLoader) GetModelConfigsByFilter(filter ModelConfigFilterFn) []ModelConfig {
	bcl.Lock()
	defer bcl.Unlock()
	var res []ModelConfig

	if filter == nil {
		filter = NoFilterFn
	}

	for n, v := range bcl.configs {
		if filter(n, &v) {
			res = append(res, v)
		}
	}

	// TODO: I don't think this one needs to Sort on name... but we'll see what breaks.

	return res
}

func (bcl *ModelConfigLoader) RemoveModelConfig(m string) {
	bcl.Lock()
	defer bcl.Unlock()
	delete(bcl.configs, m)
}

// ReplaceModelConfigs atomically replaces the in-memory configuration set with
// a previously parsed snapshot.
func (bcl *ModelConfigLoader) ReplaceModelConfigs(configs []ModelConfig) {
	bcl.Lock()
	defer bcl.Unlock()
	replacement := make(map[string]ModelConfig, len(configs))
	for _, cfg := range configs {
		replacement[cfg.Name] = cfg
	}
	bcl.configs = replacement
}

// GetModelsConflictingWith returns the names of every other configured (and
// not-disabled) model that shares at least one concurrency group with the
// named model. Returns nil if the named model has no groups, is unknown, or
// has no peers in any of its groups. The result excludes the queried name.
func (bcl *ModelConfigLoader) GetModelsConflictingWith(name string) []string {
	bcl.Lock()
	defer bcl.Unlock()
	target, ok := bcl.configs[name]
	if !ok {
		return nil
	}
	targetGroups := target.GetConcurrencyGroups()
	if len(targetGroups) == 0 {
		return nil
	}
	var conflicts []string
	for n, cfg := range bcl.configs {
		if n == name || cfg.IsDisabled() {
			continue
		}
		other := cfg.GetConcurrencyGroups()
		if len(other) == 0 {
			continue
		}
		for _, g := range targetGroups {
			if slices.Contains(other, g) {
				conflicts = append(conflicts, n)
				break
			}
		}
	}
	return conflicts
}

// UpdateModelConfig updates an existing model config in the loader.
// This is useful for updating runtime-detected properties like thinking support.
func (bcl *ModelConfigLoader) UpdateModelConfig(m string, updater func(*ModelConfig)) {
	bcl.Lock()
	defer bcl.Unlock()
	if cfg, exists := bcl.configs[m]; exists {
		updater(&cfg)
		bcl.configs[m] = cfg
	}
}

// ResolveAlias follows a one-hop alias to its target config. Returns
// (resolved, wasAlias, err). Non-alias configs return (cfg, false, nil)
// unchanged. Strict: the target must exist and must not itself be an alias
// (chains are rejected). The returned config is a copy of the target.
func (bcl *ModelConfigLoader) ResolveAlias(cfg *ModelConfig) (*ModelConfig, bool, error) {
	if cfg == nil || !cfg.IsAlias() {
		return cfg, false, nil
	}
	target, exists := bcl.GetModelConfig(cfg.Alias)
	if !exists {
		return nil, true, fmt.Errorf("alias %q points to unknown model %q", cfg.Name, cfg.Alias)
	}
	if target.IsAlias() {
		return nil, true, fmt.Errorf("alias %q points to another alias %q (chains are not allowed)", cfg.Name, cfg.Alias)
	}
	return &target, true, nil
}

// ResolveAliasName maps a model name to the name of the model that actually
// serves it: an alias resolves to its target, anything else resolves to
// itself. The second return reports whether name was an alias.
//
// Unlike ResolveAlias this never errors. A name with no config (a rule may be
// authored before the model is installed), a dangling alias, and a chained
// alias all resolve to themselves, so callers keep a usable name that simply
// has no model behind it rather than silently governing a different model.
func (bcl *ModelConfigLoader) ResolveAliasName(name string) (string, bool) {
	cfg, exists := bcl.GetModelConfig(name)
	if !exists || !cfg.IsAlias() {
		return name, false
	}
	target, exists := bcl.GetModelConfig(cfg.Alias)
	if !exists || target.IsAlias() {
		return name, true
	}
	return target.Name, true
}

// ValidateAliasTarget checks an alias config's target at create/swap time:
// the target must exist, must not be an alias, and must not be disabled.
// Returns nil for non-alias configs.
func (bcl *ModelConfigLoader) ValidateAliasTarget(cfg *ModelConfig) error {
	if cfg == nil || !cfg.IsAlias() {
		return nil
	}
	target, exists := bcl.GetModelConfig(cfg.Alias)
	if !exists {
		return fmt.Errorf("alias target %q does not exist", cfg.Alias)
	}
	if target.IsAlias() {
		return fmt.Errorf("alias target %q is itself an alias (chains are not allowed)", cfg.Alias)
	}
	if target.IsDisabled() {
		return fmt.Errorf("alias target %q is disabled", cfg.Alias)
	}
	return nil
}

type preloadWork struct {
	key    string
	config ModelConfig
}

// Preload prepares models if they are not local but URLs or Hugging Face repositories.
func (bcl *ModelConfigLoader) Preload(modelPath string) error {
	return bcl.PreloadWithContext(context.Background(), modelPath)
}

// PreloadWithContext prepares remote model inputs while honoring cancellation.
func (bcl *ModelConfigLoader) PreloadWithContext(ctx context.Context, modelPath string) error {
	bcl.Lock()
	work := make([]preloadWork, 0, len(bcl.configs))
	for key, config := range bcl.configs {
		configCopy := config
		configCopy.Artifacts = slices.Clone(config.Artifacts)
		configCopy.DownloadFiles = slices.Clone(config.DownloadFiles)
		work = append(work, preloadWork{key: key, config: configCopy})
	}
	bcl.Unlock()

	status := func(fileName, current, total string, percent float64) {
		utils.DisplayDownloadFunction(fileName, current, total, percent)
	}

	xlog.Info("Preloading models", "path", modelPath)
	for _, item := range work {
		if err := ctx.Err(); err != nil {
			return err
		}
		updated, artifactResult, err := bcl.preloadOne(ctx, modelPath, item.config, status)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		bcl.Lock()
		current, exists := bcl.configs[item.key]
		if !exists || !reflect.DeepEqual(current, item.config) {
			bcl.Unlock()
			continue
		}
		// Persist the WHOLE resolved artifact set (primary + every companion),
		// not just the primary result: writing back only the primary dropped
		// companions from disk and lost them on the next restart.
		if artifactResult != nil && bindingNeedsPersistence(current, updated.Artifacts) && current.modelConfigFile != "" {
			modelartifacts.ReportProgress(ctx, modelartifacts.ProgressEvent{
				Phase:    modelartifacts.PhasePersisting,
				Artifact: artifactResult.Spec.Name,
			})
			if err := persistArtifactBinding(current.modelConfigFile, current.Name, updated.Artifacts); err != nil {
				bcl.Unlock()
				return err
			}
		}
		bcl.configs[item.key] = updated
		bcl.Unlock()
		bcl.displayPreloadedModel(updated)
	}
	return nil
}

func (bcl *ModelConfigLoader) preloadOne(
	ctx context.Context,
	modelPath string,
	config ModelConfig,
	status func(string, string, string, float64),
) (ModelConfig, *modelartifacts.Result, error) {
	updated := config
	updated.Artifacts = slices.Clone(config.Artifacts)
	tasks := make([]downloader.FileTask, 0, len(updated.DownloadFiles))
	for index, file := range updated.DownloadFiles {
		if err := ctx.Err(); err != nil {
			return ModelConfig{}, nil, err
		}
		xlog.Debug("Checking file exists and matches SHA", "filename", file.Filename)
		if err := utils.VerifyPath(file.Filename, modelPath); err != nil {
			return ModelConfig{}, nil, err
		}
		tasks = append(tasks, downloader.FileTask{
			URI:         file.URI,
			Destination: filepath.Join(modelPath, file.Filename),
			SHA256:      file.SHA256,
			FileIndex:   index,
			TotalFiles:  len(updated.DownloadFiles),
		})
	}
	if err := downloader.DownloadFilesWithContext(ctx, tasks, status); err != nil {
		return ModelConfig{}, nil, err
	}

	artifactSpec, inferred, managedPrimary, err := updated.PrimaryArtifactSpec(modelPath)
	if err != nil {
		return ModelConfig{}, nil, err
	}
	var artifactResult *modelartifacts.Result
	if managedPrimary {
		result, err := bcl.artifactMaterializer.Ensure(ctx, modelPath, artifactSpec)
		if err != nil {
			if inferred {
				LogArtifactFallback(updated.Name, updated.Backend, err)
				managedPrimary = false
			} else {
				return ModelConfig{}, nil, err
			}
		} else {
			next := []modelartifacts.Spec{result.Spec}
			if len(updated.Artifacts) > 1 {
				next = append(next, updated.Artifacts[1:]...)
			}
			updated.Artifacts = next
			artifactResult = &result
		}
	}

	// Companions are only ever declared explicitly, so unlike the inferred
	// primary above they have no legacy path to fall back to: a config that
	// names one is asserting the backend needs it. Failing here keeps the error
	// at the acquisition boundary instead of surfacing as a confusing
	// missing-weights error inside the backend.
	if managedPrimary {
		for i := 1; i < len(updated.Artifacts); i++ {
			if updated.Artifacts[i].Target != modelartifacts.TargetCompanion {
				continue
			}
			companion, err := bcl.artifactMaterializer.Ensure(ctx, modelPath, updated.Artifacts[i])
			if err != nil {
				return ModelConfig{}, nil, fmt.Errorf("materialize companion artifact %q: %w", updated.Artifacts[i].Name, err)
			}
			updated.Artifacts[i] = companion.Spec
		}
	}

	if !managedPrimary && updated.IsModelURL() {
		modelFileName := updated.ModelFileName()
		uri := downloader.URI(updated.Model)
		if uri.ResolveURL() != updated.Model {
			if _, err := os.Stat(filepath.Join(modelPath, modelFileName)); errors.Is(err, os.ErrNotExist) {
				if err := uri.DownloadFileWithContext(ctx, filepath.Join(modelPath, modelFileName), "", 0, 0, status); err != nil {
					return ModelConfig{}, nil, err
				}
			}
			updated.Model = modelFileName
		}
	}

	if updated.IsMMProjURL() {
		modelFileName := updated.MMProjFileName()
		uri := downloader.URI(updated.MMProj)
		if _, err := os.Stat(filepath.Join(modelPath, modelFileName)); errors.Is(err, os.ErrNotExist) {
			if err := uri.DownloadFileWithContext(ctx, filepath.Join(modelPath, modelFileName), "", 0, 0, status); err != nil {
				return ModelConfig{}, nil, err
			}
		}
		updated.MMProj = modelFileName
	}
	return updated, artifactResult, nil
}

// bindingNeedsPersistence reports whether the freshly resolved artifact set
// differs from what is currently on the config, and so has to be written back.
// It compares the WHOLE set, not just the primary: a companion that resolved
// for the first time (or changed) must trigger a write even when the primary is
// unchanged, or its resolved state would never reach disk and would be lost on
// the next restart.
func bindingNeedsPersistence(current ModelConfig, resolved []modelartifacts.Spec) bool {
	return !reflect.DeepEqual(current.Artifacts, resolved)
}

func (bcl *ModelConfigLoader) displayPreloadedModel(config ModelConfig) {
	glamText := func(t string) {
		out, err := glamour.Render(t, bcl.preloadRenderMode)
		if err == nil && !bcl.disablePreloadColor {
			fmt.Println(out)
		} else {
			fmt.Println(t)
		}
	}

	if config.Name != "" {
		glamText(fmt.Sprintf("**Model name**: _%s_", config.Name))
	}
	if config.Description != "" {
		glamText(config.Description)
	}
	if config.Usage != "" {
		glamText(config.Usage)
	}
}

// MITMHostOwnership is the result of mapping intercept hosts to the
// model configs that claim them. The invariant the dispatcher relies
// on: every host belongs to AT MOST one model config. Any duplicate
// is surfaced via Conflicts and disables the MITM listener until
// resolved — a half-applied "first wins" rule would silently mask
// configuration drift, so we fail loud.
type MITMHostOwnership struct {
	// Owners maps lowercase hostname → owning model name. Empty when
	// no model declares mitm.hosts.
	Owners map[string]string
	// Conflicts lists hosts claimed by 2+ configs, with the names of
	// the configs that claim them. Non-empty Conflicts means callers
	// must NOT start the MITM listener.
	Conflicts map[string][]string
}

// MITMHostOwners walks every loaded ModelConfig's mitm.hosts, builds
// the host→owner index, and reports any duplicates. The lookup table
// is hostname-lowercased to match the Server's allowlist semantics.
func (bcl *ModelConfigLoader) MITMHostOwners() MITMHostOwnership {
	bcl.Lock()
	defer bcl.Unlock()
	owners := map[string]string{}
	collisions := map[string][]string{}
	for name, cfg := range bcl.configs {
		for _, h := range cfg.MITM.Hosts {
			h = strings.ToLower(strings.TrimSpace(h))
			if h == "" {
				continue
			}
			if existing, ok := owners[h]; ok && existing != name {
				if _, seen := collisions[h]; !seen {
					collisions[h] = []string{existing}
				}
				collisions[h] = append(collisions[h], name)
				continue
			}
			owners[h] = name
		}
	}
	return MITMHostOwnership{Owners: owners, Conflicts: collisions}
}

// LoadModelConfigsFromPath reads all the configurations of the models from a path
// (non-recursive)
func (bcl *ModelConfigLoader) LoadModelConfigsFromPath(path string, opts ...ConfigLoaderOption) error {
	return bcl.loadModelConfigsFromPath(path, false, opts...)
}

// LoadModelConfigsFromPathStrict builds an authoritative snapshot and fails
// when any visible config cannot be parsed or validated.
func (bcl *ModelConfigLoader) LoadModelConfigsFromPathStrict(path string, opts ...ConfigLoaderOption) error {
	return bcl.loadModelConfigsFromPath(path, true, opts...)
}

func (bcl *ModelConfigLoader) loadModelConfigsFromPath(path string, strict bool, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("LoadModelConfigsFromPath cannot read directory '%s': %w", path, err)
	}
	files := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, info)
	}
	loadOptions := &LoadOptions{}
	loadOptions.Apply(opts...)
	for _, file := range files {
		// Only load real YAML config files and ignore dotfiles or backup variants
		ext := strings.ToLower(filepath.Ext(file.Name()))
		if (ext != ".yaml" && ext != ".yml") || strings.HasPrefix(file.Name(), ".") {
			continue
		}

		filePath := filepath.Join(path, file.Name())
		absolutePath, absErr := filepath.Abs(filePath)
		if absErr != nil {
			return absErr
		}
		if _, gallerySource := loadOptions.galleryFiles[absolutePath]; gallerySource {
			galleryDocument, err := classifyGalleryDocument(filePath)
			if err != nil {
				if strict {
					return err
				}
				xlog.Error("LoadModelConfigsFromPath cannot validate gallery YAML file", "error", err, "File Name", file.Name())
				continue
			}
			if !galleryDocument {
				if strict {
					return fmt.Errorf("configured gallery source %q is not valid gallery metadata", filePath)
				}
				xlog.Error("Configured gallery source is not valid gallery metadata", "File Name", file.Name())
				continue
			}
			continue
		}

		// Read config(s) - handles both single and array formats
		configs, err := readModelConfigsFromFile(filePath, opts...)
		if err != nil {
			if strict {
				return err
			}
			xlog.Error("LoadModelConfigsFromPath cannot read config file", "error", err, "File Name", file.Name())
			continue
		}

		// Validate and store each config
		for _, c := range configs {
			if valid, validationErr := c.Validate(); valid {
				bcl.configs[c.Name] = *c
			} else {
				if strict {
					return fmt.Errorf("invalid model config %q: %w", c.Name, validationErr)
				}
				xlog.Error("config is not valid", "error", validationErr, "Name", c.Name)
			}
		}
	}

	// Surface aliases whose targets are missing or themselves aliases. These
	// resolve to a clear request-time error; warning here gives operators
	// visibility without failing startup.
	for name, c := range bcl.configs {
		if !c.IsAlias() {
			continue
		}
		target, ok := bcl.configs[c.Alias]
		switch {
		case !ok:
			xlog.Warn("alias points to unknown model", "alias", name, "target", c.Alias)
		case target.IsAlias():
			xlog.Warn("alias points to another alias (chains are not allowed)", "alias", name, "target", c.Alias)
		}
	}

	return nil
}

var galleryMetadataKeys = map[string]struct{}{
	"name": {}, "description": {}, "license": {}, "icon": {}, "tags": {}, "size": {},
	"url": {}, "urls": {}, "config_file": {}, "overrides": {}, "files": {}, "variants": {}, "prompt_templates": {},
}

// classifyGalleryDocument recognizes the two gallery documents LocalAI writes
// beside model configurations: a GalleryModel catalogue sequence and the
// legacy downloadable ModelConfig mapping. A gallery discriminator makes the
// document subject to the complete shape check; malformed or mixed documents
// are errors rather than silently disappearing from an authoritative snapshot.
func classifyGalleryDocument(path string) (bool, error) {
	data, _, err := safefile.ReadRegularAt(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return false, fmt.Errorf("read YAML file %q for classification: %w", path, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) != 1 {
		return false, nil // The model-config parser supplies the syntax error.
	}
	root := document.Content[0]
	switch root.Kind {
	case yaml.SequenceNode:
		looksGallery := false
		for _, entry := range root.Content {
			if entry.Kind == yaml.MappingNode && hasAnyMappingKey(entry, "url", "config_file", "variants", "files", "overrides") {
				looksGallery = true
			}
		}
		if !looksGallery {
			return false, nil
		}
		if len(root.Content) == 0 {
			return false, nil
		}
		for _, entry := range root.Content {
			if err := validateGalleryCatalogueEntry(entry); err != nil {
				return false, fmt.Errorf("invalid gallery catalogue %q: %w", path, err)
			}
		}
		return true, nil
	case yaml.MappingNode:
		if !hasAnyMappingKey(root, "config_file", "prompt_templates") {
			return false, nil
		}
		if err := validateLegacyGalleryModel(root); err != nil {
			return false, fmt.Errorf("invalid gallery model metadata %q: %w", path, err)
		}
		return true, nil
	default:
		return false, nil
	}
}

func validateGalleryCatalogueEntry(entry *yaml.Node) error {
	if entry.Kind != yaml.MappingNode {
		return errors.New("entry must be a mapping")
	}
	if err := validateGalleryKeys(entry); err != nil {
		return err
	}
	if !nonemptyScalar(galleryMappingValue(entry, "name")) {
		return errors.New("entry name must be a non-empty string")
	}
	hasPayload := nonemptyScalar(galleryMappingValue(entry, "url"))
	if node := galleryMappingValue(entry, "config_file"); node != nil {
		if node.Kind != yaml.MappingNode {
			return errors.New("config_file must be a mapping in a gallery catalogue")
		}
		hasPayload = true
	}
	for _, key := range []string{"overrides", "files", "variants"} {
		if node := galleryMappingValue(entry, key); node != nil {
			if err := validateGalleryPayload(key, node); err != nil {
				return err
			}
			hasPayload = hasPayload || len(node.Content) > 0
		}
	}
	if !hasPayload {
		return errors.New("entry has no installable gallery payload")
	}
	return nil
}

func validateLegacyGalleryModel(entry *yaml.Node) error {
	if err := validateGalleryKeys(entry); err != nil {
		return err
	}
	if !nonemptyScalar(galleryMappingValue(entry, "name")) {
		return errors.New("model name must be a non-empty string")
	}
	configFile := galleryMappingValue(entry, "config_file")
	if !nonemptyScalar(configFile) {
		return errors.New("config_file must be a non-empty YAML string")
	}
	for _, key := range []string{"files", "prompt_templates"} {
		if node := galleryMappingValue(entry, key); node != nil && node.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s must be a sequence", key)
		}
	}
	return nil
}

func validateGalleryKeys(entry *yaml.Node) error {
	for i := 0; i+1 < len(entry.Content); i += 2 {
		key := entry.Content[i].Value
		if _, ok := galleryMetadataKeys[key]; !ok {
			return fmt.Errorf("field %q is not gallery metadata", key)
		}
	}
	return nil
}

func validateGalleryPayload(key string, node *yaml.Node) error {
	switch key {
	case "overrides":
		if node.Kind != yaml.MappingNode {
			return errors.New("overrides must be a mapping")
		}
	case "files":
		if node.Kind != yaml.SequenceNode {
			return errors.New("files must be a sequence")
		}
		for _, file := range node.Content {
			if file.Kind != yaml.MappingNode || !nonemptyScalar(galleryMappingValue(file, "filename")) || !nonemptyScalar(galleryMappingValue(file, "uri")) {
				return errors.New("each gallery file must have non-empty filename and uri strings")
			}
		}
	case "variants":
		if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
			return errors.New("variants must be a non-empty sequence")
		}
		for _, variant := range node.Content {
			if variant.Kind != yaml.MappingNode || len(variant.Content) != 2 || variant.Content[0].Value != "model" || !nonemptyScalar(variant.Content[1]) {
				return errors.New("each variant must contain only a non-empty model string")
			}
		}
	}
	return nil
}

func galleryMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func hasAnyMappingKey(mapping *yaml.Node, keys ...string) bool {
	for _, key := range keys {
		if galleryMappingValue(mapping, key) != nil {
			return true
		}
	}
	return false
}

func nonemptyScalar(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str" && strings.TrimSpace(node.Value) != ""
}

// RevisionFor returns the config revision for modelName: the one an inference
// request for that model will carry.
//
// This is the only way to obtain a revision outside this package. Every
// publisher must use it, so that what is published and what is checked are
// the same value by construction rather than by two implementations happening
// to agree. Hashing a ModelConfig directly is not available to callers, because
// a config that has been through SetDefaults or the request middleware hashes
// to something no request will ever present.
func (bcl *ModelConfigLoader) RevisionFor(modelName string, appConfig *ApplicationConfig) (string, error) {
	cfg, err := bcl.LoadModelConfigFileByNameDefaultOptions(modelName, appConfig)
	if err != nil {
		return "", fmt.Errorf("resolving config revision for %q: %w", modelName, err)
	}
	return stampedRevision(cfg, modelName)
}

// RevisionForPath is RevisionFor for callers that hold loader options and a
// models path rather than an ApplicationConfig.
func (bcl *ModelConfigLoader) RevisionForPath(modelName, modelPath string, opts ...ConfigLoaderOption) (string, error) {
	cfg, err := bcl.LoadModelConfigFileByName(modelName, modelPath, opts...)
	if err != nil {
		return "", fmt.Errorf("resolving config revision for %q: %w", modelName, err)
	}
	return stampedRevision(cfg, modelName)
}

func stampedRevision(cfg *ModelConfig, modelName string) (string, error) {
	revision := cfg.PersistedConfigRevision()
	if revision == "" {
		return "", fmt.Errorf("no config revision stamped for %q", modelName)
	}
	return revision, nil
}
