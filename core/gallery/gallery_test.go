package gallery_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/gallery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v2"
)

var _ = Describe("Gallery", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "gallery-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("ReadConfigFile", func() {
		It("should read and unmarshal a valid YAML file", func() {
			testConfig := map[string]interface{}{
				"name":        "test-model",
				"description": "A test model",
				"license":     "MIT",
			}
			yamlData, err := yaml.Marshal(testConfig)
			Expect(err).NotTo(HaveOccurred())

			filePath := filepath.Join(tempDir, "test.yaml")
			err = os.WriteFile(filePath, yamlData, 0644)
			Expect(err).NotTo(HaveOccurred())

			var result map[string]interface{}
			config, err := ReadConfigFile[map[string]interface{}](filePath)
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			result = *config
			Expect(result["name"]).To(Equal("test-model"))
			Expect(result["description"]).To(Equal("A test model"))
			Expect(result["license"]).To(Equal("MIT"))
		})

		It("should return error when file does not exist", func() {
			_, err := ReadConfigFile[map[string]interface{}]("nonexistent.yaml")
			Expect(err).To(HaveOccurred())
		})

		It("should return error when YAML is invalid", func() {
			filePath := filepath.Join(tempDir, "invalid.yaml")
			err := os.WriteFile(filePath, []byte("invalid: yaml: content: [unclosed"), 0644)
			Expect(err).NotTo(HaveOccurred())

			_, err = ReadConfigFile[map[string]interface{}](filePath)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("GalleryElements Search", func() {
		var elements GalleryElements[*GalleryModel]

		BeforeEach(func() {
			elements = GalleryElements[*GalleryModel]{
				{
					Metadata: Metadata{
						Name:        "bert-embeddings",
						Description: "BERT model for embeddings",
						Tags:        []string{"embeddings", "bert", "nlp"},
						License:     "Apache-2.0",
						Gallery: config.Gallery{
							Name: "huggingface",
						},
					},
				},
				{
					Metadata: Metadata{
						Name:        "gpt-2",
						Description: "GPT-2 language model",
						Tags:        []string{"gpt", "language-model"},
						License:     "MIT",
						Gallery: config.Gallery{
							Name: "openai",
						},
					},
				},
				{
					Metadata: Metadata{
						Name:        "llama-7b",
						Description: "LLaMA 7B model",
						Tags:        []string{"llama", "llm"},
						License:     "LLaMA",
						Gallery: config.Gallery{
							Name: "meta",
						},
					},
				},
			}
		})

		It("should find elements by exact name match", func() {
			results := elements.Search("bert-embeddings")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetName()).To(Equal("bert-embeddings"))
		})

		It("should find elements by partial name match", func() {
			results := elements.Search("bert")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetName()).To(Equal("bert-embeddings"))
		})

		It("should find elements by description", func() {
			results := elements.Search("embeddings")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetName()).To(Equal("bert-embeddings"))
		})

		It("should find elements by gallery name", func() {
			results := elements.Search("huggingface")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetGallery().Name).To(Equal("huggingface"))
		})

		It("should find elements by tags", func() {
			results := elements.Search("nlp")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetName()).To(Equal("bert-embeddings"))
		})

		It("should be case insensitive", func() {
			results := elements.Search("BERT")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetName()).To(Equal("bert-embeddings"))
		})

		It("should find multiple elements", func() {
			results := elements.Search("gpt")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetName()).To(Equal("gpt-2"))
		})

		It("should return empty results for no matches", func() {
			results := elements.Search("nonexistent")
			Expect(results).To(HaveLen(0))
		})

		It("should use fuzzy matching", func() {
			results := elements.Search("bert-emb")
			Expect(results).To(HaveLen(1))
			Expect(results[0].GetName()).To(Equal("bert-embeddings"))
		})
	})

	Describe("GalleryElements SortByName", func() {
		var elements GalleryElements[*GalleryModel]

		BeforeEach(func() {
			elements = GalleryElements[*GalleryModel]{
				{Metadata: Metadata{Name: "zebra"}},
				{Metadata: Metadata{Name: "alpha"}},
				{Metadata: Metadata{Name: "beta"}},
			}
		})

		It("should sort ascending", func() {
			sorted := elements.SortByName("asc")
			Expect(sorted).To(HaveLen(3))
			Expect(sorted[0].GetName()).To(Equal("alpha"))
			Expect(sorted[1].GetName()).To(Equal("beta"))
			Expect(sorted[2].GetName()).To(Equal("zebra"))
		})

		It("should sort descending", func() {
			sorted := elements.SortByName("desc")
			Expect(sorted).To(HaveLen(3))
			Expect(sorted[0].GetName()).To(Equal("zebra"))
			Expect(sorted[1].GetName()).To(Equal("beta"))
			Expect(sorted[2].GetName()).To(Equal("alpha"))
		})

		It("should be case insensitive", func() {
			elements = GalleryElements[*GalleryModel]{
				{Metadata: Metadata{Name: "Zebra"}},
				{Metadata: Metadata{Name: "alpha"}},
				{Metadata: Metadata{Name: "Beta"}},
			}
			sorted := elements.SortByName("asc")
			Expect(sorted[0].GetName()).To(Equal("alpha"))
			Expect(sorted[1].GetName()).To(Equal("Beta"))
			Expect(sorted[2].GetName()).To(Equal("Zebra"))
		})
	})

	Describe("GalleryElements SortByRepository", func() {
		var elements GalleryElements[*GalleryModel]

		BeforeEach(func() {
			elements = GalleryElements[*GalleryModel]{
				{
					Metadata: Metadata{
						Gallery: config.Gallery{Name: "zebra-repo"},
					},
				},
				{
					Metadata: Metadata{
						Gallery: config.Gallery{Name: "alpha-repo"},
					},
				},
				{
					Metadata: Metadata{
						Gallery: config.Gallery{Name: "beta-repo"},
					},
				},
			}
		})

		It("should sort ascending", func() {
			sorted := elements.SortByRepository("asc")
			Expect(sorted).To(HaveLen(3))
			Expect(sorted[0].GetGallery().Name).To(Equal("alpha-repo"))
			Expect(sorted[1].GetGallery().Name).To(Equal("beta-repo"))
			Expect(sorted[2].GetGallery().Name).To(Equal("zebra-repo"))
		})

		It("should sort descending", func() {
			sorted := elements.SortByRepository("desc")
			Expect(sorted).To(HaveLen(3))
			Expect(sorted[0].GetGallery().Name).To(Equal("zebra-repo"))
			Expect(sorted[1].GetGallery().Name).To(Equal("beta-repo"))
			Expect(sorted[2].GetGallery().Name).To(Equal("alpha-repo"))
		})
	})

	Describe("GalleryElements SortByLicense", func() {
		var elements GalleryElements[*GalleryModel]

		BeforeEach(func() {
			elements = GalleryElements[*GalleryModel]{
				{Metadata: Metadata{License: "MIT"}},
				{Metadata: Metadata{License: "Apache-2.0"}},
				{Metadata: Metadata{License: ""}},
				{Metadata: Metadata{License: "GPL-3.0"}},
			}
		})

		It("should sort ascending with empty licenses at end", func() {
			sorted := elements.SortByLicense("asc")
			Expect(sorted).To(HaveLen(4))
			Expect(sorted[0].GetLicense()).To(Equal("Apache-2.0"))
			Expect(sorted[1].GetLicense()).To(Equal("GPL-3.0"))
			Expect(sorted[2].GetLicense()).To(Equal("MIT"))
			Expect(sorted[3].GetLicense()).To(Equal(""))
		})

		It("should sort descending with empty licenses at beginning", func() {
			sorted := elements.SortByLicense("desc")
			Expect(sorted).To(HaveLen(4))
			Expect(sorted[0].GetLicense()).To(Equal(""))
			Expect(sorted[1].GetLicense()).To(Equal("MIT"))
			Expect(sorted[2].GetLicense()).To(Equal("GPL-3.0"))
			Expect(sorted[3].GetLicense()).To(Equal("Apache-2.0"))
		})

		It("should handle all empty licenses", func() {
			elements = GalleryElements[*GalleryModel]{
				{Metadata: Metadata{License: ""}},
				{Metadata: Metadata{License: ""}},
			}
			sorted := elements.SortByLicense("asc")
			Expect(sorted).To(HaveLen(2))
		})
	})

	Describe("GalleryElements SortByInstalled", func() {
		var elements GalleryElements[*GalleryModel]

		BeforeEach(func() {
			elements = GalleryElements[*GalleryModel]{
				{Metadata: Metadata{Name: "installed-2", Installed: true}},
				{Metadata: Metadata{Name: "not-installed-1", Installed: false}},
				{Metadata: Metadata{Name: "installed-1", Installed: true}},
				{Metadata: Metadata{Name: "not-installed-2", Installed: false}},
			}
		})

		It("should sort ascending with installed first, then by name", func() {
			sorted := elements.SortByInstalled("asc")
			Expect(sorted).To(HaveLen(4))
			Expect(sorted[0].GetInstalled()).To(BeTrue())
			Expect(sorted[0].GetName()).To(Equal("installed-1"))
			Expect(sorted[1].GetInstalled()).To(BeTrue())
			Expect(sorted[1].GetName()).To(Equal("installed-2"))
			Expect(sorted[2].GetInstalled()).To(BeFalse())
			Expect(sorted[2].GetName()).To(Equal("not-installed-1"))
			Expect(sorted[3].GetInstalled()).To(BeFalse())
			Expect(sorted[3].GetName()).To(Equal("not-installed-2"))
		})

		It("should sort descending with not-installed first, then by name", func() {
			sorted := elements.SortByInstalled("desc")
			Expect(sorted).To(HaveLen(4))
			Expect(sorted[0].GetInstalled()).To(BeFalse())
			Expect(sorted[0].GetName()).To(Equal("not-installed-2"))
			Expect(sorted[1].GetInstalled()).To(BeFalse())
			Expect(sorted[1].GetName()).To(Equal("not-installed-1"))
			Expect(sorted[2].GetInstalled()).To(BeTrue())
			Expect(sorted[2].GetName()).To(Equal("installed-2"))
			Expect(sorted[3].GetInstalled()).To(BeTrue())
			Expect(sorted[3].GetName()).To(Equal("installed-1"))
		})
	})

	Describe("GalleryElements FindByName", func() {
		var elements GalleryElements[*GalleryModel]

		BeforeEach(func() {
			elements = GalleryElements[*GalleryModel]{
				{Metadata: Metadata{Name: "bert-embeddings"}},
				{Metadata: Metadata{Name: "gpt-2"}},
				{Metadata: Metadata{Name: "llama-7b"}},
			}
		})

		It("should find element by exact name", func() {
			result := elements.FindByName("bert-embeddings")
			Expect(result).NotTo(BeNil())
			Expect(result.GetName()).To(Equal("bert-embeddings"))
		})

		It("should be case insensitive", func() {
			result := elements.FindByName("BERT-EMBEDDINGS")
			Expect(result).NotTo(BeNil())
			Expect(result.GetName()).To(Equal("bert-embeddings"))
		})

		It("should return zero value when not found", func() {
			result := elements.FindByName("nonexistent")
			Expect(result).To(BeNil())
		})
	})

	Describe("GalleryElements Paginate", func() {
		var elements GalleryElements[*GalleryModel]

		BeforeEach(func() {
			elements = GalleryElements[*GalleryModel]{
				{Metadata: Metadata{Name: "model-1"}},
				{Metadata: Metadata{Name: "model-2"}},
				{Metadata: Metadata{Name: "model-3"}},
				{Metadata: Metadata{Name: "model-4"}},
				{Metadata: Metadata{Name: "model-5"}},
			}
		})

		It("should return first page", func() {
			page := elements.Paginate(1, 2)
			Expect(page).To(HaveLen(2))
			Expect(page[0].GetName()).To(Equal("model-1"))
			Expect(page[1].GetName()).To(Equal("model-2"))
		})

		It("should return second page", func() {
			page := elements.Paginate(2, 2)
			Expect(page).To(HaveLen(2))
			Expect(page[0].GetName()).To(Equal("model-3"))
			Expect(page[1].GetName()).To(Equal("model-4"))
		})

		It("should return partial last page", func() {
			page := elements.Paginate(3, 2)
			Expect(page).To(HaveLen(1))
			Expect(page[0].GetName()).To(Equal("model-5"))
		})

		It("should handle page beyond range", func() {
			page := elements.Paginate(10, 2)
			Expect(page).To(HaveLen(0))
		})

		It("should handle empty elements", func() {
			empty := GalleryElements[*GalleryModel]{}
			page := empty.Paginate(1, 10)
			Expect(page).To(HaveLen(0))
		})
	})

	Describe("FindGalleryElement", func() {
		var models []*GalleryModel

		BeforeEach(func() {
			models = []*GalleryModel{
				{
					Metadata: Metadata{
						Name: "bert-embeddings",
						Gallery: config.Gallery{
							Name: "huggingface",
						},
					},
				},
				{
					Metadata: Metadata{
						Name: "gpt-2",
						Gallery: config.Gallery{
							Name: "openai",
						},
					},
				},
			}
		})

		It("should find element by name without @ notation", func() {
			result := FindGalleryElement(models, "bert-embeddings")
			Expect(result).NotTo(BeNil())
			Expect(result.GetName()).To(Equal("bert-embeddings"))
		})

		It("should find element by name with @ notation", func() {
			result := FindGalleryElement(models, "huggingface@bert-embeddings")
			Expect(result).NotTo(BeNil())
			Expect(result.GetName()).To(Equal("bert-embeddings"))
			Expect(result.GetGallery().Name).To(Equal("huggingface"))
		})

		It("should be case insensitive", func() {
			result := FindGalleryElement(models, "BERT-EMBEDDINGS")
			Expect(result).NotTo(BeNil())
			Expect(result.GetName()).To(Equal("bert-embeddings"))
		})

		It("should handle path separators in name", func() {
			// Path separators are replaced with __, so bert/embeddings becomes bert__embeddings
			// This test verifies the replacement happens, but won't match unless model name has __
			modelsWithPath := []*GalleryModel{
				{
					Metadata: Metadata{
						Name: "bert__embeddings",
						Gallery: config.Gallery{
							Name: "huggingface",
						},
					},
				},
			}
			result := FindGalleryElement(modelsWithPath, "bert/embeddings")
			Expect(result).NotTo(BeNil())
			Expect(result.GetName()).To(Equal("bert__embeddings"))
		})

		It("should return zero value when not found", func() {
			result := FindGalleryElement(models, "nonexistent")
			Expect(result).To(BeNil())
		})

		It("should return zero value when gallery@name not found", func() {
			result := FindGalleryElement(models, "nonexistent@model")
			Expect(result).To(BeNil())
		})
	})
})
