package langchain

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/huggingface"
)

type HuggingFace struct {
	modelPath string
	token     string
}

func NewHuggingFace(repoId, token string) (*HuggingFace, error) {
	if token == "" {
		return nil, fmt.Errorf("no huggingface token provided")
	}
	return &HuggingFace{
		modelPath: repoId,
		token:     token,
	}, nil
}

func (s *HuggingFace) PredictHuggingFace(text string, opts ...PredictOption) (*Predict, error) {
	po := NewPredictOptions(opts...)

	// Init client
	llm, err := huggingface.New(huggingface.WithToken(s.token))
	if err != nil {
		return nil, err
	}

	// Convert from LocalAI to LangChainGo format of options
	co := []llms.CallOption{
		llms.WithModel(po.Model),
		llms.WithMaxTokens(po.MaxTokens),
		llms.WithTemperature(po.Temperature),
		llms.WithStopWords(po.StopWords),
	}

	// Call Inference API
	ctx := context.Background()
	completion, err := llm.Call(ctx, text, co...)
	if err != nil {
		return nil, err
	}

	return &Predict{
		Completion: completion,
	}, nil
}
