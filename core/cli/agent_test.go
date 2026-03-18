package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAGI/core/state"
)

func TestAgentRunCMD_LoadAgentConfigFromFile(t *testing.T) {
	// Create a temporary agent config file
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "agent.json")

	cfg := state.AgentConfig{
		Name:         "test-agent",
		Model:        "llama3",
		SystemPrompt: "You are a helpful assistant",
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := &AgentRunCMD{
		Config:   configFile,
		StateDir: tmpDir,
	}

	loaded, err := cmd.loadAgentConfig()
	if err != nil {
		t.Fatalf("loadAgentConfig() error: %v", err)
	}
	if loaded.Name != "test-agent" {
		t.Errorf("expected name %q, got %q", "test-agent", loaded.Name)
	}
	if loaded.Model != "llama3" {
		t.Errorf("expected model %q, got %q", "llama3", loaded.Model)
	}
}

func TestAgentRunCMD_LoadAgentConfigFromPool(t *testing.T) {
	tmpDir := t.TempDir()

	pool := map[string]state.AgentConfig{
		"my-agent": {
			Model:        "gpt-4",
			Description:  "A test agent",
			SystemPrompt: "Hello",
		},
		"other-agent": {
			Model: "llama3",
		},
	}
	data, err := json.MarshalIndent(pool, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "pool.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := &AgentRunCMD{
		Name:     "my-agent",
		StateDir: tmpDir,
	}

	loaded, err := cmd.loadAgentConfig()
	if err != nil {
		t.Fatalf("loadAgentConfig() error: %v", err)
	}
	if loaded.Name != "my-agent" {
		t.Errorf("expected name %q, got %q", "my-agent", loaded.Name)
	}
	if loaded.Model != "gpt-4" {
		t.Errorf("expected model %q, got %q", "gpt-4", loaded.Model)
	}
}

func TestAgentRunCMD_LoadAgentConfigFromPool_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	pool := map[string]state.AgentConfig{
		"existing-agent": {Model: "llama3"},
	}
	data, err := json.MarshalIndent(pool, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "pool.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := &AgentRunCMD{
		Name:     "nonexistent",
		StateDir: tmpDir,
	}

	_, err = cmd.loadAgentConfig()
	if err == nil {
		t.Fatal("expected error for missing agent, got nil")
	}
}

func TestAgentRunCMD_LoadAgentConfigNoNameOrConfig(t *testing.T) {
	cmd := &AgentRunCMD{
		StateDir: t.TempDir(),
	}

	_, err := cmd.loadAgentConfig()
	if err == nil {
		t.Fatal("expected error when no pool.json exists, got nil")
	}
}

func TestAgentRunCMD_ApplyOverrides(t *testing.T) {
	cfg := &state.AgentConfig{
		Name: "test",
	}

	cmd := &AgentRunCMD{
		APIURL:       "http://localhost:9090",
		APIKey:       "secret",
		DefaultModel: "my-model",
	}

	cmd.applyOverrides(cfg)

	if cfg.APIURL != "http://localhost:9090" {
		t.Errorf("expected APIURL %q, got %q", "http://localhost:9090", cfg.APIURL)
	}
	if cfg.APIKey != "secret" {
		t.Errorf("expected APIKey %q, got %q", "secret", cfg.APIKey)
	}
	if cfg.Model != "my-model" {
		t.Errorf("expected Model %q, got %q", "my-model", cfg.Model)
	}
}

func TestAgentRunCMD_ApplyOverridesDoesNotOverwriteExisting(t *testing.T) {
	cfg := &state.AgentConfig{
		Name:  "test",
		Model: "existing-model",
	}

	cmd := &AgentRunCMD{
		DefaultModel: "override-model",
	}

	cmd.applyOverrides(cfg)

	if cfg.Model != "existing-model" {
		t.Errorf("expected Model to remain %q, got %q", "existing-model", cfg.Model)
	}
}

func TestAgentRunCMD_LoadConfigMissingName(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "agent.json")

	// Agent config with no name
	cfg := state.AgentConfig{
		Model: "llama3",
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configFile, data, 0644)

	cmd := &AgentRunCMD{
		Config:   configFile,
		StateDir: tmpDir,
	}

	_, err := cmd.loadAgentConfig()
	if err == nil {
		t.Fatal("expected error for config with no name, got nil")
	}
}

func TestAgentListCMD_NoPoolFile(t *testing.T) {
	cmd := &AgentListCMD{
		StateDir: t.TempDir(),
	}

	// Should not error, just print "no agents found"
	err := cmd.Run(nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAgentListCMD_WithAgents(t *testing.T) {
	tmpDir := t.TempDir()

	pool := map[string]state.AgentConfig{
		"agent-a": {Model: "llama3", Description: "First agent"},
		"agent-b": {Model: "gpt-4"},
	}
	data, _ := json.MarshalIndent(pool, "", "  ")
	os.WriteFile(filepath.Join(tmpDir, "pool.json"), data, 0644)

	cmd := &AgentListCMD{
		StateDir: tmpDir,
	}

	err := cmd.Run(nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
