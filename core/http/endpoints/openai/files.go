package openai

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/pkg/utils"
)

var UploadedFiles []schema.File

const UploadedFilesFile = "uploadedFiles.json"

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
			return c.Status(fiber.StatusInternalServerError).SendString("Failed to save file: " + bluemonday.StrictPolicy().Sanitize(err.Error()))
		}

		f := schema.File{
			ID:        fmt.Sprintf("file-%d", getNextFileId()),
			Object:    "file",
			Bytes:     int(file.Size),
			CreatedAt: time.Now(),
			Filename:  file.Filename,
			Purpose:   purpose,
		}

		UploadedFiles = append(UploadedFiles, f)
		utils.SaveConfig(appConfig.UploadDir, UploadedFilesFile, UploadedFiles)
		return c.Status(fiber.StatusOK).JSON(f)
	}
}

var currentFileId int64 = 0

func getNextFileId() int64 {
	atomic.AddInt64(&currentId, 1)
	return currentId
}

// ListFilesEndpoint https://platform.openai.com/docs/api-reference/files/list
// @Summary List files.
// @Success 200 {object} schema.ListFiles "Response"
// @Router /v1/files [get]
func ListFilesEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {

	return func(c *fiber.Ctx) error {
		var listFiles schema.ListFiles

		purpose := c.Query("purpose")
		if purpose == "" {
			listFiles.Data = UploadedFiles
		} else {
			for _, f := range UploadedFiles {
				if purpose == f.Purpose {
					listFiles.Data = append(listFiles.Data, f)
				}
			}
		}
		listFiles.Object = "list"
		return c.Status(fiber.StatusOK).JSON(listFiles)
	}
}

func getFileFromRequest(c *fiber.Ctx) (*schema.File, error) {
	id := c.Params("file_id")
	if id == "" {
		return nil, fmt.Errorf("file_id parameter is required")
	}

	for _, f := range UploadedFiles {
		if id == f.ID {
			return &f, nil
		}
	}

	return nil, fmt.Errorf("unable to find file id %s", id)
}

// GetFilesEndpoint is the OpenAI API endpoint to get files https://platform.openai.com/docs/api-reference/files/retrieve
// @Summary Returns information about a specific file.
// @Success 200 {object} schema.File "Response"
// @Router /v1/files/{file_id} [get]
func GetFilesEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(bluemonday.StrictPolicy().Sanitize(err.Error()))
		}

		return c.JSON(file)
	}
}

type DeleteStatus struct {
	Id      string
	Object  string
	Deleted bool
}

// DeleteFilesEndpoint is the OpenAI API endpoint to delete files https://platform.openai.com/docs/api-reference/files/delete
// @Summary Delete a file.
// @Success 200 {object} DeleteStatus "Response"
// @Router /v1/files/{file_id} [delete]
func DeleteFilesEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {

	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(bluemonday.StrictPolicy().Sanitize(err.Error()))
		}

		err = os.Remove(filepath.Join(appConfig.UploadDir, file.Filename))
		if err != nil {
			// If the file doesn't exist then we should just continue to remove it
			if !errors.Is(err, os.ErrNotExist) {
				return c.Status(fiber.StatusInternalServerError).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Unable to delete file: %s, %v", file.Filename, err)))
			}
		}

		// Remove upload from list
		for i, f := range UploadedFiles {
			if f.ID == file.ID {
				UploadedFiles = append(UploadedFiles[:i], UploadedFiles[i+1:]...)
				break
			}
		}

		utils.SaveConfig(appConfig.UploadDir, UploadedFilesFile, UploadedFiles)
		return c.JSON(DeleteStatus{
			Id:      file.ID,
			Object:  "file",
			Deleted: true,
		})
	}
}

// GetFilesContentsEndpoint is the OpenAI API endpoint to get files content https://platform.openai.com/docs/api-reference/files/retrieve-contents
// @Summary Returns information about a specific file.
// @Success	200		{string}	binary				"file"
// @Router /v1/files/{file_id}/content [get]
// GetFilesContentsEndpoint
func GetFilesContentsEndpoint(cm *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(bluemonday.StrictPolicy().Sanitize(err.Error()))
		}

		fileContents, err := os.ReadFile(filepath.Join(appConfig.UploadDir, file.Filename))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(bluemonday.StrictPolicy().Sanitize(err.Error()))
		}

		return c.Send(fileContents)
	}
}
