package nibharness

import (
	"context"
	"regexp"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

// nib appends text about itself to every system prompt, whatever the prompt
// is: how to add MCP servers with nib's command line, and that the model is
// "an AI terminal assistant" that loads the "about-nib" skill. A chat of LocalAI
// has no such command or skill, so the model would try to use them. nib has no
// switch for this text, so it is cut from the request on its way to the model.
var nibOwnText = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\s*You can register additional MCP servers from the command line:.*?become available on the next \w+ session\.`),
	regexp.MustCompile(`(?s)\s*You are \w+, an AI terminal assistant\. When the user asks about .*? read the documentation first, then answer\.`),
}

// withoutNibText returns s without the text nib adds about itself.
func withoutNibText(s string) string {
	for _, re := range nibOwnText {
		s = re.ReplaceAllString(s, "")
	}
	return s
}

// hostPromptLLM cuts nib's own text from the system messages of each request.
type hostPromptLLM struct{ cogito.LLM }

func clean(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role == openai.ChatMessageRoleSystem {
			out[i].Content = withoutNibText(out[i].Content)
		}
	}
	return out
}

func (l hostPromptLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	f.Messages = clean(f.Messages)
	return l.LLM.Ask(ctx, f)
}

func (l hostPromptLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	req.Messages = clean(req.Messages)
	return l.LLM.CreateChatCompletion(ctx, req)
}

// hostPromptStreamingLLM is the same for a model that streams.
type hostPromptStreamingLLM struct {
	hostPromptLLM
	stream cogito.StreamingLLM
}

func (l hostPromptStreamingLLM) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	req.Messages = clean(req.Messages)
	return l.stream.CreateChatCompletionStream(ctx, req)
}

// forHost wraps llm so that its requests carry the host's prompt only.
func forHost(llm cogito.LLM) cogito.LLM {
	if s, ok := llm.(cogito.StreamingLLM); ok {
		return hostPromptStreamingLLM{hostPromptLLM: hostPromptLLM{llm}, stream: s}
	}
	return hostPromptLLM{llm}
}
