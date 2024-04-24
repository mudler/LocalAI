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
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type BackendConfigLoader struct {
	configs map[string]BackendConfig
	sync.Mutex
}

type LoadOptions struct {
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

// Load a config file for a model
func (cl *BackendConfigLoader) LoadBackendConfigFileByName(modelName, modelPath string, opts ...ConfigLoaderOption) (*BackendConfig, error) {

	// Load a config file if present after the model name
	cfg := &BackendConfig{
		PredictionOptions: schema.PredictionOptions{
			Model: modelName,
		},
	}

	cfgExisting, exists := cl.GetBackendConfig(modelName)
	if exists {
		cfg = &cfgExisting
	} else {
		// Try loading a model config file
		modelConfig := filepath.Join(modelPath, modelName+".yaml")
		if _, err := os.Stat(modelConfig); err == nil {
			if err := cl.LoadBackendConfig(
				modelConfig, opts...,
			); err != nil {
				return nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = cl.GetBackendConfig(modelName)
			if exists {
				cfg = &cfgExisting
			}
		}
	}

	cfg.SetDefaults(opts...)

	return cfg, nil
}

func NewBackendConfigLoader() *BackendConfigLoader {
	return &BackendConfigLoader{
		configs: make(map[string]BackendConfig),
	}
}
func ReadBackendConfigFile(file string, opts ...ConfigLoaderOption) ([]*BackendConfig, error) {
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

func ReadBackendConfig(file string, opts ...ConfigLoaderOption) (*BackendConfig, error) {
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

func (cm *BackendConfigLoader) LoadBackendConfigFile(file string, opts ...ConfigLoaderOption) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := ReadBackendConfigFile(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		cm.configs[cc.Name] = *cc
	}
	return nil
}

func (cl *BackendConfigLoader) LoadBackendConfig(file string, opts ...ConfigLoaderOption) error {
	cl.Lock()
	defer cl.Unlock()
	c, err := ReadBackendConfig(file, opts...)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	cl.configs[c.Name] = *c
	return nil
}

func (cl *BackendConfigLoader) GetBackendConfig(m string) (BackendConfig, bool) {
	cl.Lock()
	defer cl.Unlock()
	v, exists := cl.configs[m]
	return v, exists
}

func (cl *BackendConfigLoader) GetAllBackendConfigs() []BackendConfig {
	cl.Lock()
	defer cl.Unlock()
	var res []BackendConfig
	for _, v := range cl.configs {
		res = append(res, v)
	}

	sort.SliceStable(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})

	return res
}

func (cl *BackendConfigLoader) ListBackendConfigs() []string {
	cl.Lock()
	defer cl.Unlock()
	var res []string
	for k := range cl.configs {
		res = append(res, k)
	}
	return res
}

// Preload prepare models if they are not local but url or huggingface repositories
func (cl *BackendConfigLoader) Preload(modelPath string) error {
	cl.Lock()
	defer cl.Unlock()

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

	for i, config := range cl.configs {

		// Download files and verify their SHA
		for i, file := range config.DownloadFiles {
			log.Debug().Msgf("Checking %q exists and matches SHA", file.Filename)

			if err := utils.VerifyPath(file.Filename, modelPath); err != nil {
				return err
			}
			// Create file path
			filePath := filepath.Join(modelPath, file.Filename)

			if err := downloader.DownloadFile(file.URI, filePath, file.SHA256, i, len(config.DownloadFiles), status); err != nil {
				return err
			}
		}

		modelURL := config.PredictionOptions.Model
		modelURL = downloader.ConvertURL(modelURL)

		if downloader.LooksLikeURL(modelURL) {
			// md5 of model name
			md5Name := utils.MD5(modelURL)

			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, md5Name)); errors.Is(err, os.ErrNotExist) {
				err := downloader.DownloadFile(modelURL, filepath.Join(modelPath, md5Name), "", 0, 0, status)
				if err != nil {
					return err
				}
			}

			cc := cl.configs[i]
			c := &cc
			c.PredictionOptions.Model = md5Name
			cl.configs[i] = *c
		}
		if cl.configs[i].Name != "" {
			glamText(fmt.Sprintf("**Model name**: _%s_", cl.configs[i].Name))
		}
		if cl.configs[i].Description != "" {
			//glamText("**Description**")
			glamText(cl.configs[i].Description)
		}
		if cl.configs[i].Usage != "" {
			//glamText("**Usage**")
			glamText(cl.configs[i].Usage)
		}
	}
	return nil
}

// LoadBackendConfigsFromPath reads all the configurations of the models from a path
// (non-recursive)
func (cm *BackendConfigLoader) LoadBackendConfigsFromPath(path string, opts ...ConfigLoaderOption) error {
	cm.Lock()
	defer cm.Unlock()
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
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
		if !strings.Contains(file.Name(), ".yaml") && !strings.Contains(file.Name(), ".yml") {
			continue
		}
		c, err := ReadBackendConfig(filepath.Join(path, file.Name()), opts...)
		if err == nil {
			cm.configs[c.Name] = *c
		}
	}

	return nil
}
