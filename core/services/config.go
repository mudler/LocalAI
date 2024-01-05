package services

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-skynet/LocalAI/pkg/schema"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

type ConfigLoader struct {
	configs map[string]schema.Config
	sync.Mutex
}

func NewConfigLoader() *ConfigLoader {
	return &ConfigLoader{
		configs: make(map[string]schema.Config),
	}
}

// TODO: check this is correct post-merge
func (cm *ConfigLoader) LoadConfig(file string) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := schema.ReadSingleConfigFile(file)
	if err != nil {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	cm.configs[c.Name] = *c
	return nil
}

func (cm *ConfigLoader) GetConfig(m string) (schema.Config, bool) {
	cm.Lock()
	defer cm.Unlock()
	v, exists := cm.configs[m]
	return v, exists
}

func (cm *ConfigLoader) GetAllConfigs() []schema.Config {
	cm.Lock()
	defer cm.Unlock()
	var res []schema.Config
	for _, v := range cm.configs {
		res = append(res, v)
	}
	return res
}

func (cm *ConfigLoader) ListConfigs() []string {
	cm.Lock()
	defer cm.Unlock()
	var res []string
	for k := range cm.configs {
		res = append(res, k)
	}
	return res
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
		c, err := schema.ReadSingleConfigFile(filepath.Join(path, file.Name()))
		if err == nil {
			cm.configs[c.Name] = *c
		}
	}

	return nil
}

// Preload prepare models if they are not local but url or huggingface repositories
func (cm *ConfigLoader) Preload(modelPath string) error {
	cm.Lock()
	defer cm.Unlock()

	status := func(fileName, current, total string, percent float64) {
		utils.DisplayDownloadFunction(fileName, current, total, percent)
	}

	log.Info().Msgf("Preloading models from %s", modelPath)

	for _, config := range cm.configs {

		// Download files and verify their SHA
		for _, file := range config.DownloadFiles {
			log.Debug().Msgf("Checking %q exists and matches SHA", file.Filename)

			if err := utils.VerifyPath(file.Filename, modelPath); err != nil {
				return err
			}
			// Create file path
			filePath := filepath.Join(modelPath, file.Filename)

			if err := utils.DownloadFile(file.URI, filePath, file.SHA256, status); err != nil {
				return err
			}
		}

		modelURL := config.PredictionOptions.Model
		modelURL = utils.ConvertURL(modelURL)

		if utils.LooksLikeURL(modelURL) {
			// md5 of model name
			md5Name := utils.MD5(modelURL)

			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, md5Name)); errors.Is(err, os.ErrNotExist) {
				err := utils.DownloadFile(modelURL, filepath.Join(modelPath, md5Name), "", status)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (cl *ConfigLoader) LoadConfigFile(file string) error {
	cl.Lock()
	defer cl.Unlock()
	c, err := schema.ReadConfigFile(file)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		cl.configs[cc.Name] = *cc
	}
	return nil
}
