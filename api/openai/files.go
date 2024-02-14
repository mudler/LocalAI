package openai

import (
	"fmt"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/gofiber/fiber/v2"
	"os"
	"path/filepath"
	"time"
)

var uploadedFiles []File

// File represents the structure of a file object from the OpenAI API.
type File struct {
	ID        string    `json:"id"`         // Unique identifier for the file
	Object    string    `json:"object"`     // Type of the object (e.g., "file")
	Bytes     int       `json:"bytes"`      // Size of the file in bytes
	CreatedAt time.Time `json:"created_at"` // The time at which the file was created
	Filename  string    `json:"filename"`   // The name of the file
	Purpose   string    `json:"purpose"`    // The purpose of the file (e.g., "fine-tune", "classifications", etc.)
}

// UploadFilesEndpoint https://platform.openai.com/docs/api-reference/files/create
func UploadFilesEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := c.FormFile("file")
		if err != nil {
			return err
		}

		// Check the file size
		if file.Size > int64(o.UploadLimitMB) {
			return c.Status(fiber.StatusBadRequest).SendString(fmt.Sprintf("File size %d exceeds upload limit %d", file.Size, o.UploadLimitMB))
		}

		purpose := c.FormValue("purpose", "") //TODO put in purpose dirs
		if purpose == "" {
			return c.Status(fiber.StatusBadRequest).SendString("Purpose is not defined")
		}

		// Sanitize the filename to prevent directory traversal
		filename := utils.SanitizeFileName(file.Filename)

		// Create the directory if it doesn't exist
		err = os.MkdirAll(o.UploadDir, os.ModePerm)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString("Failed to create directory: " + err.Error())
		}

		savePath := filepath.Join(o.UploadDir, filename)

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

		return c.Status(fiber.StatusOK).JSON(f)
	}
}

// ListFilesEndpoint https://platform.openai.com/docs/api-reference/files/list
func ListFilesEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	type ListFiles struct {
		data   []File
		object string
	}

	return func(c *fiber.Ctx) error {
		var listFiles ListFiles

		purpose := c.Query("purpose")
		if purpose == "" {
			listFiles.data = uploadedFiles
		}
		for _, f := range uploadedFiles {
			if purpose == f.Purpose {
				listFiles.data = append(listFiles.data, f)
			}
		}

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
func GetFilesEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		return c.JSON(file)
	}
}

// DeleteFilesEndpoint https://platform.openai.com/docs/api-reference/files/delete
func DeleteFilesEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	type DeleteStatus struct {
		id     string
		object string
		delete bool
	}

	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		err = os.Remove(filepath.Join(o.UploadDir, file.Filename))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(fmt.Sprintf("Unable to delete file: %s, %v", file.Filename, err))
		}

		// Remove upload from list
		for i, f := range uploadedFiles {
			if f.ID == file.ID {
				uploadedFiles = append(uploadedFiles[:i], uploadedFiles[i+1:]...)
				break
			}
		}

		return c.JSON(DeleteStatus{
			id:     file.ID,
			object: "File",
			delete: true,
		})
	}
}

// GetFilesContentsEndpoint https://platform.openai.com/docs/api-reference/files/retrieve-contents
func GetFilesContentsEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		file, err := getFileFromRequest(c)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		fileContents, err := os.ReadFile(filepath.Join(o.UploadDir, file.Filename))
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		return c.Send(fileContents)
	}
}
