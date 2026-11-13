// Package harnessctx names the context windows the built-in chat agent needs.
// It has no dependencies, so any package can import it, the config package and
// the chat service among them.
package harnessctx

// Context windows the built-in chat agent (Studio chats) needs.
//
// Before the user types, a chat already sends a fixed block to the model: the
// system message, the always-on host tools, the LocalAI client group (or its
// small bridge when the tools are deferred) and the chat template. Measured
// with a Qwen3 tokenizer that block is about 7.4k tokens for a regular user
// and 8.7k (compact prompt, deferred tools) to 18.8k (not deferred) for an
// administrator chat. The chat loop refuses to send a request when fewer than
// 1024 tokens would be left for the reply, so a window under about 8.4k fails
// every turn, and compaction runs on every turn below roughly 16k.
//
// The numbers below are the one place that knows this. The Studio service
// reads them to tell a user when a model cannot run the chat, and the UI gets
// them from the chat API instead of keeping a copy.
const (
	// MinContext is the smallest window a regular user's chat runs well in.
	MinContext = 16384

	// RecommendedContext is the window a chat should have. It is also
	// the target of the automatic context size (see config.ModelConfig.AutoContextTarget).
	RecommendedContext = 32768

	// MinContextAdmin is the smallest window of a chat that has the
	// LocalAI management tools: their group is larger.
	MinContextAdmin = 24576
)

// For returns the minimum and the recommended window of a chat for
// an administrator or for a regular user.
func For(admin bool) (minimum, recommended int) {
	if admin {
		return MinContextAdmin, RecommendedContext
	}
	return MinContext, RecommendedContext
}
