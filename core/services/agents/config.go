package agents

import "encoding/json"

// KB mode constants for AgentConfig.KBMode.
const (
	KBModeAutoSearch = "auto_search" // default: auto-search KB on each user message
	KBModeTools      = "tools"       // expose search_memory / add_memory as agent tools
	KBModeBoth       = "both"        // auto-search + tools
)

// Skills mode constants for AgentConfig.SkillsMode.
const (
	SkillsModePrompt = "prompt" // default: inject skill descriptions into system prompt
	SkillsModeTools  = "tools"  // expose skills as callable tools
	SkillsModeBoth   = "both"   // both prompt + tools
)

// Conversation storage mode constants for AgentConfig.ConversationStorageMode.
const (
	ConvStorageUserOnly         = "user_only"          // default: store only user messages
	ConvStorageUserAndAssistant = "user_and_assistant"  // store user and assistant messages separately
	ConvStorageWholeConversation = "whole_conversation" // store entire conversation as single block
)

// AgentConfig defines the configuration for an agent.
// JSON field names match LocalAGI's state.AgentConfig for import/export compatibility.
type AgentConfig struct {
	// Integrations
	Connector      []ConnectorConfig      `json:"connectors"`
	Actions        []ActionsConfig        `json:"actions"`
	DynamicPrompts []DynamicPromptsConfig `json:"dynamic_prompts"`
	MCPServers     []MCPServer            `json:"mcp_servers"`
	MCPSTDIOServers MCPSTDIOServers       `json:"mcp_stdio_servers"`
	MCPPrepareScript string               `json:"mcp_prepare_script"`
	Filters        []FiltersConfig        `json:"filters"`

	// Basic info
	Description string `json:"description"`
	Name        string `json:"name"`

	// Models
	Model                 string `json:"model"`
	MultimodalModel       string `json:"multimodal_model"`
	TranscriptionModel    string `json:"transcription_model"`
	TranscriptionLanguage string `json:"transcription_language"`
	TTSModel              string `json:"tts_model"`

	// API
	APIURL         string `json:"api_url"`
	APIKey         string `json:"api_key"`
	LocalRAGURL    string `json:"local_rag_url"`
	LocalRAGAPIKey string `json:"local_rag_api_key"`

	// Timing
	LastMessageDuration   string `json:"last_message_duration"`
	PeriodicRuns          string `json:"periodic_runs"`
	SchedulerPollInterval string `json:"scheduler_poll_interval"`

	// Behavior
	StandaloneJob              bool   `json:"standalone_job"`
	InitiateConversations      bool   `json:"initiate_conversations"`
	CanPlan                    bool   `json:"enable_planning"`
	PlanReviewerModel          string `json:"plan_reviewer_model"`
	DisableSinkState           bool   `json:"disable_sink_state"` // legacy, kept for JSON compat — sink state is always disabled
	PermanentGoal              string `json:"permanent_goal"`
	SystemPrompt               string `json:"system_prompt"`
	SkillsPrompt               string `json:"skills_prompt"`
	InnerMonologueTemplate     string `json:"inner_monologue_template"`
	SchedulerTaskTemplate      string `json:"scheduler_task_template"`

	// Knowledge base
	EnableKnowledgeBase   bool   `json:"enable_kb"`
	KBMode                string `json:"kb_mode,omitempty"` // "auto_search" (default), "tools", "both"
	KnowledgeBaseResults  int    `json:"kb_results"`
	EnableKBCompaction    bool   `json:"enable_kb_compaction"`
	KBCompactionInterval  string `json:"kb_compaction_interval"`
	KBCompactionSummarize bool   `json:"kb_compaction_summarize"`
	KBAutoSearch          bool   `json:"kb_auto_search"` // legacy, kept for JSON compat
	KBAsTools             bool   `json:"kb_as_tools"`    // legacy, kept for JSON compat

	// Reasoning & tools
	EnableReasoning            bool `json:"enable_reasoning"`              // legacy, kept for JSON compat
	EnableForceReasoningTool   bool `json:"enable_reasoning_tool"`         // legacy, kept for JSON compat
	EnableReasoningForInstruct bool `json:"enable_reasoning_for_instruct"` // enables ForceReasoning + ForceReasoningTool
	EnableGuidedTools          bool `json:"enable_guided_tools"`
	CanStopItself              bool `json:"can_stop_itself"`

	// Skills
	EnableSkills   bool     `json:"enable_skills"`
	SkillsMode     string   `json:"skills_mode,omitempty"`     // "prompt" (default), "tools", or "both"
	SelectedSkills []string `json:"selected_skills,omitempty"` // Per-agent skill selection

	// Memory
	LongTermMemory        bool   `json:"long_term_memory"`
	SummaryLongTermMemory bool   `json:"summary_long_term_memory"`
	ConversationStorageMode string `json:"conversation_storage_mode"`

	// Execution
	ParallelJobs               int   `json:"parallel_jobs"`
	CancelPreviousOnNewMessage *bool `json:"cancel_previous_on_new_message"`
	StripThinkingTags          bool  `json:"strip_thinking_tags"`
	EnableEvaluation           bool  `json:"enable_evaluation"`
	MaxEvaluationLoops         int   `json:"max_evaluation_loops"`
	MaxAttempts             int  `json:"max_attempts"`
	MaxIterations           int  `json:"max_iterations"` // max tool loop iterations
	LoopDetection           int  `json:"loop_detection"`
	EnableAutoCompaction    bool `json:"enable_auto_compaction"`
	AutoCompactionThreshold int  `json:"auto_compaction_threshold"`
}

// ConnectorConfig defines a connector integration (Slack, Discord, etc.).
type ConnectorConfig struct {
	Type   string `json:"type"`
	Config string `json:"config"`
}

// ActionsConfig defines an action (tool) available to the agent.
type ActionsConfig struct {
	Type   string `json:"type"`
	Config string `json:"config"`
}

// DynamicPromptsConfig defines a dynamic prompt template.
type DynamicPromptsConfig struct {
	Type   string `json:"type"`
	Config string `json:"config"`
}

// FiltersConfig defines a job filter/trigger.
type FiltersConfig struct {
	Type   string `json:"type"`
	Config string `json:"config"`
}

// MCPServer defines an HTTP-based MCP server endpoint.
type MCPServer struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// MCPSTDIOServers is a list of stdio MCP servers that can be unmarshaled from
// either a JSON array of MCPSTDIOServer objects or a JSON string in Claude Desktop
// format ({"mcpServers": {"name": {"command": "...", "args": [...]}}}).
type MCPSTDIOServers []MCPSTDIOServer

// UnmarshalJSON handles both array and string (Claude Desktop JSON) formats.
func (m *MCPSTDIOServers) UnmarshalJSON(data []byte) error {
	// Try array format first
	var arr []MCPSTDIOServer
	if err := json.Unmarshal(data, &arr); err == nil {
		*m = arr
		return nil
	}

	// Try string format (Claude Desktop JSON blob)
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		parsed, err := parseClaudeDesktopMCP(raw)
		if err != nil {
			return nil // silently ignore unparseable strings
		}
		*m = parsed
		return nil
	}

	return nil
}

// parseClaudeDesktopMCP parses the {"mcpServers": {...}} format into MCPSTDIOServer list.
func parseClaudeDesktopMCP(raw string) ([]MCPSTDIOServer, error) {
	var wrapper struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Cmd     string            `json:"cmd"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return nil, err
	}
	var result []MCPSTDIOServer
	for name, srv := range wrapper.MCPServers {
		cmd := srv.Command
		if cmd == "" {
			cmd = srv.Cmd
		}
		var envList []string
		for k, v := range srv.Env {
			envList = append(envList, k+"="+v)
		}
		result = append(result, MCPSTDIOServer{
			Name: name,
			Cmd:  cmd,
			Args: srv.Args,
			Env:  envList,
		})
	}
	return result, nil
}

// MCPSTDIOServer defines a local command-based MCP server.
type MCPSTDIOServer struct {
	Name string   `json:"name,omitempty"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Cmd  string   `json:"cmd"`
}

// AgentKey returns the namespaced key for an agent: "{userID}:{name}" or just "{name}" if no userID.
func AgentKey(userID, name string) string {
	if userID == "" {
		return name
	}
	return userID + ":" + name
}
