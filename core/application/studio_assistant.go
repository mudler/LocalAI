package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
)

// studioAssistant gives the chats the LocalAI assistant. It looks the holder up
// at each call: the holder is built after the chats service starts. It reads the
// setting live, so switching it off in the settings takes effect on the next
// turn without a restart.
type studioAssistant struct{ a *Application }

// Enabled is true while the setting is on and the assistant tools exist.
func (s studioAssistant) Enabled() bool {
	if s.a.applicationConfig.DisableLocalAIAssistant {
		return false
	}
	return s.a.localAIAssistant.HasTools()
}

// Executor runs the assistant tools. It has none when the assistant is absent.
func (s studioAssistant) Executor() mcpTools.ToolExecutor {
	if s.a.localAIAssistant == nil {
		return &mcpTools.LocalToolExecutor{}
	}
	return s.a.localAIAssistant.Executor()
}

// SystemPrompt is the prompt of the assistant.
func (s studioAssistant) SystemPrompt() string { return s.a.localAIAssistant.SystemPrompt() }

// ClientExecutor runs the client tools (inference). It has none when the
// assistant is absent.
func (s studioAssistant) ClientExecutor() mcpTools.ToolExecutor {
	if s.a.localAIAssistant == nil {
		return &mcpTools.LocalToolExecutor{}
	}
	return s.a.localAIAssistant.ClientExecutor()
}

// FirstModel chooses the model of a client tool: the first installed model with
// the use case, as the tool itself does when the model names none.
func (s studioAssistant) FirstModel(ctx context.Context, use config.ModelConfigUsecase) (string, error) {
	if s.a.assistantClient == nil {
		return "", nil
	}
	return s.a.assistantClient.FirstModel(ctx, use)
}
