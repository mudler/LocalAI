package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type BackendConfigLoader struct {
	configs   map[string]BackendConfig
	modelPath string
	sync.Mutex
}

func NewBackendConfigLoader(modelPath string) *BackendConfigLoader {
	return &BackendConfigLoader{
		configs:   make(map[string]BackendConfig),
		modelPath: modelPath,
	}
}

type LoadOptions struct {
	modelPath        string
	debug            bool
	threads, ctxSize int
	f16              bool
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

type ConfigLoaderOption func(*LoadOptions)

func (lo *LoadOptions) Apply(options ...ConfigLoaderOption) {
	for _, l := range options {
		l(lo)
	}
}

// TODO: either in the next PR or the next commit, I want to merge these down into a single function that looks at the first few characters of the file to determine if we need to deserialize to []BackendConfig or BackendConfig
func readMultipleBackendConfigsFromFile(file string, opts ...ConfigLoaderOption) ([]*BackendConfig, error) {
	c := &[]*BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	for _, cc := range *c {
		cc.SetDefaults(opts...)
	}

	return *c, nil
}

func readBackendConfigFromFile(file string, opts ...ConfigLoaderOption) (*BackendConfig, error) {
	lo := &LoadOptions{}
	lo.Apply(opts...)

	c := &BackendConfig{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	c.SetDefaults(opts...)
	return c, nil
}

// Load a config file for a model
func (bcl *BackendConfigLoader) LoadBackendConfigFileByName(modelName, modelPath string, opts ...ConfigLoaderOption) (*BackendConfig, error) {

	// Load a config file if present after the model name
	cfg := &BackendConfig{
		PredictionOptions: schema.PredictionOptions{
			Model: modelName,
		},
	}

	cfgExisting, exists := bcl.GetBackendConfig(modelName)
	if exists {
		cfg = &cfgExisting
	} else {
		// Try loading a model config file
		modelConfig := filepath.Join(modelPath, modelName+".yaml")
		if _, err := os.Stat(modelConfig); err == nil {
			if err := bcl.LoadBackendConfig(
				modelConfig, opts...,
			); err != nil {
				return nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = bcl.GetBackendConfig(modelName)
			if exists {
				cfg = &cfgExisting
			}
		}
	}

	cfg.SetDefaults(append(opts, ModelPath(modelPath))...)

	return cfg, nil
}

// This format is currently only used when reading a single file at startup, passed in via ApplicationConfig.ConfigFile
func (bcl *BackendConfigLoader) LoadMultipleBackendConfigsSingleFile(file string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	c, err := readMultipleBackendConfigsFromFile(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		if cc.Validate() {
			bcl.configs[cc.Name] = *cc
		}
	}
	return nil
}

func (bcl *BackendConfigLoader) LoadBackendConfig(file string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	c, err := readBackendConfigFromFile(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	if c.Validate() {
		bcl.configs[c.Name] = *c
	} else {
		return fmt.Errorf("config is not valid")
	}

	return nil
}

func (bcl *BackendConfigLoader) GetBackendConfig(m string) (BackendConfig, bool) {
	bcl.Lock()
	defer bcl.Unlock()
	v, exists := bcl.configs[m]
	return v, exists
}

func (bcl *BackendConfigLoader) GetAllBackendConfigs() []BackendConfig {
	bcl.Lock()
	defer bcl.Unlock()
	var res []BackendConfig
	for _, v := range bcl.configs {
		res = append(res, v)
	}

	sort.SliceStable(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})

	return res
}

func (bcl *BackendConfigLoader) GetBackendConfigsByFilter(filter BackendConfigFilterFn) []BackendConfig {
	bcl.Lock()
	defer bcl.Unlock()
	var res []BackendConfig

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

func (bcl *BackendConfigLoader) RemoveBackendConfig(m string) {
	bcl.Lock()
	defer bcl.Unlock()
	delete(bcl.configs, m)
}

// Preload prepare models if they are not local but url or huggingface repositories
func (bcl *BackendConfigLoader) Preload(modelPath string) error {
	bcl.Lock()
	defer bcl.Unlock()

	status := func(fileName, current, total string, percent float64) {
		utils.DisplayDownloadFunction(fileName, current, total, percent)
	}

	log.Info().Msgf("Preloading models from %s", modelPath)

	renderMode := "dark"
	if os.Getenv("COLOR") != "" {
		renderMode = os.Getenv("COLOR")
	}

	glamText := func(t string) {
		out, err := glamour.Render(t, renderMode)
		if err == nil && os.Getenv("NO_COLOR") == "" {
			fmt.Println(out)
		} else {
			fmt.Println(t)
		}
	}

	for i, config := range bcl.configs {

		// Download files and verify their SHA
		for i, file := range config.DownloadFiles {
			log.Debug().Msgf("Checking %q exists and matches SHA", file.Filename)

			if err := utils.VerifyPath(file.Filename, modelPath); err != nil {
				return err
			}
			// Create file path
			filePath := filepath.Join(modelPath, file.Filename)

			if err := file.URI.DownloadFile(filePath, file.SHA256, i, len(config.DownloadFiles), status); err != nil {
				return err
			}
		}

		// If the model is an URL, expand it, and download the file
		if config.IsModelURL() {
			modelFileName := config.ModelFileName()
			uri := downloader.URI(config.Model)
			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, modelFileName)); errors.Is(err, os.ErrNotExist) {
				err := uri.DownloadFile(filepath.Join(modelPath, modelFileName), "", 0, 0, status)
				if err != nil {
					return err
				}
			}

			cc := bcl.configs[i]
			c := &cc
			c.PredictionOptions.Model = modelFileName
			bcl.configs[i] = *c
		}

		if config.IsMMProjURL() {
			modelFileName := config.MMProjFileName()
			uri := downloader.URI(config.MMProj)
			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, modelFileName)); errors.Is(err, os.ErrNotExist) {
				err := uri.DownloadFile(filepath.Join(modelPath, modelFileName), "", 0, 0, status)
				if err != nil {
					return err
				}
			}

			cc := bcl.configs[i]
			c := &cc
			c.MMProj = modelFileName
			bcl.configs[i] = *c
		}

		if bcl.configs[i].Name != "" {
			glamText(fmt.Sprintf("**Model name**: _%s_", bcl.configs[i].Name))
		}
		if bcl.configs[i].Description != "" {
			//glamText("**Description**")
			glamText(bcl.configs[i].Description)
		}
		if bcl.configs[i].Usage != "" {
			//glamText("**Usage**")
			glamText(bcl.configs[i].Usage)
		}
	}
	return nil
}

// LoadBackendConfigsFromPath reads all the configurations of the models from a path
// (non-recursive)
func (bcl *BackendConfigLoader) LoadBackendConfigsFromPath(path string, opts ...ConfigLoaderOption) error {
	bcl.Lock()
	defer bcl.Unlock()
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("cannot read directory '%s': %w", path, err)
	}
	files := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, info)
	}
	for _, file := range files {
		// Skip templates, YAML and .keep files
		if !strings.Contains(file.Name(), ".yaml") && !strings.Contains(file.Name(), ".yml") ||
			strings.HasPrefix(file.Name(), ".") {
			continue
		}
		c, err := readBackendConfigFromFile(filepath.Join(path, file.Name()), opts...)
		if err != nil {
			log.Error().Err(err).Msgf("cannot read config file: %s", file.Name())
			continue
		}
		if c.Validate() {
			bcl.configs[c.Name] = *c
		} else {
			log.Error().Err(err).Msgf("config is not valid")
		}
	}

	return nil
}
