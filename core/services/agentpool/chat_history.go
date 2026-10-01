package agentpool

import (
	"strings"

	"github.com/mudler/LocalAGI/core/conversations"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	"github.com/sashabaranov/go-openai"
)

// webChatConversation is the tracker key for the agent's web chat. Agents are
// already scoped per user by the pool key, so one key per agent is enough.
const webChatConversation = "webchat"

// chatJobOptions builds the job for a web chat message: the earlier turns kept
// by the agent's conversation tracker, followed by the new user message, which
// is recorded in the tracker as well. The tracker drops a conversation after the
// agent's last_message_duration of inactivity, like it does for connectors.
func chatJobOptions(tracker *conversations.ConversationTracker[string], message string) []coreTypes.JobOption {
	if tracker == nil {
		return []coreTypes.JobOption{coreTypes.WithText(message)}
	}
	history := tracker.GetConversation(webChatConversation)
	tracker.AddMessage(webChatConversation, openai.ChatCompletionMessage{Role: "user", Content: message})
	return []coreTypes.JobOption{coreTypes.WithConversationHistory(history), coreTypes.WithText(message)}
}

// rememberChatReply records a successful answer so the next chat message can
// refer to it. Failed, cancelled or empty runs are not recorded.
func rememberChatReply(tracker *conversations.ConversationTracker[string], response *coreTypes.JobResult) {
	if tracker == nil || response == nil || response.Error != nil || strings.TrimSpace(response.Response) == "" {
		return
	}
	tracker.AddMessage(webChatConversation, openai.ChatCompletionMessage{Role: "assistant", Content: response.Response})
}
