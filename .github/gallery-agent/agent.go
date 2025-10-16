package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/go-skynet/LocalAI/.github/gallery-agent/hfapi"
	"github.com/mudler/cogito"

	"github.com/mudler/cogito/structures"
	"github.com/sashabaranov/go-openai/jsonschema"
)

var (
	openAIModel      = os.Getenv("OPENAI_MODEL")
	openAIKey        = os.Getenv("OPENAI_KEY")
	openAIBaseURL    = os.Getenv("OPENAI_BASE_URL")
	galleryIndexPath = os.Getenv("GALLERY_INDEX_PATH")
	//defaultclient
	llm = cogito.NewOpenAILLM(openAIModel, openAIKey, openAIBaseURL)
)

// cleanTextContent removes trailing spaces, tabs, and normalizes line endings
// to prevent YAML linting issues like trailing spaces and multiple empty lines
func cleanTextContent(text string) string {
	lines := strings.Split(text, "\n")
	var cleanedLines []string
	var prevEmpty bool
	for _, line := range lines {
		// Remove all trailing whitespace (spaces, tabs, etc.)
		trimmed := strings.TrimRight(line, " \t\r")
		// Avoid multiple consecutive empty lines
		if trimmed == "" {
			if !prevEmpty {
				cleanedLines = append(cleanedLines, "")
			}
			prevEmpty = true
		} else {
			cleanedLines = append(cleanedLines, trimmed)
			prevEmpty = false
		}
	}
	// Remove trailing empty lines from the result
	result := strings.Join(cleanedLines, "\n")
	return strings.TrimRight(result, "\n")
}

// isModelExisting checks if a specific model ID exists in the gallery using text search
func isModelExisting(modelID string) (bool, error) {
	indexPath := getGalleryIndexPath()
	content, err := os.ReadFile(indexPath)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", indexPath, err)
	}

	contentStr := string(content)
	// Simple text search - if the model ID appears anywhere in the file, it exists
	return strings.Contains(contentStr, modelID), nil
}

// filterExistingModels removes models that already exist in the gallery
func filterExistingModels(models []ProcessedModel) ([]ProcessedModel, error) {
	var filteredModels []ProcessedModel
	for _, model := range models {
		exists, err := isModelExisting(model.ModelID)
		if err != nil {
			fmt.Printf("Error checking if model %s exists: %v, skipping\n", model.ModelID, err)
			continue
		}

		if !exists {
			filteredModels = append(filteredModels, model)
		} else {
			fmt.Printf("Skipping existing model: %s\n", model.ModelID)
		}
	}

	fmt.Printf("Filtered out %d existing models, %d new models remaining\n",
		len(models)-len(filteredModels), len(filteredModels))

	return filteredModels, nil
}

// getGalleryIndexPath returns the gallery index file path, with a default fallback
func getGalleryIndexPath() string {
	if galleryIndexPath != "" {
		return galleryIndexPath
	}
	return "gallery/index.yaml"
}

func getRealReadme(ctx context.Context, repository string) (string, error) {
	// Create a conversation fragment
	fragment := cogito.NewEmptyFragment().
		AddMessage("user",
			`Your task is to get a clear description of a large language model from huggingface by using the provided tool. I will share with you a repository that might be quantized, and as such probably not by the original model author. We need to get the real  description of the model, and not the one that might be quantized. You will have to call the tool to get the readme more than once by figuring out from the quantized readme which is the base model readme. This is the repository: `+repository)

	// Execute with tools
	result, err := cogito.ExecuteTools(llm, fragment,
		cogito.WithIterations(3),
		cogito.WithMaxAttempts(3),
		cogito.WithTools(&HFReadmeTool{client: hfapi.NewClient()}))
	if err != nil {
		return "", err
	}

	result = result.AddMessage("user", "Describe the model in a clear and concise way that can be shared in a model gallery.")

	// Get a response
	newFragment, err := llm.Ask(ctx, result)
	if err != nil {
		return "", err
	}

	content := newFragment.LastMessage().Content
	return cleanTextContent(content), nil
}

func selectMostInterestingModels(ctx context.Context, searchResult *SearchResult) ([]ProcessedModel, error) {
	// Create a conversation fragment
	fragment := cogito.NewEmptyFragment().
		AddMessage("user",
			`Your task is to analyze a list of AI models and select the most interesting ones for a model gallery. You will be given detailed information about multiple models including their metadata, file information, and README content.

Consider the following criteria when selecting models:
1. Model popularity (download count)
2. Model recency (last modified date)
3. Model completeness (has preferred model file, README, etc.)
4. Model uniqueness (not duplicates or very similar models)
5. Model quality (based on README content and description)
6. Model utility (practical applications)

You should select models that would be most valuable for users browsing a model gallery. Prioritize models that are:
- Well-documented with clear READMEs
- Recently updated
- Popular (high download count)
- Have the preferred quantization format available
- Offer unique capabilities or are from reputable authors

Return your analysis and selection reasoning.`)

	// Add the search results as context
	modelsInfo := fmt.Sprintf("Found %d models matching '%s' with quantization preference '%s':\n\n",
		searchResult.TotalModelsFound, searchResult.SearchTerm, searchResult.Quantization)

	for i, model := range searchResult.Models {
		modelsInfo += fmt.Sprintf("Model %d:\n", i+1)
		modelsInfo += fmt.Sprintf("  ID: %s\n", model.ModelID)
		modelsInfo += fmt.Sprintf("  Author: %s\n", model.Author)
		modelsInfo += fmt.Sprintf("  Downloads: %d\n", model.Downloads)
		modelsInfo += fmt.Sprintf("  Last Modified: %s\n", model.LastModified)
		modelsInfo += fmt.Sprintf("  Files: %d files\n", len(model.Files))

		if model.PreferredModelFile != nil {
			modelsInfo += fmt.Sprintf("  Preferred Model File: %s (%d bytes)\n",
				model.PreferredModelFile.Path, model.PreferredModelFile.Size)
		} else {
			modelsInfo += "  No preferred model file found\n"
		}

		if model.ReadmeContent != "" {
			modelsInfo += fmt.Sprintf("  README: %s\n", model.ReadmeContent)
		}

		if model.ProcessingError != "" {
			modelsInfo += fmt.Sprintf("  Processing Error: %s\n", model.ProcessingError)
		}

		modelsInfo += "\n"
	}

	fragment = fragment.AddMessage("user", modelsInfo)

	fragment = fragment.AddMessage("user", "Based on your analysis, select the top 5 most interesting models and provide a brief explanation for each selection. Also, create a filtered SearchResult with only the selected models. Return just a list of repositories IDs, you will later be asked to output it as a JSON array with the json tool.")

	// Get a response
	newFragment, err := llm.Ask(ctx, fragment)
	if err != nil {
		return nil, err
	}

	fmt.Println(newFragment.LastMessage().Content)
	repositories := struct {
		Repositories []string `json:"repositories"`
	}{}

	s := structures.Structure{
		Schema: jsonschema.Definition{
			Type:                 jsonschema.Object,
			AdditionalProperties: false,
			Properties: map[string]jsonschema.Definition{
				"repositories": {
					Type:        jsonschema.Array,
					Items:       &jsonschema.Definition{Type: jsonschema.String},
					Description: "The trending repositories IDs",
				},
			},
			Required: []string{"repositories"},
		},
		Object: &repositories,
	}

	err = newFragment.ExtractStructure(ctx, llm, s)
	if err != nil {
		return nil, err
	}

	filteredModels := []ProcessedModel{}
	for _, m := range searchResult.Models {
		if slices.Contains(repositories.Repositories, m.ModelID) {
			filteredModels = append(filteredModels, m)
		}
	}

	return filteredModels, nil
}

// ModelFamily represents a YAML anchor/family
type ModelFamily struct {
	Anchor string `json:"anchor"`
	Name   string `json:"name"`
}

// selectModelFamily selects the appropriate model family/anchor for a given model
func selectModelFamily(ctx context.Context, model ProcessedModel, availableFamilies []ModelFamily) (string, error) {
	// Create a conversation fragment
	fragment := cogito.NewEmptyFragment().
		AddMessage("user",
			`Your task is to select the most appropriate model family/anchor for a given AI model. You will be provided with:
1. Information about the model (name, description, etc.)
2. A list of available model families/anchors

You need to select the family that best matches the model's architecture, capabilities, or characteristics. Consider:
- Model architecture (e.g., Llama, Qwen, Mistral, etc.)
- Model capabilities (e.g., vision, coding, chat, etc.)
- Model size/type (e.g., small, medium, large)
- Model purpose (e.g., general purpose, specialized, etc.)

Return the anchor name that best fits the model.`)

	// Add model information
	modelInfo := "Model Information:\n"
	modelInfo += fmt.Sprintf("  ID: %s\n", model.ModelID)
	modelInfo += fmt.Sprintf("  Author: %s\n", model.Author)
	modelInfo += fmt.Sprintf("  Downloads: %d\n", model.Downloads)
	modelInfo += fmt.Sprintf("  Description: %s\n", model.ReadmeContentPreview)

	fragment = fragment.AddMessage("user", modelInfo)

	// Add available families
	familiesInfo := "Available Model Families:\n"
	for _, family := range availableFamilies {
		familiesInfo += fmt.Sprintf("  - %s (%s)\n", family.Anchor, family.Name)
	}

	fragment = fragment.AddMessage("user", familiesInfo)
	fragment = fragment.AddMessage("user", "Select the most appropriate family anchor for this model. Return just the anchor name.")

	// Get a response
	newFragment, err := llm.Ask(ctx, fragment)
	if err != nil {
		return "", err
	}

	// Extract the selected family
	selectedFamily := strings.TrimSpace(newFragment.LastMessage().Content)

	// Validate that the selected family exists in our list
	for _, family := range availableFamilies {
		if family.Anchor == selectedFamily {
			return selectedFamily, nil
		}
	}

	// If no exact match, try to find a close match
	for _, family := range availableFamilies {
		if strings.Contains(strings.ToLower(family.Anchor), strings.ToLower(selectedFamily)) ||
			strings.Contains(strings.ToLower(selectedFamily), strings.ToLower(family.Anchor)) {
			return family.Anchor, nil
		}
	}

	// Default fallback
	return "llama3", nil
}
