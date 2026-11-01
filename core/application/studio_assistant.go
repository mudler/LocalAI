package application

import (
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
