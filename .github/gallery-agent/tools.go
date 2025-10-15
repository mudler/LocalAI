package main

import (
	"fmt"

	"github.com/go-skynet/LocalAI/.github/gallery-agent/hfapi"
	"github.com/sashabaranov/go-openai"
	"github.com/tmc/langchaingo/jsonschema"
)

// Get repository README from HF
type HFReadmeTool struct {
	client *hfapi.Client
}

func (s *HFReadmeTool) Run(args map[string]any) (string, error) {
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
