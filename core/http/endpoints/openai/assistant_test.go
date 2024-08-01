package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/stretchr/testify/assert"
)

var configsDir string = "/tmp/localai/configs"

type MockLoader struct {
	models []string
}

func tearDown() func() {
	return func() {
		UploadedFiles = []schema.File{}
		Assistants = []Assistant{}
		AssistantFiles = []AssistantFile{}
		_ = os.Remove(filepath.Join(configsDir, AssistantsConfigFile))
		_ = os.Remove(filepath.Join(configsDir, AssistantsFileConfigFile))
	}
}

func TestAssistantEndpoints(t *testing.T) {
	// Preparing the mocked objects
	cl := &config.BackendConfigLoader{}
	//configsDir := "/tmp/localai/configs"
	modelPath := "/tmp/localai/model"
	var ml = model.NewModelLoader(modelPath)

	appConfig := &config.ApplicationConfig{
		ConfigsDir:    configsDir,
		UploadLimitMB: 10,
		UploadDir:     "test_dir",
		ModelPath:     modelPath,
	}

	_ = os.RemoveAll(appConfig.ConfigsDir)
	_ = os.MkdirAll(appConfig.ConfigsDir, 0750)
	_ = os.MkdirAll(modelPath, 0750)
	os.Create(filepath.Join(modelPath, "ggml-gpt4all-j"))

	app := fiber.New(fiber.Config{
		BodyLimit: 20 * 1024 * 1024, // sets the limit to 20MB.
	})

	// Create a Test Server
	app.Get("/assistants", ListAssistantsEndpoint(cl, ml, appConfig))
	app.Post("/assistants", CreateAssistantEndpoint(cl, ml, appConfig))
	app.Delete("/assistants/:assistant_id", DeleteAssistantEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id", GetAssistantEndpoint(cl, ml, appConfig))
	app.Post("/assistants/:assistant_id", ModifyAssistantEndpoint(cl, ml, appConfig))

	app.Post("/files", UploadFilesEndpoint(cl, appConfig))
	app.Get("/assistants/:assistant_id/files", ListAssistantFilesEndpoint(cl, ml, appConfig))
	app.Post("/assistants/:assistant_id/files", CreateAssistantFileEndpoint(cl, ml, appConfig))
	app.Delete("/assistants/:assistant_id/files/:file_id", DeleteAssistantFileEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id/files/:file_id", GetAssistantFileEndpoint(cl, ml, appConfig))

	t.Run("CreateAssistantEndpoint", func(t *testing.T) {
		t.Cleanup(tearDown())
		ar := &AssistantRequest{
			Model:        "ggml-gpt4all-j",
			Name:         "3.5-turbo",
			Description:  "Test Assistant",
			Instructions: "You are computer science teacher answering student questions",
			Tools:        []Tool{{Type: Function}},
			FileIDs:      nil,
			Metadata:     nil,
		}

		resultAssistant, resp, err := createAssistant(app, *ar)
		assert.NoError(t, err)
		assert.Equal(t, fiber.StatusOK, resp.StatusCode)

		assert.Equal(t, 1, len(Assistants))
		//t.Cleanup(cleanupAllAssistants(t, app, []string{resultAssistant.ID}))

		assert.Equal(t, ar.Name, resultAssistant.Name)
		assert.Equal(t, ar.Model, resultAssistant.Model)
		assert.Equal(t, ar.Tools, resultAssistant.Tools)
		assert.Equal(t, ar.Description, resultAssistant.Description)
		assert.Equal(t, ar.Instructions, resultAssistant.Instructions)
		assert.Equal(t, ar.FileIDs, resultAssistant.FileIDs)
		assert.Equal(t, ar.Metadata, resultAssistant.Metadata)
	})

	t.Run("ListAssistantsEndpoint", func(t *testing.T) {
		var ids []string
		var resultAssistant []Assistant
		for i := 0; i < 4; i++ {
			ar := &AssistantRequest{
				Model:        "ggml-gpt4all-j",
				Name:         fmt.Sprintf("3.5-turbo-%d", i),
				Description:  fmt.Sprintf("Test Assistant - %d", i),
				Instructions: fmt.Sprintf("You are computer science teacher answering student questions - %d", i),
				Tools:        []Tool{{Type: Function}},
				FileIDs:      []string{"fid-1234"},
				Metadata:     map[string]string{"meta": "data"},
			}

			//var err error
			ra, _, err := createAssistant(app, *ar)
			// Because we create the assistants so fast all end up with the same created time.
			time.Sleep(time.Second)
			resultAssistant = append(resultAssistant, ra)
			assert.NoError(t, err)
			ids = append(ids, resultAssistant[i].ID)
		}

		t.Cleanup(cleanupAllAssistants(t, app, ids))

		tests := []struct {
			name                 string
			reqURL               string
			expectedStatus       int
			expectedResult       []Assistant
			expectedStringResult string
		}{
			{
				name:           "Valid Usage - limit only",
				reqURL:         "/assistants?limit=2",
				expectedStatus: http.StatusOK,
				expectedResult: Assistants[:2], // Expecting the first two assistants
			},
			{
				name:           "Valid Usage - order asc",
				reqURL:         "/assistants?order=asc",
				expectedStatus: http.StatusOK,
				expectedResult: Assistants, // Expecting all assistants in ascending order
			},
			{
				name:           "Valid Usage - order desc",
				reqURL:         "/assistants?order=desc",
				expectedStatus: http.StatusOK,
				expectedResult: []Assistant{Assistants[3], Assistants[2], Assistants[1], Assistants[0]}, // Expecting all assistants in descending order
			},
			{
				name:           "Valid Usage - after specific ID",
				reqURL:         "/assistants?after=2",
				expectedStatus: http.StatusOK,
				// Note this is correct because it's put in descending order already
				expectedResult: Assistants[:3], // Expecting assistants after (excluding) ID 2
			},
			{
				name:           "Valid Usage - before specific ID",
				reqURL:         "/assistants?before=4",
				expectedStatus: http.StatusOK,
				expectedResult: Assistants[2:], // Expecting assistants before (excluding) ID 3.
			},
			{
				name:                 "Invalid Usage - non-integer limit",
				reqURL:               "/assistants?limit=two",
				expectedStatus:       http.StatusBadRequest,
				expectedStringResult: "Invalid limit query value: two",
			},
			{
				name:           "Invalid Usage - non-existing id in after",
				reqURL:         "/assistants?after=100",
				expectedStatus: http.StatusOK,
				expectedResult: []Assistant(nil), // Expecting empty list as there are no IDs above 100
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, tt.reqURL, nil)
				response, err := app.Test(request)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedStatus, response.StatusCode)
				if tt.expectedStatus != fiber.StatusOK {
					all, _ := io.ReadAll(response.Body)
					assert.Equal(t, tt.expectedStringResult, string(all))
				} else {
					var result []Assistant
					err = json.NewDecoder(response.Body).Decode(&result)
					assert.NoError(t, err)

					assert.Equal(t, tt.expectedResult, result)
				}
			})
		}
	})

	t.Run("DeleteAssistantEndpoint", func(t *testing.T) {
		ar := &AssistantRequest{
			Model:        "ggml-gpt4all-j",
			Name:         "3.5-turbo",
			Description:  "Test Assistant",
			Instructions: "You are computer science teacher answering student questions",
			Tools:        []Tool{{Type: Function}},
			FileIDs:      nil,
			Metadata:     nil,
		}

		resultAssistant, _, err := createAssistant(app, *ar)
		assert.NoError(t, err)

		target := fmt.Sprintf("/assistants/%s", resultAssistant.ID)
		deleteReq := httptest.NewRequest(http.MethodDelete, target, nil)
		_, err = app.Test(deleteReq)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(Assistants))
	})

	t.Run("GetAssistantEndpoint", func(t *testing.T) {
		ar := &AssistantRequest{
			Model:        "ggml-gpt4all-j",
			Name:         "3.5-turbo",
			Description:  "Test Assistant",
			Instructions: "You are computer science teacher answering student questions",
			Tools:        []Tool{{Type: Function}},
			FileIDs:      nil,
			Metadata:     nil,
		}

		resultAssistant, _, err := createAssistant(app, *ar)
		assert.NoError(t, err)
		t.Cleanup(cleanupAllAssistants(t, app, []string{resultAssistant.ID}))

		target := fmt.Sprintf("/assistants/%s", resultAssistant.ID)
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response, err := app.Test(request)
		assert.NoError(t, err)

		var getAssistant Assistant
		err = json.NewDecoder(response.Body).Decode(&getAssistant)
		assert.NoError(t, err)

		assert.Equal(t, resultAssistant.ID, getAssistant.ID)
	})

	t.Run("ModifyAssistantEndpoint", func(t *testing.T) {
		ar := &AssistantRequest{
			Model:        "ggml-gpt4all-j",
			Name:         "3.5-turbo",
			Description:  "Test Assistant",
			Instructions: "You are computer science teacher answering student questions",
			Tools:        []Tool{{Type: Function}},
			FileIDs:      nil,
			Metadata:     nil,
		}

		resultAssistant, _, err := createAssistant(app, *ar)
		assert.NoError(t, err)

		modifiedAr := &AssistantRequest{
			Model:        "ggml-gpt4all-j",
			Name:         "4.0-turbo",
			Description:  "Modified Test Assistant",
			Instructions: "You are math teacher answering student questions",
			Tools:        []Tool{{Type: CodeInterpreter}},
			FileIDs:      nil,
			Metadata:     nil,
		}

		modifiedArJson, err := json.Marshal(modifiedAr)
		assert.NoError(t, err)

		target := fmt.Sprintf("/assistants/%s", resultAssistant.ID)
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(modifiedArJson)))
		request.Header.Set(fiber.HeaderContentType, "application/json")

		modifyResponse, err := app.Test(request)
		assert.NoError(t, err)
		var getAssistant Assistant
		err = json.NewDecoder(modifyResponse.Body).Decode(&getAssistant)
		assert.NoError(t, err)

		t.Cleanup(cleanupAllAssistants(t, app, []string{getAssistant.ID}))

		assert.Equal(t, resultAssistant.ID, getAssistant.ID) // IDs should match even if contents change
		assert.Equal(t, modifiedAr.Tools, getAssistant.Tools)
		assert.Equal(t, modifiedAr.Name, getAssistant.Name)
		assert.Equal(t, modifiedAr.Instructions, getAssistant.Instructions)
		assert.Equal(t, modifiedAr.Description, getAssistant.Description)
	})

	t.Run("CreateAssistantFileEndpoint", func(t *testing.T) {
		t.Cleanup(tearDown())
		file, assistant, err := createFileAndAssistant(t, app, appConfig)
		assert.NoError(t, err)

		afr := schema.AssistantFileRequest{FileID: file.ID}
		af, _, err := createAssistantFile(app, afr, assistant.ID)

		assert.NoError(t, err)
		assert.Equal(t, assistant.ID, af.AssistantID)
	})
	t.Run("ListAssistantFilesEndpoint", func(t *testing.T) {
		t.Cleanup(tearDown())
		file, assistant, err := createFileAndAssistant(t, app, appConfig)
		assert.NoError(t, err)

		afr := schema.AssistantFileRequest{FileID: file.ID}
		af, _, err := createAssistantFile(app, afr, assistant.ID)
		assert.NoError(t, err)

		assert.Equal(t, assistant.ID, af.AssistantID)
	})
	t.Run("GetAssistantFileEndpoint", func(t *testing.T) {
		t.Cleanup(tearDown())
		file, assistant, err := createFileAndAssistant(t, app, appConfig)
		assert.NoError(t, err)

		afr := schema.AssistantFileRequest{FileID: file.ID}
		af, _, err := createAssistantFile(app, afr, assistant.ID)
		assert.NoError(t, err)
		t.Cleanup(cleanupAssistantFile(t, app, af.ID, af.AssistantID))

		target := fmt.Sprintf("/assistants/%s/files/%s", assistant.ID, file.ID)
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response, err := app.Test(request)
		assert.NoError(t, err)

		var assistantFile AssistantFile
		err = json.NewDecoder(response.Body).Decode(&assistantFile)
		assert.NoError(t, err)

		assert.Equal(t, af.ID, assistantFile.ID)
		assert.Equal(t, af.AssistantID, assistantFile.AssistantID)
	})
	t.Run("DeleteAssistantFileEndpoint", func(t *testing.T) {
		t.Cleanup(tearDown())
		file, assistant, err := createFileAndAssistant(t, app, appConfig)
		assert.NoError(t, err)

		afr := schema.AssistantFileRequest{FileID: file.ID}
		af, _, err := createAssistantFile(app, afr, assistant.ID)
		assert.NoError(t, err)

		cleanupAssistantFile(t, app, af.ID, af.AssistantID)()

		assert.Empty(t, AssistantFiles)
	})

}

func createFileAndAssistant(t *testing.T, app *fiber.App, o *config.ApplicationConfig) (schema.File, Assistant, error) {
	ar := &AssistantRequest{
		Model:        "ggml-gpt4all-j",
		Name:         "3.5-turbo",
		Description:  "Test Assistant",
		Instructions: "You are computer science teacher answering student questions",
		Tools:        []Tool{{Type: Function}},
		FileIDs:      nil,
		Metadata:     nil,
	}

	assistant, _, err := createAssistant(app, *ar)
	if err != nil {
		return schema.File{}, Assistant{}, err
	}
	t.Cleanup(cleanupAllAssistants(t, app, []string{assistant.ID}))

	file := CallFilesUploadEndpointWithCleanup(t, app, "test.txt", "file", "fine-tune", 5, o)
	t.Cleanup(func() {
		_, err := CallFilesDeleteEndpoint(t, app, file.ID)
		assert.NoError(t, err)
	})
	return file, assistant, nil
}

func createAssistantFile(app *fiber.App, afr schema.AssistantFileRequest, assistantId string) (AssistantFile, *http.Response, error) {
	afrJson, err := json.Marshal(afr)
	if err != nil {
		return AssistantFile{}, nil, err
	}

	target := fmt.Sprintf("/assistants/%s/files", assistantId)
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(afrJson)))
	request.Header.Set(fiber.HeaderContentType, "application/json")
	request.Header.Set("OpenAi-Beta", "assistants=v1")

	resp, err := app.Test(request)
	if err != nil {
		return AssistantFile{}, resp, err
	}

	var assistantFile AssistantFile
	all, err := io.ReadAll(resp.Body)
	if err != nil {
		return AssistantFile{}, resp, err
	}
	err = json.NewDecoder(strings.NewReader(string(all))).Decode(&assistantFile)
	if err != nil {
		return AssistantFile{}, resp, err
	}

	return assistantFile, resp, nil
}

func createAssistant(app *fiber.App, ar AssistantRequest) (Assistant, *http.Response, error) {
	assistant, err := json.Marshal(ar)
	if err != nil {
		return Assistant{}, nil, err
	}

	request := httptest.NewRequest(http.MethodPost, "/assistants", strings.NewReader(string(assistant)))
	request.Header.Set(fiber.HeaderContentType, "application/json")
	request.Header.Set("OpenAi-Beta", "assistants=v1")

	resp, err := app.Test(request)
	if err != nil {
		return Assistant{}, resp, err
	}

	bodyString, err := io.ReadAll(resp.Body)
	if err != nil {
		return Assistant{}, resp, err
	}

	var resultAssistant Assistant
	err = json.NewDecoder(strings.NewReader(string(bodyString))).Decode(&resultAssistant)
	return resultAssistant, resp, err
}

func cleanupAllAssistants(t *testing.T, app *fiber.App, ids []string) func() {
	return func() {
		for _, assistant := range ids {
			target := fmt.Sprintf("/assistants/%s", assistant)
			deleteReq := httptest.NewRequest(http.MethodDelete, target, nil)
			_, err := app.Test(deleteReq)
			if err != nil {
				t.Fatalf("Failed to delete assistant %s: %v", assistant, err)
			}
		}
	}
}

func cleanupAssistantFile(t *testing.T, app *fiber.App, fileId, assistantId string) func() {
	return func() {
		target := fmt.Sprintf("/assistants/%s/files/%s", assistantId, fileId)
		request := httptest.NewRequest(http.MethodDelete, target, nil)
		request.Header.Set(fiber.HeaderContentType, "application/json")
		request.Header.Set("OpenAi-Beta", "assistants=v1")

		resp, err := app.Test(request)
		assert.NoError(t, err)

		var dafr schema.DeleteAssistantFileResponse
		err = json.NewDecoder(resp.Body).Decode(&dafr)
		assert.NoError(t, err)
		assert.True(t, dafr.Deleted)
	}
}
