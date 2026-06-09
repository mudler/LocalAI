package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

type chatClient interface {
	ListModels(ctx context.Context) ([]string, error)
	StreamChat(ctx context.Context, model string, messages []chatMessage, out io.Writer) (string, error)
}

type localAIChatClient struct {
	client *openai.Client
}

func newLocalAIChatClient(baseURL string, apiKey string) *localAIChatClient {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &localAIChatClient{client: openai.NewClientWithConfig(cfg)}
}

func (c *localAIChatClient) ListModels(ctx context.Context) ([]string, error) {
	resp, err := c.client.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]string, 0, len(resp.Models))
	for _, model := range resp.Models {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

func (c *localAIChatClient) StreamChat(ctx context.Context, model string, messages []chatMessage, out io.Writer) (string, error) {
	stream, err := c.client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    model,
		Messages: openAIChatMessages(messages),
	})
	if err != nil {
		return "", friendlyChatError(err, model)
	}
	defer stream.Close()

	var answer strings.Builder
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return answer.String(), friendlyChatError(err, model)
		}
		if len(resp.Choices) == 0 {
			continue
		}

		token := resp.Choices[0].Delta.Content
		if token == "" {
			continue
		}
		answer.WriteString(token)
		if _, err := fmt.Fprint(out, token); err != nil {
			return answer.String(), err
		}
	}

	return answer.String(), nil
}

func openAIChatMessages(messages []chatMessage) []openai.ChatCompletionMessage {
	converted := make([]openai.ChatCompletionMessage, len(messages))
	for i, message := range messages {
		converted[i] = openai.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
		}
	}
	return converted
}

func friendlyChatError(err error, model string) error {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 404:
			return fmt.Errorf("model %q is not available. Run `local-ai models list`, install a model with `local-ai models install <model>`, or switch with `/model <name>`", model)
		case 403:
			return fmt.Errorf("model %q is disabled. Enable it from LocalAI settings or choose another model with `/model <name>`", model)
		}
		if apiErr.Message != "" {
			return errors.New(apiErr.Message)
		}
	}

	msg := err.Error()
	if strings.Contains(msg, "model") && strings.Contains(msg, "not found") {
		return fmt.Errorf("model %q is not available. Run `local-ai models list`, install a model with `local-ai models install <model>`, or switch with `/model <name>`", model)
	}

	return err
}
