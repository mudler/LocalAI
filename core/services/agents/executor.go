package agents

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/xlog"
)

// Callbacks defines the event hooks for agent execution.
// The same executor uses different callback implementations
// for local (SSE manager) vs distributed (NATS EventBridge) modes.
type Callbacks struct {
	// OnStream receives raw streaming events from cogito (reasoning tokens, content tokens, tool calls).
	OnStream func(cogito.StreamEvent)
	// OnReasoning receives the complete parsed reasoning text after the LLM call.
	OnReasoning func(text string)
	// OnToolCall receives tool selection decisions (tool name + arguments).
	OnToolCall func(name, args string)
	// OnToolResult receives tool execution results.
	OnToolResult func(name, result string)
	// OnStatus receives status updates ("processing", "completed", "error: ...").
	OnStatus func(status string)
	// OnMessage receives final messages (sender="user"/"agent", content, messageID).
	OnMessage func(sender, content, messageID string)
}

// DefaultInnerMonologueTemplate is the prompt used for autonomous/background runs
// when the agent evaluates what to do next based on its permanent goal.
const DefaultInnerMonologueTemplate = `You are an autonomous agent. Your permanent goal is: {{.Goal}}

Evaluate the current situation and decide what action to take next.
Consider what you have done so far and what remains to be done.
If there is nothing to do, respond with a brief status update.`

// ExecuteChatOpts holds optional dependencies for agent execution.
type ExecuteChatOpts struct {
	SkillProvider SkillProvider // optional: provides skills for injection
	APIURL        string        // resolved API URL for KB and memory operations
	APIKey        string        // resolved API key for KB and memory operations
	UserID        string        // owner user ID — passed in collection API calls so per-user scoping works in distributed mode
	MessageID     string        // original message ID from the dispatch — used to correlate SSE responses with the originating request
}

// ExecuteBackgroundRun runs an autonomous/background agent execution.
// This is the equivalent of LocalAGI's periodicallyRun — the agent evaluates
// its permanent goal using the inner monologue template and decides what to do.
func ExecuteBackgroundRun(ctx context.Context, apiURL, apiKey string, cfg *AgentConfig, cb Callbacks, opts ...ExecuteChatOpts) (string, error) {
	// Build the inner monologue message
	monologue := cmp.Or(cfg.InnerMonologueTemplate, DefaultInnerMonologueTemplate)

	// Simple template substitution for {{.Goal}}
	message := strings.ReplaceAll(monologue, "{{.Goal}}", cfg.PermanentGoal)

	return ExecuteChat(ctx, apiURL, apiKey, cfg, message, cb, opts...)
}

// ExecuteBackgroundRunWithLLM is like ExecuteBackgroundRun but accepts a pre-built LLM client.
// This enables testing background agent execution with mock LLMs.
func ExecuteBackgroundRunWithLLM(ctx context.Context, llm cogito.LLM, cfg *AgentConfig, cb Callbacks, opts ...ExecuteChatOpts) (string, error) {
	monologue := cmp.Or(cfg.InnerMonologueTemplate, DefaultInnerMonologueTemplate)
	message := strings.ReplaceAll(monologue, "{{.Goal}}", cfg.PermanentGoal)
	return ExecuteChatWithLLM(ctx, llm, cfg, message, cb, opts...)
}

// ExecuteChat runs a single agent chat interaction using cogito.
// It is stateless — config and conversation come from the caller,
// results are delivered via callbacks.

func ExecuteChat(ctx context.Context, apiURL, apiKey string, cfg *AgentConfig, message string, cb Callbacks, opts ...ExecuteChatOpts) (string, error) {
	if cfg.Model == "" {
		return "", fmt.Errorf("agent has no model configured")
	}

	// Resolve API endpoint and key.
	// Agent config overrides if set (user may point to external OpenAI, etc.)
	// Otherwise fall back to worker defaults.
	effectiveURL := apiURL
	if cfg.APIURL != "" {
		effectiveURL = cfg.APIURL
	}
	effectiveKey := apiKey
	if cfg.APIKey != "" {
		effectiveKey = cfg.APIKey
	}
	endpoint := effectiveURL + "/v1"
	llm := clients.NewLocalAILLM(cfg.Model, effectiveKey, endpoint)

	var o ExecuteChatOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	o.APIURL = effectiveURL
	o.APIKey = effectiveKey
	return ExecuteChatWithLLM(ctx, llm, cfg, message, cb, o)
}

// ExecuteChatWithLLM is like ExecuteChat but accepts a pre-built LLM client.
// This enables testing with mock LLMs.
func ExecuteChatWithLLM(ctx context.Context, llm cogito.LLM, cfg *AgentConfig, message string, cb Callbacks, opts ...ExecuteChatOpts) (string, error) {
	var skillProvider SkillProvider
	var effectiveURL, effectiveKey, userID string
	if len(opts) > 0 {
		skillProvider = opts[0].SkillProvider
		effectiveURL = opts[0].APIURL
		effectiveKey = opts[0].APIKey
		userID = opts[0].UserID
	}
	// Notify processing
	if cb.OnStatus != nil {
		cb.OnStatus("processing")
	}

	// Build conversation fragment
	fragment := cogito.NewEmptyFragment()

	// System prompt
	systemPrompt := cmp.Or(cfg.SystemPrompt, "You are a helpful assistant.")

	// Inject skills (if enabled)
	if cfg.EnableSkills && skillProvider != nil {
		skillsMode := cmp.Or(cfg.SkillsMode, SkillsModePrompt)

		allSkills, err := skillProvider.ListSkills()
		if err == nil && len(allSkills) > 0 {
			filtered := FilterSkills(allSkills, cfg.SelectedSkills)

			// Prompt mode: inject full skill content into system prompt
			if skillsMode == SkillsModePrompt || skillsMode == SkillsModeBoth {
				if skillsText := RenderSkillsPrompt(filtered, cfg.SkillsPrompt); skillsText != "" {
					systemPrompt += "\n\n" + skillsText
				}
			}

			// Tools mode: add a hint so the agent knows to use request_skill
			if skillsMode == SkillsModeTools {
				systemPrompt += "\n\n" + SkillsToolsHint
			}

			// Tools mode: skills will be added as cogito tools after fragment is built
			// (handled below with cogitoOpts)
			_ = filtered // used below
		}
	}

	fragment = fragment.AddMessage(cogito.SystemMessageRole, systemPrompt)

	// KB auto-search: query the knowledge base with the user's message
	// and prepend results to the conversation as context.
	// Resolve KB mode: prefer KBMode field, fall back to legacy booleans.
	kbMode := cfg.KBMode
	if kbMode == "" {
		// Legacy fallback
		switch {
		case cfg.KBAutoSearch && cfg.KBAsTools:
			kbMode = KBModeBoth
		case cfg.KBAsTools:
			kbMode = KBModeTools
		default:
			kbMode = KBModeAutoSearch
		}
	}

	if cfg.EnableKnowledgeBase && (kbMode == KBModeAutoSearch || kbMode == KBModeBoth) {
		kbResults := KBAutoSearchPrompt(ctx, effectiveURL, effectiveKey, cfg.Name, message, cfg.KnowledgeBaseResults, userID)
		if kbResults != "" {
			fragment = fragment.AddMessage(cogito.SystemMessageRole, kbResults)
		}
	}

	// User message
	fragment = fragment.AddMessage(cogito.UserMessageRole, message)

	// Build cogito options
	var cogitoOpts []cogito.Option

	// MCP sessions
	sessions, cleanup, err := setupMCPSessions(ctx, cfg)
	if err != nil {
		xlog.Warn("Failed to set up MCP sessions", "error", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(sessions) > 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithMCPs(sessions...))
	}

	// KB tools (search_memory / add_memory) — when kb mode is "tools" or "both"
	if cfg.EnableKnowledgeBase && (kbMode == KBModeTools || kbMode == KBModeBoth) {
		kbResults := cfg.KnowledgeBaseResults
		if kbResults <= 0 {
			kbResults = 5
		}
		cogitoOpts = append(cogitoOpts, cogito.WithTools(
			cogito.NewToolDefinition(
				KBSearchMemoryTool{APIURL: effectiveURL, APIKey: effectiveKey, Collection: cfg.Name, MaxResults: kbResults, UserID: userID},
				KBSearchMemoryArgs{},
				"search_memory",
				"Search the knowledge base for relevant information",
			),
			cogito.NewToolDefinition(
				KBAddMemoryTool{APIURL: effectiveURL, APIKey: effectiveKey, Collection: cfg.Name, UserID: userID},
				KBAddMemoryArgs{},
				"add_memory",
				"Store content in memory for later retrieval",
			),
		))
	}

	// Skill tools — when skills_mode is "tools" or "both"
	if cfg.EnableSkills && skillProvider != nil {
		skillsMode := cmp.Or(cfg.SkillsMode, SkillsModePrompt)
		if skillsMode == SkillsModeTools || skillsMode == SkillsModeBoth {
			allSkills, _ := skillProvider.ListSkills()
			filtered := FilterSkills(allSkills, cfg.SelectedSkills)
			if len(filtered) > 0 {
				cogitoOpts = append(cogitoOpts, cogito.WithTools(
					cogito.NewToolDefinition(
						RequestSkillTool{Skills: filtered},
						RequestSkillArgs{},
						"request_skill",
						"Request a skill by name. Available skills: "+skillNames(filtered),
					),
				))
			}
		}
	}

	// Sink state is always disabled — the agent responds directly when no tools match.
	cogitoOpts = append(cogitoOpts, cogito.DisableSinkState)

	// Stream callback
	if cb.OnStream != nil {
		cogitoOpts = append(cogitoOpts, cogito.WithStreamCallback(cb.OnStream))
	}

	// Reasoning callback
	if cb.OnReasoning != nil {
		cogitoOpts = append(cogitoOpts, cogito.WithReasoningCallback(func(s string) {
			if s != "" {
				cb.OnReasoning(s)
			}
		}))
	}

	// Tool call result callback
	if cb.OnToolResult != nil || cb.OnToolCall != nil {
		cogitoOpts = append(cogitoOpts, cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			if isInternalCogitoTool(t.Name) {
				return
			}
			if cb.OnToolCall != nil {
				argsJSON, _ := json.Marshal(t.ToolArguments.Arguments)
				cb.OnToolCall(t.Name, string(argsJSON))
			}
			if cb.OnToolResult != nil && t.Result != "" {
				cb.OnToolResult(t.Name, t.Result)
			}
		}))
	}

	// Tool call decision callback — forward tool selection as stream event
	if cb.OnStream != nil {
		cogitoOpts = append(cogitoOpts, cogito.WithToolCallBack(
			func(tc *cogito.ToolChoice, _ *cogito.SessionState) cogito.ToolCallDecision {
				if !isInternalCogitoTool(tc.Name) {
					argsJSON, _ := json.Marshal(tc.Arguments)
					cb.OnStream(cogito.StreamEvent{
						Type:     cogito.StreamEventToolCall,
						ToolName: tc.Name,
						ToolArgs: string(argsJSON),
					})
				}
				return cogito.ToolCallDecision{Approved: true}
			},
		))
	}

	// Iteration limits
	if cfg.MaxAttempts > 1 {
		cogitoOpts = append(cogitoOpts, cogito.WithMaxAttempts(cfg.MaxAttempts))
		cogitoOpts = append(cogitoOpts, cogito.WithMaxRetries(cfg.MaxAttempts))
	}

	// Auto compaction
	if cfg.EnableAutoCompaction {
		cogitoOpts = append(cogitoOpts, cogito.WithCompactionThreshold(cfg.AutoCompactionThreshold))
	}

	// Loop detection
	if cfg.LoopDetection > 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithLoopDetection(cfg.LoopDetection))
	}

	// Reasoning for instruct models (enables both ForceReasoning + ForceReasoningTool)
	if cfg.EnableReasoningForInstruct {
		cogitoOpts = append(cogitoOpts, cogito.WithForceReasoning())
		cogitoOpts = append(cogitoOpts, cogito.WithForceReasoningTool())
	}

	// Guided tools
	if cfg.EnableGuidedTools {
		cogitoOpts = append(cogitoOpts, cogito.EnableGuidedTools)
	}

	// Max iterations (tool loop iterations)
	if cfg.MaxIterations > 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithIterations(cfg.MaxIterations))
	}

	// Context propagation (enables cancellation of cogito execution)
	cogitoOpts = append(cogitoOpts, cogito.WithContext(ctx))

	// Execute
	xlog.Info("Executing agent chat", "agent", cfg.Name, "model", cfg.Model)
	result, err := cogito.ExecuteTools(llm, fragment, cogitoOpts...)
	if err != nil {
		if cb.OnStatus != nil {
			cb.OnStatus("error: " + err.Error())
		}
		return "", fmt.Errorf("agent execution failed: %w", err)
	}

	// Extract response
	response := ""
	if len(result.Messages) > 0 {
		last := result.Messages[len(result.Messages)-1]
		response = last.Content
	}

	// Strip thinking tags if configured
	if cfg.StripThinkingTags && response != "" {
		response = stripThinkingTags(response)
	}

	// Save conversation to KB when long-term memory is enabled.
	// Use a detached context: the parent ctx may be cancelled (e.g. in distributed
	// mode handleJob defers cancel()) before this goroutine completes.
	if cfg.LongTermMemory && effectiveURL != "" {
		go saveConversationToKB(context.Background(), llm, effectiveURL, effectiveKey, cfg, message, response, userID)
	}

	// Publish agent response — use original messageID from the dispatch so the
	// frontend can correlate this response with the conversation that sent it.
	var respMsgID string
	if len(opts) > 0 && opts[0].MessageID != "" {
		respMsgID = opts[0].MessageID + "-agent"
	} else {
		respMsgID = fmt.Sprintf("%d-agent", time.Now().UnixNano())
	}
	if cb.OnMessage != nil {
		cb.OnMessage("agent", response, respMsgID)
	}

	// Mark completed
	if cb.OnStatus != nil {
		cb.OnStatus("completed")
	}

	xlog.Info("Agent chat completed", "agent", cfg.Name, "responseLen", len(response))
	return response, nil
}

// isInternalCogitoTool returns true for tool names used internally by cogito's
// force-reasoning pipeline (reasoning, pick_tool, pick_tools, reply).
// These should be filtered from user-facing stream events.
func isInternalCogitoTool(name string) bool {
	switch name {
	case "reasoning", "pick_tool", "pick_tools", "reply":
		return true
	}
	return false
}

// stripThinkingTags removes <thinking>...</thinking> blocks from content.
func stripThinkingTags(content string) string {
	// Simple implementation — remove content between <thinking> and </thinking>
	result := content
	for {
		start := strings.Index(result, "<thinking>")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], "</thinking>")
		if end == -1 {
			break
		}
		result = result[:start] + result[start+end+len("</thinking>"):]
	}
	return result
}
