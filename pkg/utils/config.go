package utils

import (
	"encoding/json"
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
)

func SaveConfig(filePath, fileName string, obj any) {
	file, err := json.MarshalIndent(obj, "", " ")
	if err != nil {
		log.Error().Msgf("Failed to JSON marshal the uploadedFiles: %s", err)
	}

	absolutePath := filepath.Join(filePath, fileName)
	err = os.WriteFile(absolutePath, file, 0644)
	if err != nil {
		log.Error().Msgf("Failed to save configuration file to %s: %s", absolutePath, err)
	}
}

func LoadConfig(filePath, fileName string, obj interface{}) {
	uploadFilePath := filepath.Join(filePath, fileName)

	_, err := os.Stat(uploadFilePath)
	if os.IsNotExist(err) {
		log.Debug().Msgf("No configuration file found at %s", uploadFilePath)
		return
	}

	file, err := os.ReadFile(uploadFilePath)
	if err != nil {
		log.Error().Msgf("Failed to read file: %s", err)
	} else {
		err = json.Unmarshal(file, &obj)
		if err != nil {
			log.Error().Msgf("Failed to JSON unmarshal the file %s: %v", uploadFilePath, err)
		}
	}
}
