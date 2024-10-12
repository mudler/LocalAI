package openai

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	model "github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
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
	Assistants           = []Assistant{} // better to return empty array instead of "null"
	AssistantsConfigFile = "assistants.json"
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

// CreateAssistantEndpoint is the OpenAI Assistant API endpoint https://platform.openai.com/docs/api-reference/assistants/createAssistant
// @Summary Create an assistant with a model and instructions.
// @Param request body AssistantRequest true "query params"
// @Success 200 {object} Assistant "Response"
// @Router /v1/assistants [post]
func CreateAssistantEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		request := new(AssistantRequest)
		if err := c.BodyParser(request); err != nil {
			log.Warn().AnErr("Unable to parse AssistantRequest", err)
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot parse JSON"})
		}

		if !modelExists(cl, ml, request.Model) {
			log.Warn().Msgf("Model: %s was not found in list of models.", request.Model)
			return c.Status(fiber.StatusBadRequest).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Model %q not found", request.Model)))
		}

		if request.Tools == nil {
			request.Tools = []Tool{}
		}

		if request.FileIDs == nil {
			request.FileIDs = []string{}
		}

		if request.Metadata == nil {
			request.Metadata = make(map[string]string)
		}

		id := "asst_" + strconv.FormatInt(generateRandomID(), 10)

		assistant := Assistant{
			ID:           id,
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

		Assistants = append(Assistants, assistant)
		utils.SaveConfig(appConfig.ConfigsDir, AssistantsConfigFile, Assistants)
		return c.Status(fiber.StatusOK).JSON(assistant)
	}
}

var currentId int64 = 0

func generateRandomID() int64 {
	atomic.AddInt64(&currentId, 1)
	return currentId
}

// ListAssistantsEndpoint is the OpenAI Assistant API endpoint to list assistents https://platform.openai.com/docs/api-reference/assistants/listAssistants
// @Summary List available assistents
// @Param limit query int false "Limit the number of assistants returned"
// @Param order query string false "Order of assistants returned"
// @Param after query string false "Return assistants created after the given ID"
// @Param before query string false "Return assistants created before the given ID"
// @Success 200 {object} []Assistant "Response"
// @Router /v1/assistants [get]
func ListAssistantsEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// Because we're altering the existing assistants list we should just duplicate it for now.
		returnAssistants := Assistants
		// Parse query parameters
		limitQuery := c.Query("limit", "20")
		orderQuery := c.Query("order", "desc")
		afterQuery := c.Query("after")
		beforeQuery := c.Query("before")

		// Convert string limit to integer
		limit, err := strconv.Atoi(limitQuery)
		if err != nil {
			return c.Status(http.StatusBadRequest).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Invalid limit query value: %s", limitQuery)))
		}

		// Sort assistants
		sort.SliceStable(returnAssistants, func(i, j int) bool {
			if orderQuery == "asc" {
				return returnAssistants[i].Created < returnAssistants[j].Created
			}
			return returnAssistants[i].Created > returnAssistants[j].Created
		})

		// After and before cursors
		if afterQuery != "" {
			returnAssistants = filterAssistantsAfterID(returnAssistants, afterQuery)
		}
		if beforeQuery != "" {
			returnAssistants = filterAssistantsBeforeID(returnAssistants, beforeQuery)
		}

		// Apply limit
		if limit < len(returnAssistants) {
			returnAssistants = returnAssistants[:limit]
		}

		return c.JSON(returnAssistants)
	}
}

// FilterAssistantsBeforeID filters out those assistants whose ID comes before the given ID
// We assume that the assistants are already sorted
func filterAssistantsBeforeID(assistants []Assistant, id string) []Assistant {
	idInt, err := strconv.Atoi(id)
	if err != nil {
		return assistants // Return original slice if invalid id format is provided
	}

	var filteredAssistants []Assistant

	for _, assistant := range assistants {
		aid, err := strconv.Atoi(strings.TrimPrefix(assistant.ID, "asst_"))
		if err != nil {
			continue // Skip if invalid id in assistant
		}

		if aid < idInt {
			filteredAssistants = append(filteredAssistants, assistant)
		}
	}

	return filteredAssistants
}

// FilterAssistantsAfterID filters out those assistants whose ID comes after the given ID
// We assume that the assistants are already sorted
func filterAssistantsAfterID(assistants []Assistant, id string) []Assistant {
	idInt, err := strconv.Atoi(id)
	if err != nil {
		return assistants // Return original slice if invalid id format is provided
	}

	var filteredAssistants []Assistant

	for _, assistant := range assistants {
		aid, err := strconv.Atoi(strings.TrimPrefix(assistant.ID, "asst_"))
		if err != nil {
			continue // Skip if invalid id in assistant
		}

		if aid > idInt {
			filteredAssistants = append(filteredAssistants, assistant)
		}
	}

	return filteredAssistants
}

func modelExists(cl *config.BackendConfigLoader, ml *model.ModelLoader, modelName string) (found bool) {
	found = false
	models, err := services.ListModels(cl, ml, config.NoFilterFn, services.SKIP_IF_CONFIGURED)
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

// DeleteAssistantEndpoint is the OpenAI Assistant API endpoint to delete assistents https://platform.openai.com/docs/api-reference/assistants/deleteAssistant
// @Summary Delete assistents
// @Success 200 {object} schema.DeleteAssistantResponse "Response"
// @Router /v1/assistants/{assistant_id} [delete]
func DeleteAssistantEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		for i, assistant := range Assistants {
			if assistant.ID == assistantID {
				Assistants = append(Assistants[:i], Assistants[i+1:]...)
				utils.SaveConfig(appConfig.ConfigsDir, AssistantsConfigFile, Assistants)
				return c.Status(fiber.StatusOK).JSON(schema.DeleteAssistantResponse{
					ID:      assistantID,
					Object:  "assistant.deleted",
					Deleted: true,
				})
			}
		}

		log.Warn().Msgf("Unable to find assistant %s for deletion", assistantID)
		return c.Status(fiber.StatusNotFound).JSON(schema.DeleteAssistantResponse{
			ID:      assistantID,
			Object:  "assistant.deleted",
			Deleted: false,
		})
	}
}

// GetAssistantEndpoint is the OpenAI Assistant API endpoint to get assistents https://platform.openai.com/docs/api-reference/assistants/getAssistant
// @Summary Get assistent data
// @Success 200 {object} Assistant "Response"
// @Router /v1/assistants/{assistant_id} [get]
func GetAssistantEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		for _, assistant := range Assistants {
			if assistant.ID == assistantID {
				return c.Status(fiber.StatusOK).JSON(assistant)
			}
		}

		return c.Status(fiber.StatusNotFound).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Unable to find assistant with id: %s", assistantID)))
	}
}

type AssistantFile struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	CreatedAt   int64  `json:"created_at"`
	AssistantID string `json:"assistant_id"`
}

var (
	AssistantFiles           []AssistantFile
	AssistantsFileConfigFile = "assistantsFile.json"
)

func CreateAssistantFileEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		request := new(schema.AssistantFileRequest)
		if err := c.BodyParser(request); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Cannot parse JSON"})
		}

		assistantID := c.Params("assistant_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id is required")
		}

		for _, assistant := range Assistants {
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
						AssistantFiles = append(AssistantFiles, assistantFile)
						utils.SaveConfig(appConfig.ConfigsDir, AssistantsFileConfigFile, AssistantFiles)
						return c.Status(fiber.StatusOK).JSON(assistantFile)
					}
				}

				return c.Status(fiber.StatusNotFound).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Unable to find file_id: %s", request.FileID)))
			}
		}

		return c.Status(fiber.StatusNotFound).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Unable to find %q", assistantID)))
	}
}

func ListAssistantFilesEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	type ListAssistantFiles struct {
		Data   []schema.File
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
			sort.Slice(AssistantFiles, func(i, j int) bool {
				return AssistantFiles[i].CreatedAt < AssistantFiles[j].CreatedAt
			})
		} else { // default to "desc"
			sort.Slice(AssistantFiles, func(i, j int) bool {
				return AssistantFiles[i].CreatedAt > AssistantFiles[j].CreatedAt
			})
		}

		// Limit the number of files returned
		var limitedFiles []AssistantFile
		hasMore := false
		if len(AssistantFiles) > limit {
			hasMore = true
			limitedFiles = AssistantFiles[:limit]
		} else {
			limitedFiles = AssistantFiles
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

func ModifyAssistantEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
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

		for i, assistant := range Assistants {
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
				Assistants = append(Assistants[:i], Assistants[i+1:]...)
				Assistants = append(Assistants, newAssistant)
				utils.SaveConfig(appConfig.ConfigsDir, AssistantsConfigFile, Assistants)
				return c.Status(fiber.StatusOK).JSON(newAssistant)
			}
		}
		return c.Status(fiber.StatusNotFound).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Unable to find assistant with id: %s", assistantID)))
	}
}

func DeleteAssistantFileEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		fileId := c.Params("file_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id and file_id are required")
		}
		// First remove file from assistant
		for i, assistant := range Assistants {
			if assistant.ID == assistantID {
				for j, fileId := range assistant.FileIDs {
					Assistants[i].FileIDs = append(Assistants[i].FileIDs[:j], Assistants[i].FileIDs[j+1:]...)

					// Check if the file exists in the assistantFiles slice
					for i, assistantFile := range AssistantFiles {
						if assistantFile.ID == fileId {
							// Remove the file from the assistantFiles slice
							AssistantFiles = append(AssistantFiles[:i], AssistantFiles[i+1:]...)
							utils.SaveConfig(appConfig.ConfigsDir, AssistantsFileConfigFile, AssistantFiles)
							return c.Status(fiber.StatusOK).JSON(schema.DeleteAssistantFileResponse{
								ID:      fileId,
								Object:  "assistant.file.deleted",
								Deleted: true,
							})
						}
					}
				}

				log.Warn().Msgf("Unable to locate file_id: %s in assistants: %s. Continuing to delete assistant file.", fileId, assistantID)
				for i, assistantFile := range AssistantFiles {
					if assistantFile.AssistantID == assistantID {

						AssistantFiles = append(AssistantFiles[:i], AssistantFiles[i+1:]...)
						utils.SaveConfig(appConfig.ConfigsDir, AssistantsFileConfigFile, AssistantFiles)

						return c.Status(fiber.StatusNotFound).JSON(schema.DeleteAssistantFileResponse{
							ID:      fileId,
							Object:  "assistant.file.deleted",
							Deleted: true,
						})
					}
				}
			}
		}
		log.Warn().Msgf("Unable to find assistant: %s", assistantID)

		return c.Status(fiber.StatusNotFound).JSON(schema.DeleteAssistantFileResponse{
			ID:      fileId,
			Object:  "assistant.file.deleted",
			Deleted: false,
		})
	}
}

func GetAssistantFileEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		assistantID := c.Params("assistant_id")
		fileId := c.Params("file_id")
		if assistantID == "" {
			return c.Status(fiber.StatusBadRequest).SendString("parameter assistant_id and file_id are required")
		}

		for _, assistantFile := range AssistantFiles {
			if assistantFile.AssistantID == assistantID {
				if assistantFile.ID == fileId {
					return c.Status(fiber.StatusOK).JSON(assistantFile)
				}
				return c.Status(fiber.StatusNotFound).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Unable to find assistant file with file_id: %s", fileId)))
			}
		}
		return c.Status(fiber.StatusNotFound).SendString(bluemonday.StrictPolicy().Sanitize(fmt.Sprintf("Unable to find assistant file with assistant_id: %s", assistantID)))
	}
}
