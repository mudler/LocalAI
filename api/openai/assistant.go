package openai

import (
	"fmt"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/options"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ToolType defines a type for tool options
type ToolType string

const (
	CodeInterpreter ToolType = "code_interpreter"
	Retrieval       ToolType = "retrieval"
	Function        ToolType = "function"

	MaxCharacterInstructions  = 32768
	MaxCharacterDescription   = 512
	MaxCharacterName          = 256
	MaxToolsSize              = 128
	MaxFileIdSize             = 20
	MaxCharacterMetadataKey   = 64
	MaxCharacterMetadataValue = 512

	MaxLengthRandomID = 0
)

type Tool struct {
	Type ToolType `json:"type"`
}

// Assistant represents the structure of an assistant object from the OpenAI API.
type Assistant struct {
	ID           string            `json:"id"`                     // The unique identifier of the assistant.
	Object       string            `json:"object"`                 // Object type, which is "assistant".
	Created      int64             `json:"created"`                // The time at which the assistant was created.
	Model        string            `json:"model"`                  // The model ID used by the assistant.
	Name         string            `json:"name,omitempty"`         // The name of the assistant.
	Description  string            `json:"description,omitempty"`  // The description of the assistant.
	Instructions string            `json:"instructions,omitempty"` // The system instructions that the assistant uses.
	Tools        []Tool            `json:"tools,omitempty"`        // A list of tools enabled on the assistant.
	FileIDs      []string          `json:"file_ids,omitempty"`     // A list of file IDs attached to this assistant.
	Metadata     map[string]string `json:"metadata,omitempty"`     // Set of key-value pairs attached to the assistant.
}

var (
	assistants = []Assistant{} // better to return empty array instead of "null"
)

type AssistantRequest struct {
	Model        string            `json:"model"`
	Name         string            `json:"name,omitempty"`
	Description  string            `json:"description,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Tools        []Tool            `json:"tools,omitempty"`
	FileIDs      []string          `json:"file_ids,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func CreateAssistantEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		request := new(AssistantRequest)
		if err := c.BodyParser(request); err != nil {
			log.Warn().AnErr("Unable to parse AssistantRequest", err)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot parse JSON"})
		}

		if !modelExists(o, request.Model) {
			log.Warn().Msgf("Model: %s was not found in list of models.", request.Model)
			return c.Status(fiber.StatusBadRequest).SendString("Model " + request.Model + " not found")
		}

		assistant := Assistant{
			ID:           "asst_" + generateRandomID(MaxLengthRandomID),
			Object:       "assistant",
			Created:      time.Now().Unix(),
			Model:        request.Model,
			Name:         request.Name,
			Description:  request.Description,
			Instructions: request.Instructions,
			Tools:        request.Tools,
			FileIDs:      request.FileIDs,
			Metadata:     request.Metadata,
		}

		assistants = append(assistants, assistant)

		return c.Status(fiber.StatusOK).JSON(assistant)
	}
}

func generateRandomID(maxLength int) string {
	newUUID, err := uuid.NewUUID()
	if err != nil {
		log.Error().Msgf("Failed to generate UUID: %v", err)
		return ""
	}

	uuidStr := newUUID.String()
	if maxLength > 0 && len(uuidStr) > maxLength {
		return uuidStr[:maxLength]
	}
	return uuidStr
}

func ListAssistantsEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// Parse query parameters
		limitQuery := c.Query("limit", "20")
		orderQuery := c.Query("order", "desc")
		afterQuery := c.Query("after")
		beforeQuery := c.Query("before")

		// Convert string limit to integer
		limit, err := strconv.Atoi(limitQuery)
		if err != nil {
			return c.Status(http.StatusBadRequest).SendString(err.Error())
		}

		// Sort assistants
		sort.SliceStable(assistants, func(i, j int) bool {
			if orderQuery == "asc" {
				return assistants[i].Created < assistants[j].Created
			}
			return assistants[i].Created > assistants[j].Created
		})

		// After and before cursors
		if afterQuery != "" {
			assistants = filterAssistantsAfterID(assistants, afterQuery)
		}
		if beforeQuery != "" {
			assistants = filterAssistantsBeforeID(assistants, beforeQuery)
		}

		// Apply limit
		if limit < len(assistants) {
			assistants = assistants[:limit]
		}

		return c.JSON(assistants)
	}
}

// FilterAssistantsBeforeID filters out those assistants whose ID comes before the given ID
// We assume that the assistants are already sorted
func filterAssistantsBeforeID(assistants []Assistant, id string) []Assistant {
	for i, assistant := range assistants {
		if strings.Compare(assistant.ID, id) == 0 {
			if i != 0 {
				return assistants[:i]
			}
			return []Assistant{}
		}
	}
	return assistants
}

// FilterAssistantsAfterID filters out those assistants whose ID comes after the given ID
// We assume that the assistants are already sorted
func filterAssistantsAfterID(assistants []Assistant, id string) []Assistant {
	for i, assistant := range assistants {
		if strings.Compare(assistant.ID, id) == 0 {
			if i != len(assistants)-1 {
				return assistants[i+1:]
			}
			return []Assistant{}
		}
	}
	return assistants
}

func modelExists(o *options.Option, modelName string) (found bool) {
	found = false
	models, err := o.Loader.ListModels()
	if err != nil {
		return
	}

	for _, model := range models {
		if model == modelName {
			found = true
			return
		}
	}
	return
}

func DeleteAssistantEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	type DeleteAssistantResponse struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Deleted bool   `json:"deleted"`
	}

	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		for i, assistant := range assistants {
			if assistant.ID == assistantID {
				assistants = append(assistants[:i], assistants[i+1:]...)
				return c.Status(fiber.StatusOK).JSON(DeleteAssistantResponse{
					ID:      assistantID,
					Object:  "assistant.deleted",
					Deleted: true,
				})
			}
		}

		log.Warn().Msgf("Unable to find assistant %s for deletion", assistantID)
		return c.Status(fiber.StatusNotFound).JSON(DeleteAssistantResponse{
			ID:      assistantID,
			Object:  "assistant.deleted",
			Deleted: false,
		})
	}
}

func GetAssistantEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		for _, assistant := range assistants {
			if assistant.ID == assistantID {
				return c.Status(fiber.StatusOK).JSON(assistant)
			}
		}

		return c.Status(fiber.StatusNotFound).SendString(fmt.Sprintf("Unable to find assistant with id: %s", assistantID))
	}
}

type AssistantFile struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	CreatedAt   int64  `json:"created_at"`
	AssistantID string `json:"assistant_id"`
}

var assistantFiles []AssistantFile

func CreateAssistantFileEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	type AssistantFileRequest struct {
		FileID string `json:"file_id"`
	}

	return func(c *fiber.Ctx) error {
		request := new(AssistantFileRequest)
		if err := c.BodyParser(request); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot parse JSON"})
		}

		assistantID := c.Query("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		for _, assistant := range assistants {
			if assistant.ID == assistantID {
				if len(assistant.FileIDs) > MaxFileIdSize {
					return c.Status(fiber.StatusBadRequest).SendString(fmt.Sprintf("Max files %d for assistant %s reached.", MaxFileIdSize, assistant.Name))
				}

				for _, file := range UploadedFiles {
					if file.ID == request.FileID {
						assistant.FileIDs = append(assistant.FileIDs, request.FileID)
						assistantFile := AssistantFile{
							ID:          file.ID,
							Object:      "assistant.file",
							CreatedAt:   time.Now().Unix(),
							AssistantID: assistant.ID,
						}
						assistantFiles = append(assistantFiles, assistantFile)
						return c.Status(fiber.StatusOK).JSON(assistantFile)
					}
				}

				return c.Status(fiber.StatusNotFound).SendString(fmt.Sprintf("Unable to find file_id: %s", request.FileID))
			}
		}

		return c.Status(fiber.StatusNotFound).SendString(fmt.Sprintf("Unable to find "))
	}
}

func ListAssistantFilesEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	type ListAssistantFiles struct {
		Data   []File
		Object string
	}

	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		limitQuery := c.Query("limit", "20")
		order := c.Query("order", "desc")
		limit, err := strconv.Atoi(limitQuery)
		if err != nil || limit < 1 || limit > 100 {
			limit = 20 // Default to 20 if there's an error or the limit is out of bounds
		}

		// Sort files by CreatedAt depending on the order query parameter
		if order == "asc" {
			sort.Slice(assistantFiles, func(i, j int) bool {
				return assistantFiles[i].CreatedAt < assistantFiles[j].CreatedAt
			})
		} else { // default to "desc"
			sort.Slice(assistantFiles, func(i, j int) bool {
				return assistantFiles[i].CreatedAt > assistantFiles[j].CreatedAt
			})
		}

		// Limit the number of files returned
		var limitedFiles []AssistantFile
		hasMore := false
		if len(assistantFiles) > limit {
			hasMore = true
			limitedFiles = assistantFiles[:limit]
		} else {
			limitedFiles = assistantFiles
		}

		response := map[string]interface{}{
			"object": "list",
			"data":   limitedFiles,
			"first_id": func() string {
				if len(limitedFiles) > 0 {
					return limitedFiles[0].ID
				}
				return ""
			}(),
			"last_id": func() string {
				if len(limitedFiles) > 0 {
					return limitedFiles[len(limitedFiles)-1].ID
				}
				return ""
			}(),
			"has_more": hasMore,
		}

		return c.Status(fiber.StatusOK).JSON(response)
	}
}

func ModifyAssistantEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		request := new(AssistantRequest)
		if err := c.BodyParser(request); err != nil {
			log.Warn().AnErr("Unable to parse AssistantRequest", err)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot parse JSON"})
		}

		assistantID := c.Params("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		for i, assistant := range assistants {
			if assistant.ID == assistantID {
				newAssistant := Assistant{
					ID:           assistantID,
					Object:       assistant.Object,
					Created:      assistant.Created,
					Model:        request.Model,
					Name:         request.Name,
					Description:  request.Description,
					Instructions: request.Instructions,
					Tools:        request.Tools,
					FileIDs:      request.FileIDs, // todo: should probably verify fileids exist
					Metadata:     request.Metadata,
				}

				// Remove old one and replace with new one
				assistants = append(assistants[:i], assistants[i+1:]...)
				assistants = append(assistants, newAssistant)
				return c.Status(fiber.StatusOK).JSON(newAssistant)
			}
		}
		return c.Status(fiber.StatusNotFound).SendString(fmt.Sprintf("Unable to find assistant with id: %s", assistantID))
	}
}

func DeleteAssistantFileEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	type DeleteAssistantFileResponse struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Deleted bool   `json:"deleted"`
	}
	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		fileId := c.Params("file_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id and file_id are required")
		}
		// First remove file from assistant
		for i, assistant := range assistants {
			if assistant.ID == assistantID {
				for j, fileId := range assistant.FileIDs {
					if fileId == fileId {
						assistants[i].FileIDs = append(assistants[i].FileIDs[:j], assistants[i].FileIDs[j+1:]...)

						// Check if the file exists in the assistantFiles slice
						for i, assistantFile := range assistantFiles {
							if assistantFile.ID == fileId {
								// Remove the file from the assistantFiles slice
								assistantFiles = append(assistantFiles[:i], assistantFiles[i+1:]...)
								return c.Status(fiber.StatusOK).JSON(DeleteAssistantFileResponse{
									ID:      fileId,
									Object:  "assistant.file.deleted",
									Deleted: true,
								})
							}
						}
					}
				}

				log.Warn().Msgf("Unable to locate file_id: %s in assistants: %s", fileId, assistantID)
				return c.Status(fiber.StatusNotFound).JSON(DeleteAssistantFileResponse{
					ID:      fileId,
					Object:  "assistant.file.deleted",
					Deleted: false,
				})
			}
		}
		log.Warn().Msgf("Unable to find assistant: %s", assistantID)

		return c.Status(fiber.StatusNotFound).JSON(DeleteAssistantFileResponse{
			ID:      fileId,
			Object:  "assistant.file.deleted",
			Deleted: false,
		})
	}
}

func GetAssistantFileEndpoint(cm *config.ConfigLoader, o *options.Option) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		fileId := c.Params("file_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id and file_id are required")
		}

		for _, assistantFile := range assistantFiles {
			if assistantFile.AssistantID == assistantID {
				if assistantFile.ID == fileId {
					return c.Status(fiber.StatusOK).JSON(assistantFile)
				}
				return c.Status(fiber.StatusNotFound).SendString(fmt.Sprintf("Unable to find assistant file with file_id: %s", fileId))
			}
		}
		return c.Status(fiber.StatusNotFound).SendString(fmt.Sprintf("Unable to find assistant file with assistant_id: %s", assistantID))
	}
}
