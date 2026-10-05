package agentpool

import (
	"strings"

	coreTypes "github.com/mudler/LocalAGI/core/types"
	"github.com/sashabaranov/go-openai"
)

// ChatHistoryMessage is one earlier turn of the web chat conversation the
// message belongs to, as the client shows it.
type ChatHistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Bounds on what a client may send as history: the most recent turns win.
const (
	maxChatHistoryMessages = 40
	maxChatHistoryChars    = 64000
)

// chatJobOptions builds the job for a web chat message: the earlier turns of
// the conversation the client is showing, followed by the new user message.
//
// The history comes from the client because the web UI keeps several
// conversations per agent (New Chat, switching, Clear). Keying a server-side
// history by agent would mix them; taking the active conversation's turns from
// the request keeps each conversation separate and makes Clear and New Chat
// start without history. Only user and assistant turns with text are kept,
// bounded to the most recent maxChatHistoryMessages and maxChatHistoryChars.
func chatJobOptions(history []ChatHistoryMessage, message string) []coreTypes.JobOption {
	turns := sanitizeChatHistory(history)
	if len(turns) == 0 {
		return []coreTypes.JobOption{coreTypes.WithText(message)}
	}
	return []coreTypes.JobOption{coreTypes.WithConversationHistory(turns), coreTypes.WithText(message)}
}

func sanitizeChatHistory(history []ChatHistoryMessage) []openai.ChatCompletionMessage {
	kept := make([]openai.ChatCompletionMessage, 0, len(history))
	for _, m := range history {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		kept = append(kept, openai.ChatCompletionMessage{Role: role, Content: m.Content})
	}
	if len(kept) > maxChatHistoryMessages {
		kept = kept[len(kept)-maxChatHistoryMessages:]
	}
	total := 0
	start := len(kept)
	for i := len(kept) - 1; i >= 0; i-- {
		total += len(kept[i].Content)
		if total > maxChatHistoryChars {
			break
		}
		start = i
	}
	return kept[start:]
}
