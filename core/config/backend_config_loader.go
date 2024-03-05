package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

type BackendConfigLoader struct {
	configs map[string]BackendConfig
	sync.Mutex
}

func NewBackendConfigLoader() *BackendConfigLoader {
	return &BackendConfigLoader{
		configs: make(map[string]BackendConfig),
	}
}

func (bcl *BackendConfigLoader) LoadBackendConfig(file string) error {
	bcl.Lock()
	defer bcl.Unlock()
	c, err := ReadBackendConfig(file)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	bcl.configs[c.Name] = *c
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
	return res
}

func (bcl *BackendConfigLoader) ListBackendConfigs() []string {
	bcl.Lock()
	defer bcl.Unlock()
	var res []string
	for k := range bcl.configs {
		res = append(res, k)
	}
	return res
}

// Preload prepare models if they are not local but url or huggingface repositories
func (bcl *BackendConfigLoader) Preload(modelPath string) error {
	bcl.Lock()
	defer bcl.Unlock()

	status := func(fileName, current, total string, percent float64) {
		utils.DisplayDownloadFunction(fileName, current, total, percent)
	}

	log.Info().Msgf("Preloading models from %s", modelPath)

	for i, config := range bcl.configs {

		// Download files and verify their SHA
		for _, file := range config.DownloadFiles {
			log.Debug().Msgf("Checking %q exists and matches SHA", file.Filename)

			if err := utils.VerifyPath(file.Filename, modelPath); err != nil {
				return err
			}
			// Create file path
			filePath := filepath.Join(modelPath, file.Filename)

			if err := downloader.DownloadFile(file.URI, filePath, file.SHA256, status); err != nil {
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
				err := downloader.DownloadFile(modelURL, filepath.Join(modelPath, md5Name), "", status)
				if err != nil {
					return err
				}
			}

			cc := bcl.configs[i]
			c := &cc
			c.PredictionOptions.Model = md5Name
			bcl.configs[i] = *c
		}
		if bcl.configs[i].Name != "" {
			log.Info().Msgf("Model name: %s", bcl.configs[i].Name)
		}
		if bcl.configs[i].Description != "" {
			log.Info().Msgf("Model description: %s", bcl.configs[i].Description)
		}
		if bcl.configs[i].Usage != "" {
			log.Info().Msgf("Model usage: \n%s", bcl.configs[i].Usage)
		}
	}
	return nil
}

func (bcl *BackendConfigLoader) LoadBackendConfigsFromPath(path string) error {
	bcl.Lock()
	defer bcl.Unlock()
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
		c, err := ReadBackendConfig(filepath.Join(path, file.Name()))
		if err == nil {
			bcl.configs[c.Name] = *c
		}
	}

	return nil
}

func (bcl *BackendConfigLoader) LoadBackendConfigFile(file string) error {
	bcl.Lock()
	defer bcl.Unlock()
	c, err := ReadBackendConfigFile(file)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		bcl.configs[cc.Name] = *cc
	}
	return nil
}

//////////

// Load a config file for a model
func LoadBackendConfigFileByName(modelName string, bcl *BackendConfigLoader, appConfig *ApplicationConfig) (*BackendConfig, error) {
	// Load a config file if present after the model name
	modelConfig := filepath.Join(appConfig.ModelPath, modelName+".yaml")

	var cfg *BackendConfig

	defaults := func() {
		cfg = DefaultConfig(modelName)
		cfg.ContextSize = appConfig.ContextSize
		cfg.Threads = appConfig.Threads
		cfg.F16 = appConfig.F16
		cfg.Debug = appConfig.Debug
	}

	cfgExisting, exists := bcl.GetBackendConfig(modelName)
	if !exists {
		if _, err := os.Stat(modelConfig); err == nil {
			if err := bcl.LoadBackendConfig(modelConfig); err != nil {
				return nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = bcl.GetBackendConfig(modelName)
			if exists {
				cfg = &cfgExisting
			} else {
				defaults()
			}
		} else {
			defaults()
		}
	} else {
		cfg = &cfgExisting
	}

	// Set the parameters for the language model prediction
	//updateConfig(cfg, input)

	// Don't allow 0 as setting
	if cfg.Threads == 0 {
		if appConfig.Threads != 0 {
			cfg.Threads = appConfig.Threads
		} else {
			cfg.Threads = 4
		}
	}

	// Enforce debug flag if passed from CLI
	if appConfig.Debug {
		cfg.Debug = true
	}

	return cfg, nil
}

func LoadBackendConfigForModelAndOpenAIRequest(modelFile string, input *schema.OpenAIRequest, bcl *BackendConfigLoader, appConfig *ApplicationConfig) (*BackendConfig, *schema.OpenAIRequest, error) {
	cfg, err := LoadBackendConfigFileByName(modelFile, bcl, appConfig)

	// Set the parameters for the language model prediction
	updateBackendConfigFromOpenAIRequest(cfg, input)

	return cfg, input, err
}
