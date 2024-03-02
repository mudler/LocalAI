package utils

import (
	"encoding/json"
	"github.com/rs/zerolog/log"
	"os"
	"path/filepath"
)

func SaveConfig(uploadDir, fileName string, obj any) {
	file, err := json.MarshalIndent(obj, "", " ")
	if err != nil {
		log.Error().Msgf("Failed to JSON marshal the uploadedFiles: %s", err)
	}

	err = os.WriteFile(filepath.Join(uploadDir, fileName), file, 0644)
	if err != nil {
		log.Error().Msgf("Failed to save uploadedFiles to file: %s", err)
	}
}

func LoadConfig(filePath, fileName string, obj any) {
	uploadFilePath := filepath.Join(filePath, fileName)

	_, err := os.Stat(uploadFilePath)
	if os.IsNotExist(err) {
		log.Debug().Msgf("No uploadedFiles file found at %s", uploadFilePath)
		return
	}

	file, err := os.ReadFile(uploadFilePath)
	if err != nil {
		log.Error().Msgf("Failed to read file: %s", err)
	} else {
		err = json.Unmarshal(file, &obj)
		if err != nil {
			log.Error().Msgf("Failed to JSON unmarshal the file into uploadedFiles: %s", err)
		}
	}
}
