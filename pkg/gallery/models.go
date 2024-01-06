package gallery

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/pkg/downloader"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/imdario/mergo"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

/*

description: |
    foo
license: ""

urls:
-
-

name: "bar"

config_file: |
    # Note, name will be injected. or generated by the alias wanted by the user
    threads: 14

files:
    - filename: ""
      sha: ""
      uri: ""

prompt_templates:
    - name: ""
      content: ""

*/
// InstallableModel is the model configuration which contains all the model details
// This configuration is read from the gallery endpoint and is used to download and install the model
type InstallableModel struct {
	Description     string           `yaml:"description"`
	License         string           `yaml:"license"`
	URLs            []string         `yaml:"urls"`
	Name            string           `yaml:"name"`
	ConfigFile      string           `yaml:"config_file"`
	Files           []File           `yaml:"files"`
	PromptTemplates []PromptTemplate `yaml:"prompt_templates"`
}

type File struct {
	Filename string `yaml:"filename" json:"filename"`
	SHA256   string `yaml:"sha256" json:"sha256"`
	URI      string `yaml:"uri" json:"uri"`
}

type PromptTemplate struct {
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

func GetInstallableModelFromURL(url string) (InstallableModel, error) {
	var config InstallableModel
	err := utils.GetURI(url, func(url string, d []byte) error {
		return yaml.Unmarshal(d, &config)
	})
	if err != nil {
		log.Error().Msgf("GetGalleryConfigFromURL error for url %s\n%s", url, err.Error())
		return config, err
	}
	return config, nil
}

func ReadInstallableModelFile(filePath string) (*InstallableModel, error) {
	// Read the YAML file
	yamlFile, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %v", err)
	}

	// Unmarshal YAML data into a Config struct
	var config InstallableModel
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %v", err)
	}

	return &config, nil
}

func InstallModel(basePath, nameOverride string, config *InstallableModel, configOverrides map[string]interface{}, downloadStatus func(string, string, string, float64)) error {
	// Create base path if it doesn't exist
	err := os.MkdirAll(basePath, 0755)
	if err != nil {
		return fmt.Errorf("failed to create base path: %v", err)
	}

	if len(configOverrides) > 0 {
		log.Debug().Msgf("Config overrides %+v", configOverrides)
	}

	// Download files and verify their SHA
	for _, file := range config.Files {
		log.Debug().Msgf("Checking %q exists and matches SHA", file.Filename)

		if err := utils.VerifyPath(file.Filename, basePath); err != nil {
			return err
		}
		// Create file path
		filePath := filepath.Join(basePath, file.Filename)

		if err := downloader.DownloadFile(file.URI, filePath, file.SHA256, downloadStatus); err != nil {
			return err
		}
	}

	// Write prompt template contents to separate files
	for _, template := range config.PromptTemplates {
		if err := utils.VerifyPath(template.Name+".tmpl", basePath); err != nil {
			return err
		}
		// Create file path
		filePath := filepath.Join(basePath, template.Name+".tmpl")

		// Create parent directory
		err := os.MkdirAll(filepath.Dir(filePath), 0755)
		if err != nil {
			return fmt.Errorf("failed to create parent directory for prompt template %q: %v", template.Name, err)
		}
		// Create and write file content
		err = os.WriteFile(filePath, []byte(template.Content), 0644)
		if err != nil {
			return fmt.Errorf("failed to write prompt template %q: %v", template.Name, err)
		}

		log.Debug().Msgf("Prompt template %q written", template.Name)
	}

	name := config.Name
	if nameOverride != "" {
		name = nameOverride
	}

	if err := utils.VerifyPath(name+".yaml", basePath); err != nil {
		return err
	}

	// write config file
	if len(configOverrides) != 0 || len(config.ConfigFile) != 0 {
		configFilePath := filepath.Join(basePath, name+".yaml")

		// Read and update config file as map[string]interface{}
		configMap := make(map[string]interface{})
		err = yaml.Unmarshal([]byte(config.ConfigFile), &configMap)
		if err != nil {
			return fmt.Errorf("failed to unmarshal config YAML: %v", err)
		}

		configMap["name"] = name

		if err := mergo.Merge(&configMap, configOverrides, mergo.WithOverride); err != nil {
			return err
		}

		// Write updated config file
		updatedConfigYAML, err := yaml.Marshal(configMap)
		if err != nil {
			return fmt.Errorf("failed to marshal updated config YAML: %v", err)
		}

		err = os.WriteFile(configFilePath, updatedConfigYAML, 0644)
		if err != nil {
			return fmt.Errorf("failed to write updated config file: %v", err)
		}

		log.Debug().Msgf("Written config file %s", configFilePath)
	}

	return nil
}
