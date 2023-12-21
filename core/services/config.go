package services

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

type ConfigLoader struct {
	configs map[string]datamodel.Config
	sync.Mutex
}

func NewConfigLoader() *ConfigLoader {
	return &ConfigLoader{
		configs: make(map[string]datamodel.Config),
	}
}

// TODO: check this is correct post-merge
func (cm *ConfigLoader) LoadConfig(file string) error {
	cm.Lock()
	defer cm.Unlock()
	c, err := datamodel.ReadConfigFile(file)
	if err != nil || len(c) == 0 {
		return fmt.Errorf("cannot read config file: %w", err)
	}

	cm.configs[c[0].Name] = *c[0]
	return nil
}

func (cm *ConfigLoader) GetConfig(m string) (datamodel.Config, bool) {
	cm.Lock()
	defer cm.Unlock()
	v, exists := cm.configs[m]
	return v, exists
}

func (cm *ConfigLoader) GetAllConfigs() []datamodel.Config {
	cm.Lock()
	defer cm.Unlock()
	var res []datamodel.Config
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
		c, err := datamodel.ReadConfigFile(filepath.Join(path, file.Name()))
		if err == nil {
			cm.configs[c.Name] = *c
		}
	}

	return nil
}

// TODO: Does this belong under ConfigLoader?
func (cl *ConfigLoader) Preload(modelPath string) error {
	cl.Lock()
	defer cl.Unlock()

	for i, config := range cl.configs {
		modelURL := config.PredictionOptions.Model
		modelURL = utils.ConvertURL(modelURL)
		if strings.HasPrefix(modelURL, "http://") || strings.HasPrefix(modelURL, "https://") {
			// md5 of model name
			md5Name := utils.MD5(modelURL)

			// check if file exists
			if _, err := os.Stat(filepath.Join(modelPath, md5Name)); err == os.ErrNotExist {
				err := utils.DownloadFile(modelURL, filepath.Join(modelPath, md5Name), "", func(fileName, current, total string, percent float64) {
					log.Info().Msgf("Downloading %s: %s/%s (%.2f%%)", fileName, current, total, percent)
				})
				if err != nil {
					return err
				}
			}

			cc := cl.configs[i]
			c := &cc
			c.PredictionOptions.Model = md5Name
			cl.configs[i] = *c
		}
	}
	return nil
}

func (cl *ConfigLoader) LoadConfigFile(file string) error {
	cl.Lock()
	defer cl.Unlock()
	c, err := datamodel.ReadConfigFile(file)
	if err != nil {
		return fmt.Errorf("cannot load config file: %w", err)
	}

	for _, cc := range c {
		cl.configs[cc.Name] = *cc
	}
	return nil
}
