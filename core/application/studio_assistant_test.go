package application

import (
	"testing"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
)

func TestStudioAssistantIsOffWithoutTheServerOrWithTheSettingOff(t *testing.T) {
	cfg := config.NewApplicationConfig()
	a := &Application{applicationConfig: cfg}
	as := studioAssistant{a}
	if as.Enabled() {
		t.Fatal("Enabled with no assistant server")
	}
	if as.SystemPrompt() != "" || as.Executor().HasTools() {
		t.Fatal("an absent assistant must give no prompt and no tools")
	}

	// A holder that never initialised has no tools either, setting on or off.
	a.localAIAssistant = mcpTools.NewLocalAIAssistantHolder()
	if as.Enabled() {
		t.Fatal("Enabled with a holder that has no tools")
	}
	cfg.DisableLocalAIAssistant = true
	if as.Enabled() {
		t.Fatal("Enabled while the setting is off")
	}
}
