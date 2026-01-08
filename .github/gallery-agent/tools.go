package main

import (
	"fmt"

	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	openai "github.com/sashabaranov/go-openai"
	jsonschema "github.com/sashabaranov/go-openai/jsonschema"
)

// Get repository README from HF
type HFReadmeTool struct {
	client *hfapi.Client
}

func (s *HFReadmeTool) Execute(args map[string]any) (string, error) {
	q, ok := args["repository"].(string)
	if !ok {
		return "", fmt.Errorf("no query")
	}
	readme, err := s.client.GetReadmeContent(q, "README.md")
	if err != nil {
		return "", err
	}
	return readme, nil
}

func (s *HFReadmeTool) Tool() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "hf_readme",
			Description: "A tool to get the README content of a huggingface repository",
			Parameters: jsonschema.Definition{
				Type: jsonschema.Object,
				Properties: map[string]jsonschema.Definition{
					"repository": {
						Type:        jsonschema.String,
						Description: "The huggingface repository to get the README content of",
					},
				},
				Required: []string{"repository"},
			},
		},
	}
}
