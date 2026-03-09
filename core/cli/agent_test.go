package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/schema"
)

func TestParseParams(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected map[string]string
	}{
		{
			name:     "empty",
			input:    []string{},
			expected: map[string]string{},
		},
		{
			name:     "single param",
			input:    []string{"key=value"},
			expected: map[string]string{"key": "value"},
		},
		{
			name:     "multiple params",
			input:    []string{"a=1", "b=2", "c=hello world"},
			expected: map[string]string{"a": "1", "b": "2", "c": "hello world"},
		},
		{
			name:     "value with equals sign",
			input:    []string{"expr=a=b"},
			expected: map[string]string{"expr": "a=b"},
		},
		{
			name:     "empty value",
			input:    []string{"key="},
			expected: map[string]string{"key": ""},
		},
		{
			name:     "no equals sign is ignored",
			input:    []string{"noequals"},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseParams(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d params, got %d", len(tt.expected), len(result))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("param %q: expected %q, got %q", k, v, result[k])
				}
			}
		})
	}
}

func TestBuildPromptFromTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		params   map[string]string
		expected string
		wantErr  bool
	}{
		{
			name:     "no params",
			template: "Hello, world!",
			params:   map[string]string{},
			expected: "Hello, world!",
		},
		{
			name:     "with params",
			template: "Summarize {{.topic}} in {{.language}}",
			params:   map[string]string{"topic": "quantum physics", "language": "English"},
			expected: "Summarize quantum physics in English",
		},
		{
			name:     "invalid template",
			template: "{{.missing",
			params:   map[string]string{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildPromptFromTemplate(tt.template, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is longer than ten", 10, "this is lo..."},
		{"", 5, ""},
	}

	for _, tt := range tests {
		result := truncate(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestAgentRunCMD_Validation(t *testing.T) {
	cmd := &AgentRunCMD{}

	// Neither task name nor config
	err := cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error when neither task name nor config is provided")
	}

	// Both task name and config
	cmd.TaskName = "test"
	cmd.Config = "/some/config.json"
	err = cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error when both task name and config are provided")
	}
}

func TestAgentRunCMD_ConfigFileParsing(t *testing.T) {
	tmpDir := t.TempDir()

	// Valid config
	validConfig := AgentRunConfig{
		Name:       "test-task",
		Model:      "test-model",
		Prompt:     "Hello {{.name}}",
		Parameters: map[string]string{"name": "World"},
	}
	data, _ := json.MarshalIndent(validConfig, "", "  ")
	configPath := filepath.Join(tmpDir, "config.json")
	os.WriteFile(configPath, data, 0644)

	// Test that config is parsed correctly
	rawData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	var parsed AgentRunConfig
	if err := json.Unmarshal(rawData, &parsed); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}
	if parsed.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", parsed.Model)
	}
	if parsed.Prompt != "Hello {{.name}}" {
		t.Errorf("expected prompt 'Hello {{.name}}', got %q", parsed.Prompt)
	}
	if parsed.Parameters["name"] != "World" {
		t.Errorf("expected parameter name='World', got %q", parsed.Parameters["name"])
	}

	// Missing model
	invalidConfig := AgentRunConfig{Prompt: "test"}
	data, _ = json.MarshalIndent(invalidConfig, "", "  ")
	invalidPath := filepath.Join(tmpDir, "invalid.json")
	os.WriteFile(invalidPath, data, 0644)

	cmd := &AgentRunCMD{Config: invalidPath, ModelsPath: tmpDir}
	err = cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error for config without model")
	}

	// Missing prompt
	noPromptConfig := AgentRunConfig{Model: "test-model"}
	data, _ = json.MarshalIndent(noPromptConfig, "", "  ")
	noPromptPath := filepath.Join(tmpDir, "noprompt.json")
	os.WriteFile(noPromptPath, data, 0644)

	cmd = &AgentRunCMD{Config: noPromptPath, ModelsPath: tmpDir}
	err = cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error for config without prompt")
	}
}

func TestAgentRunCMD_RegistryLookup(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a task registry
	tasksFile := schema.TasksFile{
		Tasks: []schema.Task{
			{
				ID:      "task-123",
				Name:    "my-task",
				Model:   "test-model",
				Prompt:  "Do something with {{.input}}",
				Enabled: true,
			},
			{
				ID:      "task-456",
				Name:    "disabled-task",
				Model:   "test-model",
				Prompt:  "This is disabled",
				Enabled: false,
			},
		},
	}
	data, _ := json.MarshalIndent(tasksFile, "", "  ")
	os.WriteFile(filepath.Join(tmpDir, "agent_tasks.json"), data, 0644)

	// Task not found
	cmd := &AgentRunCMD{TaskName: "nonexistent", TasksDir: tmpDir, ModelsPath: tmpDir}
	err := cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}

	// Disabled task
	cmd = &AgentRunCMD{TaskName: "disabled-task", TasksDir: tmpDir, ModelsPath: tmpDir}
	err = cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error for disabled task")
	}

	// Valid task by name (will fail at model loading, which is expected)
	cmd = &AgentRunCMD{TaskName: "my-task", TasksDir: tmpDir, ModelsPath: tmpDir}
	err = cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error (no model config), but got nil")
	}
	// Should get past the registry lookup phase
	if err.Error() == "task \"my-task\" not found in registry" {
		t.Fatal("should have found the task in registry")
	}

	// Valid task by ID
	cmd = &AgentRunCMD{TaskName: "task-123", TasksDir: tmpDir, ModelsPath: tmpDir}
	err = cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error (no model config), but got nil")
	}
	if err.Error() == "task \"task-123\" not found in registry" {
		t.Fatal("should have found the task by ID in registry")
	}
}

func TestAgentRunCMD_MissingRegistry(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := &AgentRunCMD{TaskName: "some-task", TasksDir: tmpDir, ModelsPath: tmpDir}
	err := cmd.Run(nil)
	if err == nil {
		t.Fatal("expected error for missing registry")
	}
}
