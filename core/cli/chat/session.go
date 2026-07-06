package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	chatRoleUser      = "user"
	chatRoleAssistant = "assistant"
)

type chatMessage struct {
	Role    string
	Content string
}

type chatSession struct {
	client   chatClient
	model    string
	models   []string
	messages []chatMessage
}

func newChatSession(ctx context.Context, client chatClient, requestedModel string) (*chatSession, error) {
	models, err := client.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}

	model, err := resolveChatModel(requestedModel, models)
	if err != nil {
		return nil, err
	}

	return &chatSession{
		client: client,
		model:  model,
		models: models,
	}, nil
}

func (s *chatSession) CurrentModel() string {
	return s.model
}

func (s *chatSession) Models() []string {
	models := make([]string, len(s.models))
	copy(models, s.models)
	return models
}

func (s *chatSession) Clear() {
	s.messages = nil
}

func (s *chatSession) SwitchModel(model string) error {
	if !slices.Contains(s.models, model) {
		return fmt.Errorf("model %q is not available. Use /models to see installed models", model)
	}
	s.model = model
	s.Clear()
	return nil
}

func (s *chatSession) Send(ctx context.Context, prompt string, out io.Writer) error {
	s.messages = append(s.messages, chatMessage{
		Role:    chatRoleUser,
		Content: prompt,
	})

	answer, err := s.client.StreamChat(ctx, s.model, s.messages, out)
	if err != nil {
		return err
	}

	s.messages = append(s.messages, chatMessage{
		Role:    chatRoleAssistant,
		Content: answer,
	})
	return nil
}

func resolveChatModel(requested string, models []string) (string, error) {
	switch {
	case requested == "" && len(models) == 0:
		return "", errors.New(`no chat models are installed.

Install a model first, for example:
  local-ai models list
  local-ai models install <model>
  local-ai run

Then start a chat session:
  local-ai chat --model <model>`)
	case requested == "" && len(models) == 1:
		return models[0], nil
	case requested == "" && len(models) > 1:
		var b strings.Builder
		b.WriteString("multiple models are available; choose one with --model:\n")
		b.WriteString(formatChatModelList(models, ""))
		return "", errors.New(b.String())
	case !slices.Contains(models, requested):
		return "", fmt.Errorf("model %q is not available. Use `local-ai models list` and `local-ai models install <model>`, or pass an installed model with --model", requested)
	default:
		return requested, nil
	}
}
