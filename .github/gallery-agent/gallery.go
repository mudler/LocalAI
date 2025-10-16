package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// generateYAMLEntry generates a YAML entry for a model using the specified anchor
func generateYAMLEntry(model ProcessedModel, familyAnchor string) string {
	// Extract model name from ModelID
	parts := strings.Split(model.ModelID, "/")
	modelName := model.ModelID
	if len(parts) > 0 {
		modelName = strings.ToLower(parts[len(parts)-1])
	}
	// Remove common suffixes
	modelName = strings.ReplaceAll(modelName, "-gguf", "")
	modelName = strings.ReplaceAll(modelName, "-q4_k_m", "")
	modelName = strings.ReplaceAll(modelName, "-q4_k_s", "")
	modelName = strings.ReplaceAll(modelName, "-q3_k_m", "")
	modelName = strings.ReplaceAll(modelName, "-q2_k", "")

	fileName := ""
	checksum := ""
	if model.PreferredModelFile != nil {
		fileParts := strings.Split(model.PreferredModelFile.Path, "/")
		if len(fileParts) > 0 {
			fileName = fileParts[len(fileParts)-1]
		}
		checksum = model.PreferredModelFile.SHA256
	} else {
		fileName = model.ModelID
	}

	description := model.ReadmeContent
	if description == "" {
		description = fmt.Sprintf("AI model: %s", modelName)
	}

	// Clean up description to prevent YAML linting issues
	description = cleanTextContent(description)

	// Format description for YAML (indent each line and ensure no trailing spaces)
	formattedDescription := strings.ReplaceAll(description, "\n", "\n    ")
	// Remove any trailing spaces from the formatted description
	formattedDescription = strings.TrimRight(formattedDescription, " \t")
	yamlTemplate := ""
	if checksum != "" {
		yamlTemplate = `- !!merge <<: *%s
  name: "%s"
  urls:
    - https://huggingface.co/%s
  description: |
    %s
  overrides:
    parameters:
      model: %s
  files:
    - filename: %s
      sha256: %s
      uri: huggingface://%s/%s
`
		return fmt.Sprintf(yamlTemplate,
			familyAnchor,
			modelName,
			model.ModelID,
			formattedDescription,
			fileName,
			fileName,
			checksum,
			model.ModelID,
			fileName,
		)
	} else {
		yamlTemplate = `- !!merge <<: *%s
  name: "%s"
  urls:
    - https://huggingface.co/%s
  description: |
    %s
  overrides:
    parameters:
      model: %s
`
		return fmt.Sprintf(yamlTemplate,
			familyAnchor,
			modelName,
			model.ModelID,
			formattedDescription,
			fileName,
		)
	}
}

// extractModelFamilies extracts all YAML anchors from the gallery index.yaml file
func extractModelFamilies() ([]ModelFamily, error) {
	// Read the index.yaml file
	indexPath := getGalleryIndexPath()
	content, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", indexPath, err)
	}

	lines := strings.Split(string(content), "\n")
	var families []ModelFamily

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Look for YAML anchors (lines starting with "- &")
		if strings.HasPrefix(line, "- &") {
			// Extract the anchor name (everything after "- &")
			anchor := strings.TrimPrefix(line, "- &")
			// Remove any trailing colon or other characters
			anchor = strings.Split(anchor, ":")[0]
			anchor = strings.Split(anchor, " ")[0]

			if anchor != "" {
				families = append(families, ModelFamily{
					Anchor: anchor,
					Name:   anchor, // Use anchor as name for now
				})
			}
		}
	}

	return families, nil
}

// generateYAMLForModels generates YAML entries for selected models and appends to index.yaml
func generateYAMLForModels(ctx context.Context, models []ProcessedModel) error {
	// Extract available model families
	families, err := extractModelFamilies()
	if err != nil {
		return fmt.Errorf("failed to extract model families: %w", err)
	}

	fmt.Printf("Found %d model families: %v\n", len(families),
		func() []string {
			var names []string
			for _, f := range families {
				names = append(names, f.Anchor)
			}
			return names
		}())

	// Generate YAML entries for each model
	var yamlEntries []string
	for _, model := range models {
		fmt.Printf("Selecting family for model: %s\n", model.ModelID)

		// Select appropriate family for this model
		familyAnchor, err := selectModelFamily(ctx, model, families)
		if err != nil {
			fmt.Printf("Error selecting family for %s: %v, using default\n", model.ModelID, err)
			familyAnchor = "llama3" // Default fallback
		}

		fmt.Printf("Selected family '%s' for model %s\n", familyAnchor, model.ModelID)

		// Generate YAML entry
		yamlEntry := generateYAMLEntry(model, familyAnchor)
		yamlEntries = append(yamlEntries, yamlEntry)
	}

	// Append to index.yaml
	if len(yamlEntries) > 0 {
		indexPath := getGalleryIndexPath()
		fmt.Printf("Appending YAML entries to %s...\n", indexPath)

		// Read current content
		content, err := os.ReadFile(indexPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", indexPath, err)
		}

		// Append new entries
		// Remove trailing whitespace from existing content and join entries without extra newlines
		existingContent := strings.TrimRight(string(content), " \t\n\r")
		yamlBlock := strings.Join(yamlEntries, "\n")
		newContent := existingContent + "\n" + yamlBlock

		// Write back to file
		err = os.WriteFile(indexPath, []byte(newContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write %s: %w", indexPath, err)
		}

		fmt.Printf("Successfully added %d models to %s\n", len(yamlEntries), indexPath)
	}

	return nil
}
