package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/go-skynet/LocalAI/.github/gallery-agent/hfapi"
)

// ProcessedModelFile represents a processed model file with additional metadata
type ProcessedModelFile struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	IsReadme bool   `json:"is_readme"`
	FileType string `json:"file_type"` // "model", "readme", "other"
}

// ProcessedModel represents a processed model with all gathered metadata
type ProcessedModel struct {
	ModelID                 string               `json:"model_id"`
	Author                  string               `json:"author"`
	Downloads               int                  `json:"downloads"`
	LastModified            string               `json:"last_modified"`
	Files                   []ProcessedModelFile `json:"files"`
	PreferredModelFile      *ProcessedModelFile  `json:"preferred_model_file,omitempty"`
	ReadmeFile              *ProcessedModelFile  `json:"readme_file,omitempty"`
	ReadmeContent           string               `json:"readme_content,omitempty"`
	ReadmeContentPreview    string               `json:"readme_content_preview,omitempty"`
	QuantizationPreferences []string             `json:"quantization_preferences"`
	ProcessingError         string               `json:"processing_error,omitempty"`
}

// SearchResult represents the complete result of searching and processing models
type SearchResult struct {
	SearchTerm       string           `json:"search_term"`
	Limit            int              `json:"limit"`
	Quantization     string           `json:"quantization"`
	TotalModelsFound int              `json:"total_models_found"`
	Models           []ProcessedModel `json:"models"`
	FormattedOutput  string           `json:"formatted_output"`
}

func main() {
	searchTerm := "GGUF"
	limit := 30
	quantization := "Q4_K_M"

	// Parse command line arguments
	if len(os.Args) > 1 && os.Args[1] != "" {
		searchTerm = os.Args[1]
	}
	if len(os.Args) > 2 {
		if parsedLimit, err := strconv.Atoi(os.Args[2]); err == nil {
			limit = parsedLimit
		}
	}
	if len(os.Args) > 3 {
		quantization = os.Args[3]
	}

	result, err := searchAndProcessModels(searchTerm, limit, quantization)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(result.FormattedOutput)

	// Use AI agent to select the most interesting models
	fmt.Println("Using AI agent to select the most interesting models...")
	models, err := selectMostInterestingModels(context.Background(), result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error in model selection: %v\n", err)
		// Continue with original result if selection fails
		models = result.Models
	}

	fmt.Print(models)
}

func searchAndProcessModels(searchTerm string, limit int, quantization string) (*SearchResult, error) {
	client := hfapi.NewClient()
	var outputBuilder strings.Builder

	fmt.Println("Searching for models...")
	// Initialize the result struct
	result := &SearchResult{
		SearchTerm:   searchTerm,
		Limit:        limit,
		Quantization: quantization,
		Models:       []ProcessedModel{},
	}

	models, err := client.GetLatest(searchTerm, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}

	fmt.Println("Models found:", len(models))
	result.TotalModelsFound = len(models)

	if len(models) == 0 {
		outputBuilder.WriteString("No models found.\n")
		result.FormattedOutput = outputBuilder.String()
		return result, nil
	}

	outputBuilder.WriteString(fmt.Sprintf("Found %d models matching '%s':\n\n", len(models), searchTerm))

	// Process each model
	for i, model := range models {
		outputBuilder.WriteString(fmt.Sprintf("%d. Processing Model: %s\n", i+1, model.ModelID))
		outputBuilder.WriteString(fmt.Sprintf("   Author: %s\n", model.Author))
		outputBuilder.WriteString(fmt.Sprintf("   Downloads: %d\n", model.Downloads))
		outputBuilder.WriteString(fmt.Sprintf("   Last Modified: %s\n", model.LastModified))

		// Initialize processed model struct
		processedModel := ProcessedModel{
			ModelID:                 model.ModelID,
			Author:                  model.Author,
			Downloads:               model.Downloads,
			LastModified:            model.LastModified,
			QuantizationPreferences: []string{quantization, "Q4_K_M", "Q4_K_S", "Q3_K_M", "Q2_K"},
		}

		// Get detailed model information
		details, err := client.GetModelDetails(model.ModelID)
		if err != nil {
			errorMsg := fmt.Sprintf("   Error getting model details: %v\n", err)
			outputBuilder.WriteString(errorMsg)
			processedModel.ProcessingError = err.Error()
			result.Models = append(result.Models, processedModel)
			continue
		}

		// Define quantization preferences (in order of preference)
		quantizationPreferences := []string{quantization, "Q4_K_M", "Q4_K_S", "Q3_K_M", "Q2_K"}

		// Find preferred model file
		preferredModelFile := hfapi.FindPreferredModelFile(details.Files, quantizationPreferences)

		// Process files
		processedFiles := make([]ProcessedModelFile, len(details.Files))
		for j, file := range details.Files {
			fileType := "other"
			if file.IsReadme {
				fileType = "readme"
			} else if preferredModelFile != nil && file.Path == preferredModelFile.Path {
				fileType = "model"
			}

			processedFiles[j] = ProcessedModelFile{
				Path:     file.Path,
				Size:     file.Size,
				SHA256:   file.SHA256,
				IsReadme: file.IsReadme,
				FileType: fileType,
			}
		}

		processedModel.Files = processedFiles

		// Set preferred model file
		if preferredModelFile != nil {
			for _, file := range processedFiles {
				if file.Path == preferredModelFile.Path {
					processedModel.PreferredModelFile = &file
					break
				}
			}
		}

		// Print file information
		outputBuilder.WriteString(fmt.Sprintf("   Files found: %d\n", len(details.Files)))

		if preferredModelFile != nil {
			outputBuilder.WriteString(fmt.Sprintf("   Preferred Model File: %s (SHA256: %s)\n",
				preferredModelFile.Path,
				preferredModelFile.SHA256))
		} else {
			outputBuilder.WriteString(fmt.Sprintf("   No model file found with quantization preferences: %v\n", quantizationPreferences))
		}

		if details.ReadmeFile != nil {
			outputBuilder.WriteString(fmt.Sprintf("   README File: %s\n", details.ReadmeFile.Path))

			// Find and set readme file
			for _, file := range processedFiles {
				if file.IsReadme {
					processedModel.ReadmeFile = &file
					break
				}
			}

			fmt.Println("Getting real readme for", model.ModelID, "waiting...")
			// Use agent to get the real readme and prepare the model description
			readmeContent, err := getRealReadme(context.Background(), model.ModelID)
			if err == nil {
				processedModel.ReadmeContent = readmeContent
				processedModel.ReadmeContentPreview = truncateString(readmeContent, 200)
				outputBuilder.WriteString(fmt.Sprintf("   README Content Preview: %s\n",
					processedModel.ReadmeContentPreview))
			} else {
				panic(err)
			}
			fmt.Println("Real readme got", readmeContent)
			// Get README content
			// readmeContent, err := client.GetReadmeContent(model.ModelID, details.ReadmeFile.Path)
			// if err == nil {
			// 	processedModel.ReadmeContent = readmeContent
			// 	processedModel.ReadmeContentPreview = truncateString(readmeContent, 200)
			// 	outputBuilder.WriteString(fmt.Sprintf("   README Content Preview: %s\n",
			// 		processedModel.ReadmeContentPreview))
			// }
		}

		// Print all files with their checksums
		outputBuilder.WriteString("   All Files:\n")
		for _, file := range processedFiles {
			outputBuilder.WriteString(fmt.Sprintf("     - %s (%s, %d bytes", file.Path, file.FileType, file.Size))
			if file.SHA256 != "" {
				outputBuilder.WriteString(fmt.Sprintf(", SHA256: %s", file.SHA256))
			}
			outputBuilder.WriteString(")\n")
		}

		outputBuilder.WriteString("\n")
		result.Models = append(result.Models, processedModel)
	}

	result.FormattedOutput = outputBuilder.String()
	return result, nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
