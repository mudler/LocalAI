package main

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// runSyntheticMode generates synthetic test data and appends it to the gallery
func runSyntheticMode() error {
	generator := NewSyntheticDataGenerator()

	// Generate a random number of synthetic models (1-3)
	numModels := generator.rand.Intn(3) + 1
	fmt.Printf("Generating %d synthetic models for testing...\n", numModels)

	var models []ProcessedModel
	for i := 0; i < numModels; i++ {
		model := generator.GenerateProcessedModel()
		models = append(models, model)
		fmt.Printf("Generated synthetic model: %s\n", model.ModelID)
	}

	// Generate YAML entries and append to gallery/index.yaml
	fmt.Println("Generating YAML entries for synthetic models...")
	err := generateYAMLForModels(context.Background(), models, "Q4_K_M")
	if err != nil {
		return fmt.Errorf("error generating YAML entries: %w", err)
	}

	fmt.Printf("Successfully added %d synthetic models to the gallery for testing!\n", len(models))
	return nil
}

// SyntheticDataGenerator provides methods to generate synthetic test data
type SyntheticDataGenerator struct {
	rand *rand.Rand
}

// NewSyntheticDataGenerator creates a new synthetic data generator
func NewSyntheticDataGenerator() *SyntheticDataGenerator {
	return &SyntheticDataGenerator{
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// GenerateProcessedModelFile creates a synthetic ProcessedModelFile
func (g *SyntheticDataGenerator) GenerateProcessedModelFile() ProcessedModelFile {
	fileTypes := []string{"model", "readme", "other"}
	fileType := fileTypes[g.rand.Intn(len(fileTypes))]

	var path string
	var isReadme bool

	switch fileType {
	case "model":
		path = fmt.Sprintf("model-%s.gguf", g.randomString(8))
		isReadme = false
	case "readme":
		path = "README.md"
		isReadme = true
	default:
		path = fmt.Sprintf("file-%s.txt", g.randomString(6))
		isReadme = false
	}

	return ProcessedModelFile{
		Path:     path,
		Size:     int64(g.rand.Intn(1000000000) + 1000000), // 1MB to 1GB
		SHA256:   g.randomSHA256(),
		IsReadme: isReadme,
		FileType: fileType,
	}
}

// GenerateProcessedModel creates a synthetic ProcessedModel
func (g *SyntheticDataGenerator) GenerateProcessedModel() ProcessedModel {
	authors := []string{"microsoft", "meta", "google", "openai", "anthropic", "mistralai", "huggingface"}
	modelNames := []string{"llama", "gpt", "claude", "mistral", "gemma", "phi", "qwen", "codellama"}

	author := authors[g.rand.Intn(len(authors))]
	modelName := modelNames[g.rand.Intn(len(modelNames))]
	modelID := fmt.Sprintf("%s/%s-%s", author, modelName, g.randomString(6))

	// Generate files
	numFiles := g.rand.Intn(5) + 2 // 2-6 files
	files := make([]ProcessedModelFile, numFiles)

	// Ensure at least one model file and one readme
	hasModelFile := false
	hasReadme := false

	for i := 0; i < numFiles; i++ {
		files[i] = g.GenerateProcessedModelFile()
		if files[i].FileType == "model" {
			hasModelFile = true
		}
		if files[i].FileType == "readme" {
			hasReadme = true
		}
	}

	// Add required files if missing
	if !hasModelFile {
		modelFile := g.GenerateProcessedModelFile()
		modelFile.FileType = "model"
		modelFile.Path = fmt.Sprintf("%s-Q4_K_M.gguf", modelName)
		files = append(files, modelFile)
	}

	if !hasReadme {
		readmeFile := g.GenerateProcessedModelFile()
		readmeFile.FileType = "readme"
		readmeFile.Path = "README.md"
		readmeFile.IsReadme = true
		files = append(files, readmeFile)
	}

	// Find preferred model file
	var preferredModelFile *ProcessedModelFile
	for i := range files {
		if files[i].FileType == "model" {
			preferredModelFile = &files[i]
			break
		}
	}

	// Find readme file
	var readmeFile *ProcessedModelFile
	for i := range files {
		if files[i].FileType == "readme" {
			readmeFile = &files[i]
			break
		}
	}

	readmeContent := g.generateReadmeContent(modelName, author)

	// Generate sample metadata
	licenses := []string{"apache-2.0", "mit", "llama2", "gpl-3.0", "bsd", ""}
	license := licenses[g.rand.Intn(len(licenses))]

	sampleTags := []string{"llm", "gguf", "gpu", "cpu", "text-to-text", "chat", "instruction-tuned"}
	numTags := g.rand.Intn(4) + 3 // 3-6 tags
	tags := make([]string, numTags)
	for i := 0; i < numTags; i++ {
		tags[i] = sampleTags[g.rand.Intn(len(sampleTags))]
	}
	// Remove duplicates
	tags = g.removeDuplicates(tags)

	// Optionally include icon (50% chance)
	icon := ""
	if g.rand.Intn(2) == 0 {
		icon = fmt.Sprintf("https://cdn-avatars.huggingface.co/v1/production/uploads/%s.png", g.randomString(24))
	}

	return ProcessedModel{
		ModelID:                 modelID,
		Author:                  author,
		Downloads:               g.rand.Intn(1000000) + 1000,
		LastModified:            g.randomDate(),
		Files:                   files,
		PreferredModelFile:      preferredModelFile,
		ReadmeFile:              readmeFile,
		ReadmeContent:           readmeContent,
		ReadmeContentPreview:    truncateString(readmeContent, 200),
		QuantizationPreferences: []string{"Q4_K_M", "Q4_K_S", "Q3_K_M", "Q2_K"},
		ProcessingError:         "",
		Tags:                    tags,
		License:                 license,
		Icon:                    icon,
	}
}

// Helper methods for synthetic data generation
func (g *SyntheticDataGenerator) randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[g.rand.Intn(len(charset))]
	}
	return string(b)
}

func (g *SyntheticDataGenerator) randomSHA256() string {
	const charset = "0123456789abcdef"
	b := make([]byte, 64)
	for i := range b {
		b[i] = charset[g.rand.Intn(len(charset))]
	}
	return string(b)
}

func (g *SyntheticDataGenerator) randomDate() string {
	now := time.Now()
	daysAgo := g.rand.Intn(365) // Random date within last year
	pastDate := now.AddDate(0, 0, -daysAgo)
	return pastDate.Format("2006-01-02T15:04:05.000Z")
}

func (g *SyntheticDataGenerator) removeDuplicates(slice []string) []string {
	keys := make(map[string]bool)
	result := []string{}
	for _, item := range slice {
		if !keys[item] {
			keys[item] = true
			result = append(result, item)
		}
	}
	return result
}

func (g *SyntheticDataGenerator) generateReadmeContent(modelName, author string) string {
	templates := []string{
		fmt.Sprintf("# %s Model\n\nThis is a %s model developed by %s. It's designed for various natural language processing tasks including text generation, question answering, and conversation.\n\n## Features\n\n- High-quality text generation\n- Efficient inference\n- Multiple quantization options\n- Easy to use with LocalAI\n\n## Usage\n\nUse this model with LocalAI for various AI tasks.", strings.Title(modelName), modelName, author),
		fmt.Sprintf("# %s\n\nA powerful language model from %s. This model excels at understanding and generating human-like text across multiple domains.\n\n## Capabilities\n\n- Text completion\n- Code generation\n- Creative writing\n- Technical documentation\n\n## Model Details\n\n- Architecture: Transformer-based\n- Training: Large-scale supervised learning\n- Quantization: Available in multiple formats", strings.Title(modelName), author),
		fmt.Sprintf("# %s Language Model\n\nDeveloped by %s, this model represents state-of-the-art performance in natural language understanding and generation.\n\n## Key Features\n\n- Multilingual support\n- Context-aware responses\n- Efficient memory usage\n- Fast inference speed\n\n## Applications\n\n- Chatbots and virtual assistants\n- Content generation\n- Code completion\n- Educational tools", strings.Title(modelName), author),
	}

	return templates[g.rand.Intn(len(templates))]
}
