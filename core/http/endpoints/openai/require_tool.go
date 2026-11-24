package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
)

// toolRequirement carries a profile's require_tool through the model calls of
// one request. A nil requirement is disabled, so callers need no separate
// enabled check.
//
// Only the first call is forced. Every later call of the server-side tool loop
// goes back to the client's tool_choice: a "required" that stayed in place
// would make the model call a tool again after every tool result and never
// answer, until the loop's iteration limit ends the request.
type toolRequirement struct {
	clientChoice any
	// grammar and clientGrammar are set only on the path where LocalAI builds
	// the tool-call grammar itself (no tokenizer template). There "required"
	// is the grammar without the no-action function the model would otherwise
	// answer with directly.
	grammar       string
	clientGrammar string
}

// newToolRequirement returns nil unless the first call should be forced: the
// profile asks for it, server-side MCP tools were offered, the client left
// tool_choice open, and the request carries a new user turn. A request that
// ends with a tool or assistant message is the client returning the result of
// a tool it ran itself, or continuing an answer; forcing a call there could
// bounce tool calls between client and server.
func newToolRequirement(cfg *config.ModelConfig, input *schema.OpenAIRequest, serverTools, shouldUseFn bool) *toolRequirement {
	if !cfg.RequiresTool() || !serverTools || !shouldUseFn || cfg.FunctionToCall() != "" {
		return nil
	}
	switch choice := input.ToolsChoice.(type) {
	case nil:
	case string:
		if choice != "" && choice != "auto" {
			return nil
		}
	default:
		return nil
	}
	if n := len(input.Messages); n == 0 || input.Messages[n-1].Role != "user" {
		return nil
	}
	return &toolRequirement{clientChoice: input.ToolsChoice}
}

// apply sets the tool choice for the model call of the given iteration of the
// tool loop, 0 being the first call of the request.
func (r *toolRequirement) apply(input *schema.OpenAIRequest, cfg *config.ModelConfig, iteration int) {
	if r == nil {
		return
	}
	if iteration == 0 {
		input.ToolsChoice = "required"
		if r.grammar != "" {
			cfg.Grammar = r.grammar
		}
		return
	}
	input.ToolsChoice = r.clientChoice
	if r.grammar != "" {
		cfg.Grammar = r.clientGrammar
	}
}
