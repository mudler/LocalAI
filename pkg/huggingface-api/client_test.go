package hfapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

var _ = Describe("HuggingFace API Client", func() {
	var (
		client *hfapi.Client
		server *httptest.Server
	)

	BeforeEach(func() {
		client = hfapi.NewClient()
	})

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Context("when creating a new client", func() {
		It("should initialize with correct base URL", func() {
			Expect(client).ToNot(BeNil())
			Expect(client.BaseURL()).To(Equal("https://huggingface.co/api/models"))
		})
	})

	Context("when searching for models", func() {
		BeforeEach(func() {
			// Mock response data
			mockResponse := `[
				{
					"modelId": "test-model-1",
					"author": "test-author",
					"downloads": 1000,
					"lastModified": "2024-01-01T00:00:00.000Z",
					"pipelineTag": "text-generation",
					"private": false,
					"tags": ["gguf", "llama"],
					"createdAt": "2024-01-01T00:00:00.000Z",
					"updatedAt": "2024-01-01T00:00:00.000Z",
					"sha": "abc123",
					"config": {},
					"model_index": "test-index",
					"library_name": "transformers",
					"mask_token": null,
					"tokenizer_class": "LlamaTokenizer"
				},
				{
					"modelId": "test-model-2",
					"author": "test-author-2",
					"downloads": 2000,
					"lastModified": "2024-01-02T00:00:00.000Z",
					"pipelineTag": "text-generation",
					"private": false,
					"tags": ["gguf", "mistral"],
					"createdAt": "2024-01-02T00:00:00.000Z",
					"updatedAt": "2024-01-02T00:00:00.000Z",
					"sha": "def456",
					"config": {},
					"model_index": "test-index-2",
					"library_name": "transformers",
					"mask_token": null,
					"tokenizer_class": "MistralTokenizer"
				}
			]`

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request parameters
				Expect(r.URL.Query().Get("sort")).To(Equal("lastModified"))
				Expect(r.URL.Query().Get("direction")).To(Equal("-1"))
				Expect(r.URL.Query().Get("limit")).To(Equal("30"))
				Expect(r.URL.Query().Get("search")).To(Equal("GGUF"))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(mockResponse))
			}))

			// Override the client's base URL to use our mock server
			client.SetBaseURL(server.URL)
		})

		It("should successfully search for models", func() {
			params := hfapi.SearchParams{
				Sort:      "lastModified",
				Direction: -1,
				Limit:     30,
				Search:    "GGUF",
			}

			models, err := client.SearchModels(params)

			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(2))

			// Verify first model
			Expect(models[0].ModelID).To(Equal("test-model-1"))
			Expect(models[0].Author).To(Equal("test-author"))
			Expect(models[0].Downloads).To(Equal(1000))
			Expect(models[0].PipelineTag).To(Equal("text-generation"))
			Expect(models[0].Private).To(BeFalse())
			Expect(models[0].Tags).To(ContainElements("gguf", "llama"))

			// Verify second model
			Expect(models[1].ModelID).To(Equal("test-model-2"))
			Expect(models[1].Author).To(Equal("test-author-2"))
			Expect(models[1].Downloads).To(Equal(2000))
			Expect(models[1].Tags).To(ContainElements("gguf", "mistral"))
		})

		It("should handle empty search results", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("[]"))
			}))

			client.SetBaseURL(server.URL)

			params := hfapi.SearchParams{
				Sort:      "lastModified",
				Direction: -1,
				Limit:     30,
				Search:    "nonexistent",
			}

			models, err := client.SearchModels(params)

			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(0))
		})

		It("should handle HTTP errors", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Internal Server Error"))
			}))

			client.SetBaseURL(server.URL)

			params := hfapi.SearchParams{
				Sort:      "lastModified",
				Direction: -1,
				Limit:     30,
				Search:    "GGUF",
			}

			models, err := client.SearchModels(params)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Status code: 500"))
			Expect(models).To(BeNil())
		})

		It("should handle malformed JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("invalid json"))
			}))

			client.SetBaseURL(server.URL)

			params := hfapi.SearchParams{
				Sort:      "lastModified",
				Direction: -1,
				Limit:     30,
				Search:    "GGUF",
			}

			models, err := client.SearchModels(params)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse JSON response"))
			Expect(models).To(BeNil())
		})
	})

	Context("when getting latest GGUF models", func() {
		BeforeEach(func() {
			mockResponse := `[
				{
					"modelId": "latest-gguf-model",
					"author": "gguf-author",
					"downloads": 5000,
					"lastModified": "2024-01-03T00:00:00.000Z",
					"pipelineTag": "text-generation",
					"private": false,
					"tags": ["gguf", "latest"],
					"createdAt": "2024-01-03T00:00:00.000Z",
					"updatedAt": "2024-01-03T00:00:00.000Z",
					"sha": "latest123",
					"config": {},
					"model_index": "latest-index",
					"library_name": "transformers",
					"mask_token": null,
					"tokenizer_class": "LlamaTokenizer"
				}
			]`

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify the search parameters are correct for GGUF search
				Expect(r.URL.Query().Get("search")).To(Equal("GGUF"))
				Expect(r.URL.Query().Get("sort")).To(Equal("lastModified"))
				Expect(r.URL.Query().Get("direction")).To(Equal("-1"))

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(mockResponse))
			}))

			client.SetBaseURL(server.URL)
		})

		It("should fetch latest GGUF models with correct parameters", func() {
			models, err := client.GetLatest("GGUF", 10)

			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))
			Expect(models[0].ModelID).To(Equal("latest-gguf-model"))
			Expect(models[0].Author).To(Equal("gguf-author"))
			Expect(models[0].Downloads).To(Equal(5000))
			Expect(models[0].Tags).To(ContainElements("gguf", "latest"))
		})

		It("should use custom search term", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Query().Get("search")).To(Equal("custom-search"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("[]"))
			}))

			client.SetBaseURL(server.URL)

			models, err := client.GetLatest("custom-search", 5)

			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(0))
		})
	})

	Context("when handling network errors", func() {
		It("should handle connection failures gracefully", func() {
			// Use an invalid URL to simulate connection failure
			client.SetBaseURL("http://invalid-url-that-does-not-exist")

			params := hfapi.SearchParams{
				Sort:      "lastModified",
				Direction: -1,
				Limit:     30,
				Search:    "GGUF",
			}

			models, err := client.SearchModels(params)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to make request"))
			Expect(models).To(BeNil())
		})
	})

	Context("when getting file SHA on remote model", func() {
		It("should get file SHA successfully", func() {
			sha, err := client.GetFileSHA(
				"mudler/LocalAI-functioncall-qwen2.5-7b-v0.5-Q4_K_M-GGUF", "localai-functioncall-qwen2.5-7b-v0.5-q4_k_m.gguf")
			Expect(err).ToNot(HaveOccurred())
			Expect(sha).To(Equal("4e7b7fe1d54b881f1ef90799219dc6cc285d29db24f559c8998d1addb35713d4"))
		})
	})

	Context("when listing files", func() {
		BeforeEach(func() {
			mockFilesResponse := `[
				{
					"type": "file",
					"path": "model-Q4_K_M.gguf",
					"size": 1000000,
					"oid": "abc123",
					"lfs": {
						"oid": "def456789",
						"size": 1000000,
						"pointerSize": 135
					}
				},
				{
					"type": "file",
					"path": "README.md",
					"size": 5000,
					"oid": "readme123"
				},
				{
					"type": "file",
					"path": "config.json",
					"size": 1000,
					"oid": "config123"
				}
			]`

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/tree/main") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(mockFilesResponse))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			client.SetBaseURL(server.URL)
		})

		It("should list files successfully", func() {
			files, err := client.ListFiles("test/model")

			Expect(err).ToNot(HaveOccurred())
			Expect(files).To(HaveLen(3))

			Expect(files[0].Path).To(Equal("model-Q4_K_M.gguf"))
			Expect(files[0].Size).To(Equal(int64(1000000)))
			Expect(files[0].LFS).ToNot(BeNil())
			Expect(files[0].LFS.Oid).To(Equal("def456789"))

			Expect(files[1].Path).To(Equal("README.md"))
			Expect(files[1].Size).To(Equal(int64(5000)))
		})
	})

	Context("when listing files with subfolders", func() {
		BeforeEach(func() {
			// Mock response for root directory with files and a subfolder
			mockRootResponse := `[
				{
					"type": "file",
					"path": "README.md",
					"size": 5000,
					"oid": "readme123"
				},
				{
					"type": "directory",
					"path": "subfolder",
					"size": 0,
					"oid": "dir123"
				},
				{
					"type": "file",
					"path": "config.json",
					"size": 1000,
					"oid": "config123"
				}
			]`

			// Mock response for subfolder directory
			mockSubfolderResponse := `[
				{
					"type": "file",
					"path": "subfolder/file.bin",
					"size": 2000000,
					"oid": "filebin123",
					"lfs": {
						"oid": "filebin456",
						"size": 2000000,
						"pointerSize": 135
					}
				},
				{
					"type": "directory",
					"path": "nested",
					"size": 0,
					"oid": "nesteddir123"
				}
			]`

			// Mock response for nested subfolder
			mockNestedResponse := `[
				{
					"type": "file",
					"path": "subfolder/nested/nested_file.gguf",
					"size": 5000000,
					"oid": "nested123",
					"lfs": {
						"oid": "nested456",
						"size": 5000000,
						"pointerSize": 135
					}
				}
			]`

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				urlPath := r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)

				if strings.Contains(urlPath, "/tree/main/subfolder/nested") {
					w.Write([]byte(mockNestedResponse))
				} else if strings.Contains(urlPath, "/tree/main/subfolder") {
					w.Write([]byte(mockSubfolderResponse))
				} else if strings.Contains(urlPath, "/tree/main") {
					w.Write([]byte(mockRootResponse))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			client.SetBaseURL(server.URL)
		})

		It("should recursively list all files including those in subfolders", func() {
			files, err := client.ListFiles("test/model")

			Expect(err).ToNot(HaveOccurred())
			Expect(files).To(HaveLen(4))

			// Verify root level files
			readmeFile := findFileByPath(files, "README.md")
			Expect(readmeFile).ToNot(BeNil())
			Expect(readmeFile.Size).To(Equal(int64(5000)))
			Expect(readmeFile.Oid).To(Equal("readme123"))

			configFile := findFileByPath(files, "config.json")
			Expect(configFile).ToNot(BeNil())
			Expect(configFile.Size).To(Equal(int64(1000)))
			Expect(configFile.Oid).To(Equal("config123"))

			// Verify subfolder file with relative path
			subfolderFile := findFileByPath(files, "subfolder/file.bin")
			Expect(subfolderFile).ToNot(BeNil())
			Expect(subfolderFile.Size).To(Equal(int64(2000000)))
			Expect(subfolderFile.LFS).ToNot(BeNil())
			Expect(subfolderFile.LFS.Oid).To(Equal("filebin456"))

			// Verify nested subfolder file
			nestedFile := findFileByPath(files, "subfolder/nested/nested_file.gguf")
			Expect(nestedFile).ToNot(BeNil())
			Expect(nestedFile.Size).To(Equal(int64(5000000)))
			Expect(nestedFile.LFS).ToNot(BeNil())
			Expect(nestedFile.LFS.Oid).To(Equal("nested456"))
		})

		It("should handle files with correct relative paths", func() {
			files, err := client.ListFiles("test/model")

			Expect(err).ToNot(HaveOccurred())

			// Check that all paths are relative and correct
			paths := make([]string, len(files))
			for i, file := range files {
				paths[i] = file.Path
			}

			Expect(paths).To(ContainElements(
				"README.md",
				"config.json",
				"subfolder/file.bin",
				"subfolder/nested/nested_file.gguf",
			))
		})
	})

	Context("when getting file SHA", func() {
		BeforeEach(func() {
			mockFilesResponse := `[
				{
					"type": "file",
					"path": "model-Q4_K_M.gguf",
					"size": 1000000,
					"oid": "abc123",
					"lfs": {
						"oid": "def456789",
						"size": 1000000,
						"pointerSize": 135
					}
				}
			]`

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/tree/main") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(mockFilesResponse))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			client.SetBaseURL(server.URL)
		})

		It("should get file SHA successfully", func() {
			sha, err := client.GetFileSHA("test/model", "model-Q4_K_M.gguf")

			Expect(err).ToNot(HaveOccurred())
			Expect(sha).To(Equal("def456789"))
		})

		It("should handle missing SHA gracefully", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/tree/main") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`[
						{
							"type": "file",
							"path": "file.txt",
							"size": 100,
							"oid": "file123"
						}
					]`))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			client.SetBaseURL(server.URL)

			sha, err := client.GetFileSHA("test/model", "file.txt")

			Expect(err).ToNot(HaveOccurred())
			// When there's no LFS, it should return the OID
			Expect(sha).To(Equal("file123"))
		})
	})

	Context("when getting model details", func() {
		BeforeEach(func() {
			mockFilesResponse := `[
				{
					"type": "file",
					"path": "model-Q4_K_M.gguf",
					"size": 1000000,
					"oid": "abc123",
					"lfs": {
						"oid": "sha256:def456",
						"size": 1000000,
						"pointer": "version https://git-lfs.github.com/spec/v1",
						"sha256": "def456789"
					}
				},
				{
					"type": "file",
					"path": "README.md",
					"size": 5000,
					"oid": "readme123"
				}
			]`

			mockFileInfoResponse := `{
				"path": "model-Q4_K_M.gguf",
				"size": 1000000,
				"oid": "abc123",
				"lfs": {
					"oid": "sha256:def456",
					"size": 1000000,
					"pointer": "version https://git-lfs.github.com/spec/v1",
					"sha256": "def456789"
				}
			}`

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/tree/main") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(mockFilesResponse))
				} else if strings.Contains(r.URL.Path, "/paths-info") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(mockFileInfoResponse))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			client.SetBaseURL(server.URL)
		})

		It("should get model details successfully", func() {
			details, err := client.GetModelDetails("test/model")

			Expect(err).ToNot(HaveOccurred())
			Expect(details.ModelID).To(Equal("test/model"))
			Expect(details.Author).To(Equal("test"))
			Expect(details.Files).To(HaveLen(2))

			Expect(details.ReadmeFile).ToNot(BeNil())
			Expect(details.ReadmeFile.Path).To(Equal("README.md"))
			Expect(details.ReadmeFile.IsReadme).To(BeTrue())

			// Verify URLs are set for all files
			baseURL := strings.TrimSuffix(server.URL, "/api/models")
			for _, file := range details.Files {
				expectedURL := fmt.Sprintf("%s/test/model/resolve/main/%s", baseURL, file.Path)
				Expect(file.URL).To(Equal(expectedURL))
			}
		})
	})

	Context("when getting README content", func() {
		BeforeEach(func() {
			mockReadmeContent := "# Test Model\n\nThis is a test model for demonstration purposes."

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/raw/main/") {
					w.Header().Set("Content-Type", "text/plain")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(mockReadmeContent))
				} else {
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			client.SetBaseURL(server.URL)
		})

		It("should get README content successfully", func() {
			content, err := client.GetReadmeContent("test/model", "README.md")

			Expect(err).ToNot(HaveOccurred())
			Expect(content).To(Equal("# Test Model\n\nThis is a test model for demonstration purposes."))
		})
	})

	Context("when filtering files", func() {
		It("should filter files by quantization", func() {
			files := []hfapi.ModelFile{
				{Path: "model-Q4_K_M.gguf"},
				{Path: "model-Q3_K_M.gguf"},
				{Path: "README.md", IsReadme: true},
			}

			filtered := hfapi.FilterFilesByQuantization(files, "Q4_K_M")

			Expect(filtered).To(HaveLen(1))
			Expect(filtered[0].Path).To(Equal("model-Q4_K_M.gguf"))
		})

		It("should find preferred model file", func() {
			files := []hfapi.ModelFile{
				{Path: "model-Q3_K_M.gguf"},
				{Path: "model-Q4_K_M.gguf"},
				{Path: "README.md", IsReadme: true},
			}

			preferences := []string{"Q4_K_M", "Q3_K_M"}
			preferred := hfapi.FindPreferredModelFile(files, preferences)

			Expect(preferred).ToNot(BeNil())
			Expect(preferred.Path).To(Equal("model-Q4_K_M.gguf"))
			Expect(preferred.IsReadme).To(BeFalse())
		})

		It("should return nil if no preferred file found", func() {
			files := []hfapi.ModelFile{
				{Path: "model-Q2_K.gguf"},
				{Path: "README.md", IsReadme: true},
			}

			preferences := []string{"Q4_K_M", "Q3_K_M"}
			preferred := hfapi.FindPreferredModelFile(files, preferences)

			Expect(preferred).To(BeNil())
		})
	})

	Context("integration test with real HuggingFace API", func() {
		It("should recursively list all files including subfolders from real repository", func() {
			// This test makes actual API calls to HuggingFace
			// Skip if running in CI or if network is not available
			realClient := hfapi.NewClient()
			repoID := "bartowski/Qwen_Qwen3-Next-80B-A3B-Instruct-GGUF"

			files, err := realClient.ListFiles(repoID)

			Expect(err).ToNot(HaveOccurred())
			Expect(files).ToNot(BeEmpty(), "should return at least some files")

			// Verify that we get files from subfolders
			// Based on the repository structure, there should be files in subfolders like:
			// - Qwen_Qwen3-Next-80B-A3B-Instruct-Q4_1/...
			// - Qwen_Qwen3-Next-80B-A3B-Instruct-Q5_K_L/...
			// etc.
			hasSubfolderFiles := false
			rootLevelFiles := 0
			subfolderFiles := 0

			for _, file := range files {
				if strings.Contains(file.Path, "/") {
					hasSubfolderFiles = true
					subfolderFiles++
					// Verify the path format is correct (subfolder/file.gguf)
					Expect(file.Path).ToNot(HavePrefix("/"), "paths should be relative, not absolute")
					Expect(file.Path).ToNot(HaveSuffix("/"), "file paths should not end with /")
				} else {
					rootLevelFiles++
				}
			}

			Expect(hasSubfolderFiles).To(BeTrue(), "should find files in subfolders")
			Expect(rootLevelFiles).To(BeNumerically(">", 0), "should find files at root level")
			Expect(subfolderFiles).To(BeNumerically(">", 0), "should find files in subfolders")
			// Verify specific expected files exist
			// Root level files
			readmeFile := findFileByPath(files, "README.md")
			Expect(readmeFile).ToNot(BeNil(), "README.md should exist at root level")

			// Verify we can find files in subfolders
			// Look for any file in a subfolder (the exact structure may vary, can be nested)
			foundSubfolderFile := false
			for _, file := range files {
				if strings.Contains(file.Path, "/") && strings.HasSuffix(file.Path, ".gguf") {
					foundSubfolderFile = true
					// Verify the path structure: can be nested like subfolder/subfolder/file.gguf
					parts := strings.Split(file.Path, "/")
					Expect(len(parts)).To(BeNumerically(">=", 2), "subfolder files should have at least subfolder/file.gguf format")
					// The last part should be the filename
					Expect(parts[len(parts)-1]).To(HaveSuffix(".gguf"), "file in subfolder should be a .gguf file")
					Expect(parts[len(parts)-1]).ToNot(BeEmpty(), "filename should not be empty")
					break
				}
			}
			Expect(foundSubfolderFile).To(BeTrue(), "should find at least one .gguf file in a subfolder")

			// Verify file properties are populated
			for _, file := range files {
				Expect(file.Path).ToNot(BeEmpty(), "file path should not be empty")
				Expect(file.Type).To(Equal("file"), "all returned items should be files, not directories")
				// Size might be 0 for some files, but OID should be present
				if file.LFS == nil {
					Expect(file.Oid).ToNot(BeEmpty(), "file should have an OID if no LFS")
				}
			}
		})
	})
})

// findFileByPath is a helper function to find a file by its path in a slice of FileInfo
func findFileByPath(files []hfapi.FileInfo, path string) *hfapi.FileInfo {
	for i := range files {
		if files[i].Path == path {
			return &files[i]
		}
	}
	return nil
}
