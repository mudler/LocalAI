package utils

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
)

func SaveConfig(filePath, fileName string, obj any) {
	file, err := json.MarshalIndent(obj, "", " ")
	if err != nil {
		log.Error().Err(err).Msg("failed to JSON marshal the uploadedFiles")
	}

	absolutePath := filepath.Join(filePath, fileName)
	err = os.WriteFile(absolutePath, file, 0600)
	if err != nil {
		log.Error().Err(err).Str("filepath", absolutePath).Msg("failed to save configuration file")
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
		log.Error().Err(err).Str("filepath", uploadFilePath).Msg("failed to read file")
	} else {
		err = json.Unmarshal(file, &obj)
		if err != nil {
			log.Error().Err(err).Str("filepath", uploadFilePath).Msg("failed to parse file as JSON")
		}
	}
}
