package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/ghodss/yaml"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	cogito "github.com/mudler/cogito"

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
	return stripThinkingTags(strings.TrimRight(result, "\n"))
}

type galleryModel struct {
	Name string   `yaml:"name"`
	Urls []string `yaml:"urls"`
}

// isModelExisting checks if a specific model ID exists in the gallery using text search
func isModelExisting(modelID string) (bool, error) {
	indexPath := getGalleryIndexPath()
	content, err := os.ReadFile(indexPath)
	if err != nil {
		return false, fmt.Errorf("failed to read %s: %w", indexPath, err)
	}

	var galleryModels []galleryModel

	err = yaml.Unmarshal(content, &galleryModels)
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal %s: %w", indexPath, err)
	}

	for _, galleryModel := range galleryModels {
		if slices.Contains(galleryModel.Urls, modelID) {
			return true, nil
		}
	}

	return false, nil
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

func stripThinkingTags(content string) string {
	// Remove content between <thinking> and </thinking> (including multi-line)
	content = regexp.MustCompile(`(?s)<thinking>.*?</thinking>`).ReplaceAllString(content, "")
	// Remove content between <think> and </think> (including multi-line)
	content = regexp.MustCompile(`(?s)<think>.*?</think>`).ReplaceAllString(content, "")
	// Clean up any extra whitespace
	content = strings.TrimSpace(content)
	return content
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

// ModelMetadata represents extracted metadata from a model
type ModelMetadata struct {
	Tags    []string `json:"tags"`
	License string   `json:"license"`
}

// extractModelMetadata extracts tags and license from model README and documentation
func extractModelMetadata(ctx context.Context, model ProcessedModel) ([]string, string, error) {
	// Create a conversation fragment
	fragment := cogito.NewEmptyFragment().
		AddMessage("user",
			`Your task is to extract metadata from an AI model's README and documentation. You will be provided with:
1. Model information (ID, author, description)
2. README content

You need to extract:
1. **Tags**: An array of relevant tags that describe the model. Use common tags from the gallery such as:
   - llm, gguf, gpu, cpu, multimodal, image-to-text, text-to-text, text-to-speech, tts
   - thinking, reasoning, chat, instruction-tuned, code, vision
   - Model family names (e.g., llama, qwen, mistral, gemma) if applicable
   - Any other relevant descriptive tags
   Select 3-8 most relevant tags.

2. **License**: The license identifier (e.g., "apache-2.0", "mit", "llama2", "gpl-3.0", "bsd", "cc-by-4.0").
   If no license is found, return an empty string.

Return the extracted metadata in a structured format.`)

	// Add model information
	modelInfo := "Model Information:\n"
	modelInfo += fmt.Sprintf("  ID: %s\n", model.ModelID)
	modelInfo += fmt.Sprintf("  Author: %s\n", model.Author)
	modelInfo += fmt.Sprintf("  Downloads: %d\n", model.Downloads)
	if model.ReadmeContent != "" {
		modelInfo += fmt.Sprintf("  README Content:\n%s\n", model.ReadmeContent)
	} else if model.ReadmeContentPreview != "" {
		modelInfo += fmt.Sprintf("  README Preview: %s\n", model.ReadmeContentPreview)
	}

	fragment = fragment.AddMessage("user", modelInfo)
	fragment = fragment.AddMessage("user", "Extract the tags and license from the model information. Return the metadata as a JSON object with 'tags' (array of strings) and 'license' (string).")

	// Get a response
	newFragment, err := llm.Ask(ctx, fragment)
	if err != nil {
		return nil, "", err
	}

	// Extract structured metadata
	metadata := ModelMetadata{}

	s := structures.Structure{
		Schema: jsonschema.Definition{
			Type:                 jsonschema.Object,
			AdditionalProperties: false,
			Properties: map[string]jsonschema.Definition{
				"tags": {
					Type:        jsonschema.Array,
					Items:       &jsonschema.Definition{Type: jsonschema.String},
					Description: "Array of relevant tags describing the model",
				},
				"license": {
					Type:        jsonschema.String,
					Description: "License identifier (e.g., apache-2.0, mit, llama2). Empty string if not found.",
				},
			},
			Required: []string{"tags", "license"},
		},
		Object: &metadata,
	}

	err = newFragment.ExtractStructure(ctx, llm, s)
	if err != nil {
		return nil, "", err
	}

	return metadata.Tags, metadata.License, nil
}

// extractIconFromReadme scans the README content for image URLs and returns the first suitable icon URL found
func extractIconFromReadme(readmeContent string) string {
	if readmeContent == "" {
		return ""
	}

	// Regular expressions to match image URLs in various formats (case-insensitive)
	// Match markdown image syntax: ![alt](url) - case insensitive extensions
	markdownImageRegex := regexp.MustCompile(`(?i)!\[[^\]]*\]\(([^)]+\.(png|jpg|jpeg|svg|webp|gif))\)`)
	// Match HTML img tags: <img src="url">
	htmlImageRegex := regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+\.(png|jpg|jpeg|svg|webp|gif))["']`)
	// Match plain URLs ending with image extensions
	plainImageRegex := regexp.MustCompile(`(?i)https?://[^\s<>"']+\.(png|jpg|jpeg|svg|webp|gif)`)

	// Try markdown format first
	matches := markdownImageRegex.FindStringSubmatch(readmeContent)
	if len(matches) > 1 && matches[1] != "" {
		url := strings.TrimSpace(matches[1])
		// Prefer HuggingFace CDN URLs or absolute URLs
		if strings.HasPrefix(strings.ToLower(url), "http") {
			return url
		}
	}

	// Try HTML img tags
	matches = htmlImageRegex.FindStringSubmatch(readmeContent)
	if len(matches) > 1 && matches[1] != "" {
		url := strings.TrimSpace(matches[1])
		if strings.HasPrefix(strings.ToLower(url), "http") {
			return url
		}
	}

	// Try plain URLs
	matches = plainImageRegex.FindStringSubmatch(readmeContent)
	if len(matches) > 0 {
		url := strings.TrimSpace(matches[0])
		if strings.HasPrefix(strings.ToLower(url), "http") {
			return url
		}
	}

	return ""
}

// getHuggingFaceAvatarURL attempts to get the HuggingFace avatar URL for a user
func getHuggingFaceAvatarURL(author string) string {
	if author == "" {
		return ""
	}

	// Try to fetch user info from HuggingFace API
	// HuggingFace API endpoint: https://huggingface.co/api/users/{username}
	baseURL := "https://huggingface.co"
	userURL := fmt.Sprintf("%s/api/users/%s", baseURL, author)

	req, err := http.NewRequest("GET", userURL, nil)
	if err != nil {
		return ""
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	// Parse the response to get avatar URL
	var userInfo map[string]interface{}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	if err := json.Unmarshal(body, &userInfo); err != nil {
		return ""
	}

	// Try to extract avatar URL from response
	if avatar, ok := userInfo["avatarUrl"].(string); ok && avatar != "" {
		return avatar
	}
	if avatar, ok := userInfo["avatar"].(string); ok && avatar != "" {
		return avatar
	}

	return ""
}

// extractModelIcon extracts icon URL from README or falls back to HuggingFace avatar
func extractModelIcon(model ProcessedModel) string {
	// First, try to extract icon from README
	if icon := extractIconFromReadme(model.ReadmeContent); icon != "" {
		return icon
	}

	// Fallback: Try to get HuggingFace user avatar
	if model.Author != "" {
		if avatar := getHuggingFaceAvatarURL(model.Author); avatar != "" {
			return avatar
		}
	}

	return ""
}
