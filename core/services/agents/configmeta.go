package agents

// ConfigMeta provides field definitions for the agent create/edit UI form.
// This replaces LocalAGI's state.AgentConfigMeta with a static definition
// that doesn't depend on LocalAGI imports.

// FieldType constants for form field types.
const (
	FieldText     = "text"
	FieldTextarea = "textarea"
	FieldCheckbox = "checkbox"
	FieldNumber   = "number"
	FieldSelect   = "select"
	FieldPassword = "password"
)

// ConfigField describes a single form field.
type ConfigField struct {
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Label        string              `json:"label"`
	DefaultValue any                 `json:"defaultValue"`
	Placeholder  string              `json:"placeholder,omitempty"`
	HelpText     string              `json:"helpText,omitempty"`
	Required     bool                `json:"required,omitempty"`
	Options      []ConfigFieldOption `json:"options,omitempty"`
	Min          float32             `json:"min,omitempty"`
	Max          float32             `json:"max,omitempty"`
	Step         float32             `json:"step,omitempty"`
	Tags         ConfigFieldTags     `json:"tags,omitempty"`
}

// ConfigFieldOption is a select field option.
type ConfigFieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ConfigFieldTags groups a field into a UI section.
type ConfigFieldTags struct {
	Section   string `json:"section,omitempty"`
	DependsOn string `json:"depends_on,omitempty"` // field name: only show when this field is truthy
}

// ConfigFieldGroup groups related fields (e.g., connector fields).
type ConfigFieldGroup struct {
	Name   string        `json:"name"`
	Label  string        `json:"label"`
	Fields []ConfigField `json:"fields"`
}

// AgentConfigMeta holds all field definitions for the UI.
type AgentConfigMeta struct {
	Fields     []ConfigField      `json:"Fields"`
	Actions    []ConfigFieldGroup `json:"Actions"`
	Connectors []ConfigFieldGroup `json:"Connectors"`
	Filters    []ConfigFieldGroup `json:"Filters"`
}

// DefaultConfigMeta returns the static config metadata for the agent UI form.
// Field names and sections match LocalAGI's state.NewAgentConfigMeta() for compatibility.
func DefaultConfigMeta() AgentConfigMeta {
	return AgentConfigMeta{
		Fields:     defaultFields(),
		Actions:    []ConfigFieldGroup{},
		Connectors: []ConfigFieldGroup{},
		Filters:    []ConfigFieldGroup{},
	}
}

func defaultFields() []ConfigField {
	return []ConfigField{
		// BasicInfo
		{Name: "name", Label: "Name", Type: FieldText, Required: true, Tags: ConfigFieldTags{Section: "BasicInfo"}},
		{Name: "description", Label: "Description", Type: FieldTextarea, Tags: ConfigFieldTags{Section: "BasicInfo"}},

		// ModelSettings
		{Name: "model", Label: "Model", Type: FieldText, Required: true, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "multimodal_model", Label: "Multimodal Model", Type: FieldText, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "transcription_model", Label: "Transcription Model", Type: FieldText, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "transcription_language", Label: "Transcription Language", Type: FieldText, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "tts_model", Label: "TTS Model", Type: FieldText, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "plan_reviewer_model", Label: "Plan Reviewer Model", Type: FieldText, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "api_url", Label: "API URL", Type: FieldText, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "api_key", Label: "API Key", Type: FieldPassword, Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "strip_thinking_tags", Label: "Strip Thinking Tags", Type: FieldCheckbox, DefaultValue: false, HelpText: "Remove <thinking> tags from responses", Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "enable_auto_compaction", Label: "Enable Auto Compaction", Type: FieldCheckbox, DefaultValue: false, HelpText: "Auto-compact conversation when token threshold is reached", Tags: ConfigFieldTags{Section: "ModelSettings"}},
		{Name: "auto_compaction_threshold", Label: "Auto Compaction Threshold", Type: FieldNumber, DefaultValue: 4096, Min: 100, Step: 100, HelpText: "Token count that triggers auto-compaction", Tags: ConfigFieldTags{Section: "ModelSettings"}},

		// MemorySettings
		{Name: "enable_kb", Label: "Enable Knowledge Base", Type: FieldCheckbox, DefaultValue: false, Tags: ConfigFieldTags{Section: "MemorySettings"}},
		{Name: "kb_mode", Label: "Knowledge Base Mode", Type: FieldSelect, DefaultValue: "auto_search",
			Options: []ConfigFieldOption{
				{Value: "auto_search", Label: "Auto Search"},
				{Value: "tools", Label: "As Tools (search_memory / add_memory)"},
				{Value: "both", Label: "Both (Auto Search + Tools)"},
			},
			HelpText: "How the knowledge base is used: automatic search on each message, exposed as callable tools, or both",
			Tags:     ConfigFieldTags{Section: "MemorySettings", DependsOn: "enable_kb"},
		},
		{Name: "kb_results", Label: "Knowledge Base Results", Type: FieldNumber, DefaultValue: 5, Min: 1, Step: 1, Tags: ConfigFieldTags{Section: "MemorySettings", DependsOn: "enable_kb"}},
		{Name: "enable_kb_compaction", Label: "Enable KB Compaction", Type: FieldCheckbox, DefaultValue: false, HelpText: "Periodically compact KB entries by date", Tags: ConfigFieldTags{Section: "MemorySettings"}},
		{Name: "kb_compaction_interval", Label: "KB Compaction Interval", Type: FieldText, DefaultValue: "daily", Placeholder: "daily, weekly, monthly", Tags: ConfigFieldTags{Section: "MemorySettings"}},
		{Name: "kb_compaction_summarize", Label: "KB Compaction Summarize", Type: FieldCheckbox, DefaultValue: true, HelpText: "Summarize via LLM when compacting", Tags: ConfigFieldTags{Section: "MemorySettings"}},
		{Name: "long_term_memory", Label: "Long Term Memory", Type: FieldCheckbox, DefaultValue: false, Tags: ConfigFieldTags{Section: "MemorySettings"}},
		{Name: "summary_long_term_memory", Label: "Summary Long Term Memory", Type: FieldCheckbox, DefaultValue: false, Tags: ConfigFieldTags{Section: "MemorySettings"}},
		{Name: "conversation_storage_mode", Label: "Conversation Storage Mode", Type: FieldSelect, DefaultValue: ConvStorageUserOnly,
			Options: []ConfigFieldOption{
				{Value: ConvStorageUserOnly, Label: "User Messages Only"},
				{Value: ConvStorageUserAndAssistant, Label: "User and Assistant Messages"},
				{Value: ConvStorageWholeConversation, Label: "Whole Conversation as Block"},
			},
			HelpText: "Controls what gets stored in the knowledge base",
			Tags:     ConfigFieldTags{Section: "MemorySettings"},
		},

		// PromptsGoals
		{Name: "system_prompt", Label: "System Prompt", Type: FieldTextarea, HelpText: "Instructions that define the agent's behavior", Tags: ConfigFieldTags{Section: "PromptsGoals"}},
		{Name: "permanent_goal", Label: "Permanent Goal", Type: FieldTextarea, HelpText: "Long-term objective for the agent", Tags: ConfigFieldTags{Section: "PromptsGoals"}},
		{Name: "skills_prompt", Label: "Skills Prompt", Type: FieldTextarea, HelpText: "Optional template for skill injection. If empty, default XML format is used.", Tags: ConfigFieldTags{Section: "PromptsGoals"}},
		{Name: "inner_monologue_template", Label: "Inner Monologue Template", Type: FieldTextarea, HelpText: "Prompt for periodic autonomous runs", Tags: ConfigFieldTags{Section: "PromptsGoals"}},
		{Name: "scheduler_task_template", Label: "Scheduler Task Template", Type: FieldTextarea, HelpText: "Template for scheduled tasks. Use {{.Task}} to reference the task.", Tags: ConfigFieldTags{Section: "PromptsGoals"}},

		// AdvancedSettings
		{Name: "standalone_job", Label: "Standalone Job", Type: FieldCheckbox, DefaultValue: false, HelpText: "Run as background job without user interaction", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "initiate_conversations", Label: "Initiate Conversations", Type: FieldCheckbox, DefaultValue: false, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "enable_planning", Label: "Enable Planning", Type: FieldCheckbox, DefaultValue: false, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "cancel_previous_on_new_message", Label: "Cancel Previous on New Message", Type: FieldCheckbox, DefaultValue: true, HelpText: "Cancel running job when a new message arrives", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "loop_detection", Label: "Loop Detection", Type: FieldNumber, DefaultValue: 5, Min: 1, Step: 1, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "can_stop_itself", Label: "Can Stop Itself", Type: FieldCheckbox, DefaultValue: false, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "periodic_runs", Label: "Periodic Runs", Type: FieldText, Placeholder: "10m", HelpText: "Duration between periodic agent runs", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "scheduler_poll_interval", Label: "Scheduler Poll Interval", Type: FieldText, DefaultValue: "30s", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "enable_reasoning", Label: "Enable Reasoning", Type: FieldCheckbox, DefaultValue: false, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "enable_reasoning_tool", Label: "Enable Reasoning for Tools", Type: FieldCheckbox, DefaultValue: true, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "enable_reasoning_for_instruct", Label: "Enable Reasoning for Instruct Models", Type: FieldCheckbox, DefaultValue: false, HelpText: "Force structured reasoning before tool selection (recommended for instruct-tuned models)", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "enable_guided_tools", Label: "Enable Guided Tools", Type: FieldCheckbox, DefaultValue: false, HelpText: "Filter tools through guidance using descriptions", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "enable_skills", Label: "Enable Skills", Type: FieldCheckbox, DefaultValue: false, HelpText: "Inject skills into the agent", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "skills_mode", Label: "Skills Injection Mode", Type: FieldSelect, DefaultValue: "prompt",
			Options: []ConfigFieldOption{
				{Value: "prompt", Label: "Inject as System Prompt"},
				{Value: "tools", Label: "Inject as Tools"},
				{Value: "both", Label: "Both (Prompt + Tools)"},
			},
			HelpText: "How skills are made available to the agent: as system prompt context, as callable tools, or both",
			Tags:     ConfigFieldTags{Section: "AdvancedSettings"},
		},
		{Name: "parallel_jobs", Label: "Parallel Jobs", Type: FieldNumber, DefaultValue: 5, Min: 1, Step: 1, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "max_attempts", Label: "Max Attempts", Type: FieldNumber, DefaultValue: 2, Min: 1, Step: 1, Tags: ConfigFieldTags{Section: "AdvancedSettings"}},
		{Name: "max_iterations", Label: "Max Iterations", Type: FieldNumber, DefaultValue: 1, Min: 1, Step: 1, HelpText: "Maximum tool loop iterations per execution", Tags: ConfigFieldTags{Section: "AdvancedSettings"}},

		// MCP
		{Name: "mcp_stdio_servers", Label: "MCP STDIO Servers", Type: FieldTextarea, HelpText: "JSON array of MCP STDIO server configs", Tags: ConfigFieldTags{Section: "MCP"}},
		{Name: "mcp_prepare_script", Label: "MCP Prepare Script", Type: FieldTextarea, HelpText: "Script to run before MCP servers start", Tags: ConfigFieldTags{Section: "MCP"}},
	}
}
