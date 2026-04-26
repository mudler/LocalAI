package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
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
	Tags                    []string             `json:"tags,omitempty"`
	License                 string               `json:"license,omitempty"`
	Icon                    string               `json:"icon,omitempty"`
}

// AddedModelSummary represents a summary of models added to the gallery
type AddedModelSummary struct {
	SearchTerm     string   `json:"search_term"`
	TotalFound     int      `json:"total_found"`
	ModelsAdded    int      `json:"models_added"`
	AddedModelIDs  []string `json:"added_model_ids"`
	AddedModelURLs []string `json:"added_model_urls"`
	Quantization   string   `json:"quantization"`
	ProcessingTime string   `json:"processing_time"`
}

func main() {
	startTime := time.Now()

	// Synthetic mode for local testing
	if sm := os.Getenv("SYNTHETIC_MODE"); sm == "true" || sm == "1" {
		fmt.Println("Running in SYNTHETIC MODE - generating random test data")
		if err := runSyntheticMode(); err != nil {
			fmt.Fprintf(os.Stderr, "Error in synthetic mode: %v\n", err)
			os.Exit(1)
		}
		return
	}

	searchTerm := os.Getenv("SEARCH_TERM")
	if searchTerm == "" {
		searchTerm = "GGUF"
	}

	limitStr := os.Getenv("LIMIT")
	if limitStr == "" {
		limitStr = "15"
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing LIMIT: %v\n", err)
		os.Exit(1)
	}

	quantization := os.Getenv("QUANTIZATION")
	if quantization == "" {
		quantization = "Q4_K_M"
	}

	maxModelsStr := os.Getenv("MAX_MODELS")
	if maxModelsStr == "" {
		maxModelsStr = "1"
	}
	maxModels, err := strconv.Atoi(maxModelsStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing MAX_MODELS: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Gallery Agent Configuration:\n")
	fmt.Printf("  Search Term: %s\n", searchTerm)
	fmt.Printf("  Limit: %d\n", limit)
	fmt.Printf("  Quantization: %s\n", quantization)
	fmt.Printf("  Max Models to Add: %d\n", maxModels)
	fmt.Printf("  Gallery Index Path: %s\n", getGalleryIndexPath())
	fmt.Println()

	// Phase 1: load current gallery and query HuggingFace.
	gallerySet, err := loadGalleryURLSet()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading gallery index: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Loaded %d existing gallery entries\n", len(gallerySet))

	client := hfapi.NewClient()

	fmt.Println("Searching for trending models on HuggingFace...")
	rawModels, err := client.GetTrending(searchTerm, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching models: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found %d trending models matching %q\n", len(rawModels), searchTerm)
	totalFound := len(rawModels)

	// Phase 2: drop anything already in the gallery *before* any expensive
	// per-model work (GetModelDetails, README fetches, icon lookups).
	fresh := rawModels[:0]
	for _, m := range rawModels {
		if modelAlreadyInGallery(gallerySet, m.ModelID) {
			fmt.Printf("Skipping existing model: %s\n", m.ModelID)
			continue
		}
		fresh = append(fresh, m)
	}
	fmt.Printf("%d candidates after gallery dedup\n", len(fresh))

	// Phase 3: HuggingFace already returned these in trendingScore order —
	// just cap to MAX_MODELS.
	if len(fresh) > maxModels {
		fresh = fresh[:maxModels]
	}
	if len(fresh) == 0 {
		fmt.Println("No new models to add to the gallery.")
		writeSummary(AddedModelSummary{
			SearchTerm:     searchTerm,
			TotalFound:     totalFound,
			ModelsAdded:    0,
			Quantization:   quantization,
			ProcessingTime: time.Since(startTime).String(),
		})
		return
	}

	// Phase 4: fetch details and build ProcessedModel entries for survivors.
	var processed []ProcessedModel
	quantPrefs := []string{quantization, "Q4_K_M", "Q4_K_S", "Q3_K_M", "Q2_K", "Q8_0"}
	for _, m := range fresh {
		fmt.Printf("Processing model: %s (downloads=%d)\n", m.ModelID, m.Downloads)

		pm := ProcessedModel{
			ModelID:                 m.ModelID,
			Author:                  m.Author,
			Downloads:               m.Downloads,
			LastModified:            m.LastModified,
			QuantizationPreferences: quantPrefs,
		}

		details, err := client.GetModelDetails(m.ModelID)
		if err != nil {
			fmt.Printf("  Error getting model details: %v (skipping)\n", err)
			continue
		}

		preferred := hfapi.FindPreferredModelFile(details.Files, quantPrefs)
		if preferred == nil {
			fmt.Printf("  No GGUF file matching %v — skipping\n", quantPrefs)
			continue
		}

		pm.Files = make([]ProcessedModelFile, len(details.Files))
		for j, f := range details.Files {
			fileType := "other"
			if f.IsReadme {
				fileType = "readme"
			} else if f.Path == preferred.Path {
				fileType = "model"
			}
			pm.Files[j] = ProcessedModelFile{
				Path:     f.Path,
				Size:     f.Size,
				SHA256:   f.SHA256,
				IsReadme: f.IsReadme,
				FileType: fileType,
			}
			if f.Path == preferred.Path {
				copyFile := pm.Files[j]
				pm.PreferredModelFile = &copyFile
			}
			if f.IsReadme {
				copyFile := pm.Files[j]
				pm.ReadmeFile = &copyFile
			}
		}

		// Deterministic README resolution: follow base_model tag if set.
		// Keep the raw (HTML-bearing) README around while we extract the
		// icon, then strip it down to a plain-text description for the
		// `description:` YAML field.
		readme, err := resolveReadme(client, m.ModelID, m.Tags)
		if err != nil {
			fmt.Printf("  Warning: failed to fetch README: %v\n", err)
		}
		pm.ReadmeContent = readme

		pm.License = licenseFromTags(m.Tags)
		pm.Tags = curatedTags(m.Tags)
		pm.Icon = extractModelIcon(pm)

		if pm.ReadmeContent != "" {
			pm.ReadmeContent = extractDescription(pm.ReadmeContent)
			pm.ReadmeContentPreview = truncateString(pm.ReadmeContent, 200)
		}

		fmt.Printf("  License: %s, Tags: %v, Icon: %s\n", pm.License, pm.Tags, pm.Icon)
		processed = append(processed, pm)
	}

	if len(processed) == 0 {
		fmt.Println("No processable models after detail fetch.")
		writeSummary(AddedModelSummary{
			SearchTerm:     searchTerm,
			TotalFound:     totalFound,
			ModelsAdded:    0,
			Quantization:   quantization,
			ProcessingTime: time.Since(startTime).String(),
		})
		return
	}

	// Phase 5: write YAML entries.
	var addedIDs, addedURLs []string
	for _, pm := range processed {
		addedIDs = append(addedIDs, pm.ModelID)
		addedURLs = append(addedURLs, "https://huggingface.co/"+pm.ModelID)
	}

	fmt.Println("Generating YAML entries for selected models...")
	if err := generateYAMLForModels(context.Background(), processed, quantization); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating YAML entries: %v\n", err)
		os.Exit(1)
	}

	writeSummary(AddedModelSummary{
		SearchTerm:     searchTerm,
		TotalFound:     totalFound,
		ModelsAdded:    len(addedIDs),
		AddedModelIDs:  addedIDs,
		AddedModelURLs: addedURLs,
		Quantization:   quantization,
		ProcessingTime: time.Since(startTime).String(),
	})
}

func writeSummary(summary AddedModelSummary) {
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling summary: %v\n", err)
		return
	}
	if err := os.WriteFile("gallery-agent-summary.json", data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing summary file: %v\n", err)
		return
	}
	fmt.Println("Summary written to gallery-agent-summary.json")
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

