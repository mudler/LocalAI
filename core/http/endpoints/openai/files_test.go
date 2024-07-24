package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	"github.com/gofiber/fiber/v2"
	utils2 "github.com/mudler/LocalAI/pkg/utils"
	"github.com/stretchr/testify/assert"

	"testing"
)

func startUpApp() (app *fiber.App, option *config.ApplicationConfig, loader *config.BackendConfigLoader) {
	// Preparing the mocked objects
	loader = &config.BackendConfigLoader{}

	option = &config.ApplicationConfig{
		UploadLimitMB: 10,
		UploadDir:     "test_dir",
	}

	_ = os.RemoveAll(option.UploadDir)

	app = fiber.New(fiber.Config{
		BodyLimit: 20 * 1024 * 1024, // sets the limit to 20MB.
	})

	// Create a Test Server
	app.Post("/files", UploadFilesEndpoint(loader, option))
	app.Get("/files", ListFilesEndpoint(loader, option))
	app.Get("/files/:file_id", GetFilesEndpoint(loader, option))
	app.Delete("/files/:file_id", DeleteFilesEndpoint(loader, option))
	app.Get("/files/:file_id/content", GetFilesContentsEndpoint(loader, option))

	return
}

func TestUploadFileExceedSizeLimit(t *testing.T) {
	// Preparing the mocked objects
	loader := &config.BackendConfigLoader{}

	option := &config.ApplicationConfig{
		UploadLimitMB: 10,
		UploadDir:     "test_dir",
	}

	_ = os.RemoveAll(option.UploadDir)

	app := fiber.New(fiber.Config{
		BodyLimit: 20 * 1024 * 1024, // sets the limit to 20MB.
	})

	// Create a Test Server
	app.Post("/files", UploadFilesEndpoint(loader, option))
	app.Get("/files", ListFilesEndpoint(loader, option))
	app.Get("/files/:file_id", GetFilesEndpoint(loader, option))
	app.Delete("/files/:file_id", DeleteFilesEndpoint(loader, option))
	app.Get("/files/:file_id/content", GetFilesContentsEndpoint(loader, option))

	t.Run("UploadFilesEndpoint file size exceeds limit", func(t *testing.T) {
		t.Cleanup(tearDown())
		resp, err := CallFilesUploadEndpoint(t, app, "foo.txt", "file", "fine-tune", 11, option)
		assert.NoError(t, err)

		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, bodyToString(resp, t), "exceeds upload limit")
	})
	t.Run("UploadFilesEndpoint purpose not defined", func(t *testing.T) {
		t.Cleanup(tearDown())
		resp, _ := CallFilesUploadEndpoint(t, app, "foo.txt", "file", "", 5, option)

		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, bodyToString(resp, t), "Purpose is not defined")
	})
	t.Run("UploadFilesEndpoint file already exists", func(t *testing.T) {
		t.Cleanup(tearDown())
		f1 := CallFilesUploadEndpointWithCleanup(t, app, "foo.txt", "file", "fine-tune", 5, option)

		resp, err := CallFilesUploadEndpoint(t, app, "foo.txt", "file", "fine-tune", 5, option)
		fmt.Println(f1)
		fmt.Printf("ERror: %v\n", err)
		fmt.Printf("resp: %+v\n", resp)

		assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, bodyToString(resp, t), "File already exists")
	})
	t.Run("UploadFilesEndpoint file uploaded successfully", func(t *testing.T) {
		t.Cleanup(tearDown())
		file := CallFilesUploadEndpointWithCleanup(t, app, "test.txt", "file", "fine-tune", 5, option)

		// Check if file exists in the disk
		testName := strings.Split(t.Name(), "/")[1]
		fileName := testName + "-test.txt"
		filePath := filepath.Join(option.UploadDir, utils2.SanitizeFileName(fileName))
		_, err := os.Stat(filePath)

		assert.False(t, os.IsNotExist(err))
		assert.Equal(t, file.Bytes, 5242880)
		assert.NotEmpty(t, file.CreatedAt)
		assert.Equal(t, file.Filename, fileName)
		assert.Equal(t, file.Purpose, "fine-tune")
	})
	t.Run("ListFilesEndpoint without purpose parameter", func(t *testing.T) {
		t.Cleanup(tearDown())
		resp, err := CallListFilesEndpoint(t, app, "")
		assert.NoError(t, err)

		assert.Equal(t, 200, resp.StatusCode)

		listFiles := responseToListFile(t, resp)
		if len(listFiles.Data) != len(UploadedFiles) {
			t.Errorf("Expected %v files, got %v files", len(UploadedFiles), len(listFiles.Data))
		}
	})
	t.Run("ListFilesEndpoint with valid purpose parameter", func(t *testing.T) {
		t.Cleanup(tearDown())
		_ = CallFilesUploadEndpointWithCleanup(t, app, "test.txt", "file", "fine-tune", 5, option)

		resp, err := CallListFilesEndpoint(t, app, "fine-tune")
		assert.NoError(t, err)

		listFiles := responseToListFile(t, resp)
		if len(listFiles.Data) != 1 {
			t.Errorf("Expected 1 file, got %v files", len(listFiles.Data))
		}
	})
	t.Run("ListFilesEndpoint with invalid query parameter", func(t *testing.T) {
		t.Cleanup(tearDown())
		resp, err := CallListFilesEndpoint(t, app, "not-so-fine-tune")
		assert.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)

		listFiles := responseToListFile(t, resp)

		if len(listFiles.Data) != 0 {
			t.Errorf("Expected 0 file, got %v files", len(listFiles.Data))
		}
	})
	t.Run("GetFilesContentsEndpoint get file content", func(t *testing.T) {
		t.Cleanup(tearDown())
		req := httptest.NewRequest("GET", "/files", nil)
		resp, _ := app.Test(req)
		assert.Equal(t, 200, resp.StatusCode)

		var listFiles schema.ListFiles
		if err := json.Unmarshal(bodyToByteArray(resp, t), &listFiles); err != nil {
			t.Errorf("Failed to decode response: %v", err)
			return
		}

		if len(listFiles.Data) != 0 {
			t.Errorf("Expected 0 file, got %v files", len(listFiles.Data))
		}
	})
}

func CallListFilesEndpoint(t *testing.T, app *fiber.App, purpose string) (*http.Response, error) {
	var target string
	if purpose != "" {
		target = fmt.Sprintf("/files?purpose=%s", purpose)
	} else {
		target = "/files"
	}
	req := httptest.NewRequest("GET", target, nil)
	return app.Test(req)
}

func CallFilesContentEndpoint(t *testing.T, app *fiber.App, fileId string) (*http.Response, error) {
	request := httptest.NewRequest("GET", "/files?file_id="+fileId, nil)
	return app.Test(request)
}

func CallFilesUploadEndpoint(t *testing.T, app *fiber.App, fileName, tag, purpose string, fileSize int, appConfig *config.ApplicationConfig) (*http.Response, error) {
	testName := strings.Split(t.Name(), "/")[1]

	// Create a file that exceeds the limit
	file := createTestFile(t, testName+"-"+fileName, fileSize, appConfig)

	// Creating a new HTTP Request
	body, writer := newMultipartFile(file.Name(), tag, purpose)

	req := httptest.NewRequest(http.MethodPost, "/files", body)
	req.Header.Set(fiber.HeaderContentType, writer.FormDataContentType())
	return app.Test(req)
}

func CallFilesUploadEndpointWithCleanup(t *testing.T, app *fiber.App, fileName, tag, purpose string, fileSize int, appConfig *config.ApplicationConfig) schema.File {
	// Create a file that exceeds the limit
	testName := strings.Split(t.Name(), "/")[1]
	file := createTestFile(t, testName+"-"+fileName, fileSize, appConfig)

	// Creating a new HTTP Request
	body, writer := newMultipartFile(file.Name(), tag, purpose)

	req := httptest.NewRequest(http.MethodPost, "/files", body)
	req.Header.Set(fiber.HeaderContentType, writer.FormDataContentType())
	resp, err := app.Test(req)
	assert.NoError(t, err)
	f := responseToFile(t, resp)

	//id := f.ID
	//t.Cleanup(func() {
	//	_, err := CallFilesDeleteEndpoint(t, app, id)
	//	assert.NoError(t, err)
	//	assert.Empty(t, UploadedFiles)
	//})

	return f

}

func CallFilesDeleteEndpoint(t *testing.T, app *fiber.App, fileId string) (*http.Response, error) {
	target := fmt.Sprintf("/files/%s", fileId)
	req := httptest.NewRequest(http.MethodDelete, target, nil)
	return app.Test(req)
}

// Helper to create multi-part file
func newMultipartFile(filePath, tag, purpose string) (*strings.Reader, *multipart.Writer) {
	body := new(strings.Builder)
	writer := multipart.NewWriter(body)
	file, _ := os.Open(filePath)
	defer file.Close()
	part, _ := writer.CreateFormFile(tag, filepath.Base(filePath))
	io.Copy(part, file)

	if purpose != "" {
		_ = writer.WriteField("purpose", purpose)
	}

	writer.Close()
	return strings.NewReader(body.String()), writer
}

// Helper to create test files
func createTestFile(t *testing.T, name string, sizeMB int, option *config.ApplicationConfig) *os.File {
	err := os.MkdirAll(option.UploadDir, 0750)
	if err != nil {

		t.Fatalf("Error MKDIR: %v", err)
	}

	file, err := os.Create(name)
	assert.NoError(t, err)
	file.WriteString(strings.Repeat("a", sizeMB*1024*1024)) // sizeMB MB File

	t.Cleanup(func() {
		os.Remove(name)
		os.RemoveAll(option.UploadDir)
	})
	return file
}

func bodyToString(resp *http.Response, t *testing.T) string {
	return string(bodyToByteArray(resp, t))
}

func bodyToByteArray(resp *http.Response, t *testing.T) []byte {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return bodyBytes
}

func responseToFile(t *testing.T, resp *http.Response) schema.File {
	var file schema.File
	responseToString := bodyToString(resp, t)

	err := json.NewDecoder(strings.NewReader(responseToString)).Decode(&file)
	if err != nil {
		t.Errorf("Failed to decode response: %s", err)
	}

	return file
}

func responseToListFile(t *testing.T, resp *http.Response) schema.ListFiles {
	var listFiles schema.ListFiles
	responseToString := bodyToString(resp, t)

	err := json.NewDecoder(strings.NewReader(responseToString)).Decode(&listFiles)
	if err != nil {
		log.Error().Err(err).Msg("failed to decode response")
	}

	return listFiles
}
