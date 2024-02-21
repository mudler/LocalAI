package services

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
	"gopkg.in/yaml.v3"
)

type ConfigLoader struct {
	configs map[string]schema.Config
	sync.Mutex
}

// Load a config file for a model
func LoadConfigFileByName(modelName, modelPath string, cl *ConfigLoader, debug bool, threads, ctx int, f16 bool) (*schema.Config, error) {
	// Load a config file if present after the model name
	modelConfig := filepath.Join(modelPath, modelName+".yaml")

	var cfg *schema.Config

	defaults := func() {
		cfg = schema.DefaultConfig(modelName)
		cfg.ContextSize = ctx
		cfg.Threads = threads
		cfg.F16 = f16
		cfg.Debug = debug
	}

	cfgExisting, exists := cl.GetConfig(modelName)
	if !exists {
		if _, err := os.Stat(modelConfig); err == nil {
			if err := cl.LoadConfig(modelConfig); err != nil {
				return nil, fmt.Errorf("failed loading model config (%s) %s", modelConfig, err.Error())
			}
			cfgExisting, exists = cl.GetConfig(modelName)
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
		if threads != 0 {
			cfg.Threads = threads
		} else {
			cfg.Threads = 4
		}
	}

	// Enforce debug flag if passed from CLI
	if debug {
		cfg.Debug = true
	}

	return cfg, nil
}

func NewConfigLoader() *ConfigLoader {
	return &ConfigLoader{
		configs: make(map[string]schema.Config),
	}
}
func ReadConfigFile(file string) ([]*schema.Config, error) {
	c := &[]*schema.Config{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	return *c, nil
}

func ReadConfig(file string) (*schema.Config, error) {
	c := &schema.Config{}
	f, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read config file: %w", err)
	}
	if err := yaml.Unmarshal(f, c); err != nil {
		return nil, fmt.Errorf("cannot unmarshal config file: %w", err)
	}

	return c, nil
}

func (cm *ConfigLoader) LoadConfigFile(file string) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := ReadConfigFile(file)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		cm.configs[cc.Name] = *cc
	}
	return nil
}

func (cm *ConfigLoader) LoadConfig(file string) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := ReadConfig(file)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	cm.configs[c.Name] = *c
	return nil
}

func (cl *ConfigLoader) GetConfig(m string) (schema.Config, bool) {
	cl.Lock()
	defer cl.Unlock()
	v, exists := cl.configs[m]
	return v, exists
}

func (cl *ConfigLoader) GetAllConfigs() []schema.Config {
	cl.Lock()
	defer cl.Unlock()
	var res []schema.Config
	for _, v := range cl.configs {
		res = append(res, v)
	}
	return res
}

func (cl *ConfigLoader) ListConfigs() []string {
	cl.Lock()
	defer cl.Unlock()
	var res []string
	for k := range cl.configs {
		res = append(res, k)
	}
	return res
}

// Preload prepare models if they are not local but url or huggingface repositories
func (cl *ConfigLoader) Preload(modelPath string) error {
	cl.Lock()
	defer cl.Unlock()

	status := func(fileName, current, total string, percent float64) {
		utils.DisplayDownloadFunction(fileName, current, total, percent)
	}

	log.Info().Msgf("Preloading models from %s", modelPath)

	for i, config := range cl.configs {

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

			cc := cl.configs[i]
			c := &cc
			c.PredictionOptions.Model = md5Name
			cl.configs[i] = *c
		}
		if cl.configs[i].Name != "" {
			log.Info().Msgf("Model name: %s", cl.configs[i].Name)
		}
		if cl.configs[i].Description != "" {
			log.Info().Msgf("Model description: %s", cl.configs[i].Description)
		}
		if cl.configs[i].Usage != "" {
			log.Info().Msgf("Model usage: \n%s", cl.configs[i].Usage)
		}
	}
	return nil
}

func (cm *ConfigLoader) LoadConfigs(path string) error {
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
		c, err := ReadConfig(filepath.Join(path, file.Name()))
		if err == nil {
			cm.configs[c.Name] = *c
		}
	}

	return nil
}
