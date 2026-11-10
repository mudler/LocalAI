package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestWithExecutionCallAddsTheHostOptionsAfterTheArguments(t *testing.T) {
	body := json.RawMessage(`{"tool":"bash","command":"ls"}`)
	ctx := contract.WithExecutionCall(context.Background(), contract.ExecutionCall{Profile: "media", Pin: true, MemoryMiB: 2048})
	var got map[string]json.RawMessage
	if err := json.Unmarshal(withExecutionCall(ctx, body), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["command"]) != `"ls"` || string(got["tool"]) != `"bash"` {
		t.Fatalf("the arguments changed: %s", got)
	}
	var c contract.ExecutionCall
	if err := json.Unmarshal(got[contract.ExecutionCallArgKey], &c); err != nil || c.Profile != "media" || !c.Pin || c.MemoryMiB != 2048 {
		t.Fatalf("options = %+v, %v", c, err)
	}
}

func TestWithExecutionCallLeavesACallWithoutOptionsAlone(t *testing.T) {
	body := json.RawMessage(`{"tool":"bash","command":"ls"}`)
	if got := withExecutionCall(context.Background(), body); string(got) != string(body) {
		t.Fatalf("got %s", got)
	}
	zero := contract.WithExecutionCall(context.Background(), contract.ExecutionCall{})
	if got := withExecutionCall(zero, body); string(got) != string(body) {
		t.Fatalf("got %s", got)
	}
}
