package hfapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-skynet/LocalAI/.github/gallery-agent/hfapi"
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

	Context("when getting file SHA", func() {
		BeforeEach(func() {
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
				if strings.Contains(r.URL.Path, "/paths-info") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(mockFileInfoResponse))
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
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"path": "file.txt", "size": 100}`))
			}))

			client.SetBaseURL(server.URL)

			sha, err := client.GetFileSHA("test/model", "file.txt")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no SHA256 found"))
			Expect(sha).To(Equal(""))
		})
	})

	Context("when getting model details", func() {
		BeforeEach(func() {
			mockFilesResponse := `[
				{
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
})
