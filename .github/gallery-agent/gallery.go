package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/mudler/LocalAI/core/gallery/importers"
)

func formatTextContent(text string) string {
	return formatTextContentWithIndent(text, 4, 6)
}

// formatTextContentWithIndent formats text content with specified base and list item indentation
func formatTextContentWithIndent(text string, baseIndent int, listItemIndent int) string {
	var formattedLines []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "" {
			// Keep empty lines as empty (no indentation)
			formattedLines = append(formattedLines, "")
		} else {
			// Preserve relative indentation from yaml.Marshal output
			// Count existing leading spaces to preserve relative structure
			leadingSpaces := len(trimmed) - len(strings.TrimLeft(trimmed, " \t"))
			trimmedStripped := strings.TrimLeft(trimmed, " \t")

			var totalIndent int
			if strings.HasPrefix(trimmedStripped, "-") {
				// List items: use listItemIndent (ignore existing leading spaces)
				totalIndent = listItemIndent
			} else {
				// Regular lines: use baseIndent + preserve relative indentation
				// This handles both top-level keys (leadingSpaces=0) and nested properties (leadingSpaces>0)
				totalIndent = baseIndent + leadingSpaces
			}

			indentStr := strings.Repeat(" ", totalIndent)
			formattedLines = append(formattedLines, indentStr+trimmedStripped)
		}
	}
	formattedText := strings.Join(formattedLines, "\n")
	// Remove any trailing spaces from the formatted description
	formattedText = strings.TrimRight(formattedText, " \t")
	return formattedText
}

// generateYAMLEntry generates a YAML entry for a model using the specified anchor
func generateYAMLEntry(model ProcessedModel, quantization string) string {
	modelConfig, err := importers.DiscoverModelConfig("https://huggingface.co/"+model.ModelID, json.RawMessage(`{ "quantization": "`+quantization+`"}`))
	if err != nil {
		panic(err)
	}

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

	description := model.ReadmeContent
	if description == "" {
		description = fmt.Sprintf("AI model: %s", modelName)
	}

	// Clean up description to prevent YAML linting issues
	description = cleanTextContent(description)
	formattedDescription := formatTextContent(description)

	configFile := formatTextContent(modelConfig.ConfigFile)

	filesYAML, _ := yaml.Marshal(modelConfig.Files)

	// Files section: list items need 4 spaces (not 6), since files: is at 2 spaces
	files := formatTextContentWithIndent(string(filesYAML), 4, 4)

	// Build metadata sections
	var metadataSections []string

	// Add license if present
	if model.License != "" {
		metadataSections = append(metadataSections, fmt.Sprintf(`  license: "%s"`, model.License))
	}

	// Add tags if present
	if len(model.Tags) > 0 {
		tagsYAML, _ := yaml.Marshal(model.Tags)
		tagsFormatted := formatTextContentWithIndent(string(tagsYAML), 4, 4)
		tagsFormatted = strings.TrimRight(tagsFormatted, "\n")
		metadataSections = append(metadataSections, fmt.Sprintf("  tags:\n%s", tagsFormatted))
	}

	// Add icon if present
	if model.Icon != "" {
		metadataSections = append(metadataSections, fmt.Sprintf(`  icon: %s`, model.Icon))
	}

	// Build the metadata block
	metadataBlock := ""
	if len(metadataSections) > 0 {
		metadataBlock = strings.Join(metadataSections, "\n") + "\n"
	}

	yamlTemplate := ""
	yamlTemplate = `- name: "%s"
  url: "github:mudler/LocalAI/gallery/virtual.yaml@master"
  urls:
    - https://huggingface.co/%s
  description: |
%s%s
  overrides:
%s
  files:
%s`
	// Trim trailing newlines from formatted sections to prevent extra blank lines
	formattedDescription = strings.TrimRight(formattedDescription, "\n")
	configFile = strings.TrimRight(configFile, "\n")
	files = strings.TrimRight(files, "\n")
	// Add newline before metadata block if present
	if metadataBlock != "" {
		metadataBlock = "\n" + strings.TrimRight(metadataBlock, "\n")
	}
	return fmt.Sprintf(yamlTemplate,
		modelName,
		model.ModelID,
		formattedDescription,
		metadataBlock,
		configFile,
		files,
	)
}

// generateYAMLForModels generates YAML entries for selected models and appends to index.yaml
func generateYAMLForModels(ctx context.Context, models []ProcessedModel, quantization string) error {

	// Generate YAML entries for each model
	var yamlEntries []string
	for _, model := range models {
		fmt.Printf("Generating YAML entry for model: %s\n", model.ModelID)

		// Generate YAML entry
		yamlEntry := generateYAMLEntry(model, quantization)
		yamlEntries = append(yamlEntries, yamlEntry)
	}

	// Prepend to index.yaml (write at the top)
	if len(yamlEntries) > 0 {
		indexPath := getGalleryIndexPath()
		fmt.Printf("Prepending YAML entries to %s...\n", indexPath)

		// Read current content
		content, err := os.ReadFile(indexPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", indexPath, err)
		}

		existingContent := string(content)
		yamlBlock := strings.Join(yamlEntries, "\n")

		// Check if file starts with "---"
		var newContent string
		if strings.HasPrefix(existingContent, "---\n") {
			// File starts with "---", prepend new entries after it
			restOfContent := strings.TrimPrefix(existingContent, "---\n")
			// Ensure proper spacing: "---\n" + new entries + "\n" + rest of content
			newContent = "---\n" + yamlBlock + "\n" + restOfContent
		} else if strings.HasPrefix(existingContent, "---") {
			// File starts with "---" but no newline after
			restOfContent := strings.TrimPrefix(existingContent, "---")
			newContent = "---\n" + yamlBlock + "\n" + strings.TrimPrefix(restOfContent, "\n")
		} else {
			// No "---" at start, prepend new entries at the very beginning
			// Trim leading whitespace from existing content
			existingContent = strings.TrimLeft(existingContent, " \t\n\r")
			newContent = yamlBlock + "\n" + existingContent
		}

		// Write back to file
		err = os.WriteFile(indexPath, []byte(newContent), 0644)
		if err != nil {
			return fmt.Errorf("failed to write %s: %w", indexPath, err)
		}

		fmt.Printf("Successfully prepended %d models to %s\n", len(yamlEntries), indexPath)
	}

	return nil
}
