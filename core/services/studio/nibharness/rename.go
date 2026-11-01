package nibharness

import (
	"context"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

// aliasLLM hides the aliases of wireName from the model. nib and the loop work
// with the alias (so nib's own tool of the same name stays off), while the model
// is offered, and answers with, the name the host gave the tool. Requests are
// renamed back to the host name on the way out and replies to the alias on the
// way in.
type aliasLLM struct {
	cogito.LLM
	toHost map[string]string // alias -> host name
	toWire map[string]string // host name -> alias
}

// withAliases wraps llm when some host tool has an alias, and returns it as it
// is otherwise.
func withAliases(llm cogito.LLM, hostNames []string) cogito.LLM {
	a := &aliasLLM{LLM: llm, toHost: map[string]string{}, toWire: map[string]string{}}
	for _, n := range hostNames {
		if w := wireName(n); w != n {
			a.toHost[w], a.toWire[n] = n, w
		}
	}
	if len(a.toHost) == 0 || llm == nil {
		return llm
	}
	// Only a client that streams may look like one: the loop above prefers
	// streaming whenever the interface is there.
	if _, ok := llm.(cogito.StreamingLLM); ok {
		return &aliasStreamLLM{aliasLLM: a}
	}
	return a
}

func rename(name string, m map[string]string) string {
	if to, ok := m[name]; ok {
		return to
	}
	return name
}

// request renames the tools of a request to the host names. The slices are
// copied: the caller keeps its own.
func (a *aliasLLM) request(req openai.ChatCompletionRequest) openai.ChatCompletionRequest {
	if len(req.Tools) > 0 {
		tools := make([]openai.Tool, len(req.Tools))
		copy(tools, req.Tools)
		for i, t := range tools {
			if t.Function != nil {
				f := *t.Function
				f.Name = rename(f.Name, a.toHost)
				tools[i].Function = &f
			}
		}
		req.Tools = tools
	}
	if len(req.Messages) > 0 {
		msgs := make([]openai.ChatCompletionMessage, len(req.Messages))
		copy(msgs, req.Messages)
		for i, m := range msgs {
			if len(m.ToolCalls) == 0 {
				continue
			}
			calls := make([]openai.ToolCall, len(m.ToolCalls))
			copy(calls, m.ToolCalls)
			for j := range calls {
				calls[j].Function.Name = rename(calls[j].Function.Name, a.toHost)
			}
			msgs[i].ToolCalls = calls
		}
		req.Messages = msgs
	}
	if tc, ok := req.ToolChoice.(openai.ToolChoice); ok && tc.Function.Name != "" {
		tc.Function.Name = rename(tc.Function.Name, a.toHost)
		req.ToolChoice = tc
	}
	return req
}

func (a *aliasLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	reply, usage, err := a.LLM.CreateChatCompletion(ctx, a.request(req))
	if err != nil {
		return reply, usage, err
	}
	choices := make([]openai.ChatCompletionChoice, len(reply.ChatCompletionResponse.Choices))
	copy(choices, reply.ChatCompletionResponse.Choices)
	for i, c := range choices {
		if len(c.Message.ToolCalls) == 0 {
			continue
		}
		calls := make([]openai.ToolCall, len(c.Message.ToolCalls))
		copy(calls, c.Message.ToolCalls)
		for j := range calls {
			calls[j].Function.Name = rename(calls[j].Function.Name, a.toWire)
		}
		choices[i].Message.ToolCalls = calls
	}
	reply.ChatCompletionResponse.Choices = choices
	return reply, usage, nil
}

// aliasStreamLLM is aliasLLM over a client that streams.
type aliasStreamLLM struct{ *aliasLLM }

// CreateChatCompletionStream is the streaming form.
func (a *aliasStreamLLM) CreateChatCompletionStream(ctx context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	in, err := a.LLM.(cogito.StreamingLLM).CreateChatCompletionStream(ctx, a.request(req))
	if err != nil {
		return nil, err
	}
	out := make(chan cogito.StreamEvent)
	go func() {
		defer close(out)
		for ev := range in {
			if ev.ToolName != "" {
				ev.ToolName = rename(ev.ToolName, a.toWire)
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				// Drain so the producer is not left blocked.
				for range in {
				}
				return
			}
		}
	}()
	return out, nil
}
