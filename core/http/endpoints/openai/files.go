package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-skynet/LocalAI/core/config"

	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

var uploadedFiles []File

const uploadedFilesFile = "uploadedFiles.json"

// File represents the structure of a file object from the OpenAI API.
type File struct {
	ID        string    `json:"id"`         // Unique identifier for the file
	Object    string    `json:"object"`     // Type of the object (e.g., "file")
	Bytes     int       `json:"bytes"`      // Size of the file in bytes
	CreatedAt time.Time `json:"created_at"` // The time at which the file was created
	Filename  string    `json:"filename"`   // The name of the file
	Purpose   string    `json:"purpose"`    // The purpose of the file (e.g., "fine-tune", "classifications", etc.)
}

func saveUploadConfig(uploadDir string) {
	file, err := json.MarshalIndent(uploadedFiles, "", " ")
	if err != nil {
		log.Error().Msgf("Failed to JSON marshal the uploadedFiles: %s", err)
	}

	err = os.WriteFile(filepath.Join(uploadDir, uploadedFilesFile), file, 0644)
	if err != nil {
		log.Error().Msgf("Failed to save uploadedFiles to file: %s", err)
	}
}

func LoadUploadConfig(uploadPath string) {
	uploadFilePath := filepath.Join(uploadPath, uploadedFilesFile)

	_, err := os.Stat(uploadFilePath)
	if os.IsNotExist(err) {
		log.Debug().Msgf("No uploadedFiles file found at %s", uploadFilePath)
		return
	}

	file, err := os.ReadFile(uploadFilePath)
	if err != nil {
		log.Error().Msgf("Failed to read file: %s", err)
	} else {
		err = json.Unmarshal(file, &uploadedFiles)
		if err != nil {
			log.Error().Msgf("Failed to JSON unmarshal the file into uploadedFiles: %s", err)
		}
	}
}

// UploadFilesEndpoint https://platform.openai.com/docs/api-reference/files/create
func UploadFilesEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := c.FormFile("file")
		if err != nil {
			return err
		}

		// Check the file size
		if file.Size > int64(appConfig.UploadLimitMB*1024*1024) {
			return c.Status(fiber.StatusBadRequest).SendString(fmt.Sprintf("File size %d exceeds upload limit %d", file.Size, appConfig.UploadLimitMB))
		}

		purpose := c.FormValue("purpose", "") //TODO put in purpose dirs
		if purpose == "" {
			return c.Status(fiber.StatusBadRequest).SendString("Purpose is not defined")
		}

		// Sanitize the filename to prevent directory traversal
		filename := utils.SanitizeFileName(file.Filename)

		savePath := filepath.Join(appConfig.UploadDir, filename)

		// Check if file already exists
		if _, err := os.Stat(savePath); !os.IsNotExist(err) {
			return c.Status(fiber.StatusBadRequest).SendString("File already exists")
		}

		err = c.SaveFile(file, savePath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("Failed to save file: " + err.Error())
		}

		f := File{
			ID:        fmt.Sprintf("file-%d", time.Now().Unix()),
			Object:    "file",
			Bytes:     int(file.Size),
			CreatedAt: time.Now(),
			Filename:  file.Filename,
			Purpose:   purpose,
		}

		uploadedFiles = append(uploadedFiles, f)
		saveUploadConfig(appConfig.UploadDir)
		return c.Status(fiber.StatusOK).JSON(f)
	}
}

// ListFilesEndpoint https://platform.openai.com/docs/api-reference/files/list
func ListFilesEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	type ListFiles struct {
		Data   []File
		Object string
	}

	return func(c *fiber.Ctx) error {
		var listFiles ListFiles

		purpose := c.Query("purpose")
		if purpose == "" {
			listFiles.Data = uploadedFiles
		} else {
			for _, f := range uploadedFiles {
				if purpose == f.Purpose {
					listFiles.Data = append(listFiles.Data, f)
				}
			}
		}
		listFiles.Object = "list"
		return c.Status(fiber.StatusOK).JSON(listFiles)
	}
}

func getFileFromRequest(c *fiber.Ctx) (*File, error) {
	id := c.Params("file_id")
	if id == "" {
		return nil, fmt.Errorf("file_id parameter is required")
	}

	for _, f := range uploadedFiles {
		if id == f.ID {
			return &f, nil
		}
	}

	return nil, fmt.Errorf("unable to find file id %s", id)
}

// GetFilesEndpoint https://platform.openai.com/docs/api-reference/files/retrieve
func GetFilesEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		return c.JSON(file)
	}
}

// DeleteFilesEndpoint https://platform.openai.com/docs/api-reference/files/delete
func DeleteFilesEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	type DeleteStatus struct {
		Id      string
		Object  string
		Deleted bool
	}

	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		err = os.Remove(filepath.Join(appConfig.UploadDir, file.Filename))
		if err != nil {
			// If the file doesn't exist then we should just continue to remove it
			if !errors.Is(err, os.ErrNotExist) {
				return c.Status(fiber.StatusInternalServerError).SendString(fmt.Sprintf("Unable to delete file: %s, %v", file.Filename, err))
			}
		}

		// Remove upload from list
		for i, f := range uploadedFiles {
			if f.ID == file.ID {
				uploadedFiles = append(uploadedFiles[:i], uploadedFiles[i+1:]...)
				break
			}
		}

		saveUploadConfig(appConfig.UploadDir)
		return c.JSON(DeleteStatus{
			Id:      file.ID,
			Object:  "file",
			Deleted: true,
		})
	}
}

// GetFilesContentsEndpoint https://platform.openai.com/docs/api-reference/files/retrieve-contents
func GetFilesContentsEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		fileContents, err := os.ReadFile(filepath.Join(appConfig.UploadDir, file.Filename))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		return c.Send(fileContents)
	}
}
