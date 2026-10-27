package nibharness

import (
	"context"
	"testing"

	"github.com/mudler/cogito"
	"github.com/sashabaranov/go-openai"
)

// plainFake is a client that does not stream.
type plainFake struct{ cogito.LLM }

// streamingFake sees the requests it gets and streams one tool call.
type streamingFake struct {
	cogito.LLM
	got []openai.ChatCompletionRequest
}

func (f *streamingFake) CreateChatCompletionStream(_ context.Context, req openai.ChatCompletionRequest) (<-chan cogito.StreamEvent, error) {
	f.got = append(f.got, req)
	ch := make(chan cogito.StreamEvent, 2)
	ch <- cogito.StreamEvent{Type: cogito.StreamEventToolCall, ToolName: "spawn_agent", ToolCallIndex: 0}
	ch <- cogito.StreamEvent{Type: cogito.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestTheStreamingFormRenamesBothWays(t *testing.T) {
	fake := &streamingFake{}
	llm := withAliases(fake, []string{"spawn_agent", "plain"})
	sl, ok := llm.(cogito.StreamingLLM)
	if !ok {
		t.Fatal("a client that streams must still stream once wrapped")
	}
	req := openai.ChatCompletionRequest{Tools: []openai.Tool{
		{Type: "function", Function: &openai.FunctionDefinition{Name: "studio_spawn_agent"}},
		{Type: "function", Function: &openai.FunctionDefinition{Name: "plain"}},
	}}
	ch, err := sl.CreateChatCompletionStream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for ev := range ch {
		if ev.ToolName != "" {
			names = append(names, ev.ToolName)
		}
	}
	if len(names) != 1 || names[0] != "studio_spawn_agent" {
		t.Fatalf("the loop saw the call as %v, want the alias", names)
	}
	if got := fake.got[0].Tools[0].Function.Name; got != "spawn_agent" {
		t.Fatalf("the model was offered %q, want the host name", got)
	}
	if req.Tools[0].Function.Name != "studio_spawn_agent" {
		t.Fatal("the caller's request was changed")
	}
}

func TestAClientThatDoesNotStreamDoesNotStartToLookLikeOne(t *testing.T) {
	llm := withAliases(&plainFake{}, []string{"spawn_agent"})
	if _, ok := llm.(cogito.StreamingLLM); ok {
		t.Fatal("the wrapper claims to stream")
	}
}

func TestNothingIsWrappedWithoutAnAlias(t *testing.T) {
	inner := &plainFake{}
	if got := withAliases(inner, []string{"plain", "other"}); got != cogito.LLM(inner) {
		t.Fatal("a model with no colliding tool must be used as it is")
	}
}
