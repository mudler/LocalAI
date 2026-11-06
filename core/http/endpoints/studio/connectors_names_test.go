package studio

import (
	"encoding/json"
	"testing"
	"time"

	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

// The access view names the sender and the chat of an event beside their IDs,
// for the recent decisions and for the tool calls. The fields are optional.
func TestAccessOutCarriesNamesAndChats(t *testing.T) {
	at := time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)
	v := studiosvc.AccessView{
		Binding: "b1",
		Recent: []studiosvc.AccessEvent{
			{At: at, Senders: []string{"U001"}, SenderName: "Ada Example", Chat: "C0123", ChatName: "ops", Capability: "memory.write", Allowed: true},
			{At: at, Senders: []string{"U002"}, Capability: "files.upload"},
		},
		ToolOutcomes: []studiosvc.AccessEvent{{At: at, Senders: []string{"U001"}, SenderName: "Ada Example", Chat: "C0123", Addon: "x", Tool: "t", Outcome: "ok"}},
	}
	raw, err := json.Marshal(accessOut(v, ""))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Recent []map[string]any `json:"recent"`
		Tools  []map[string]any `json:"tool_outcomes"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Recent[0]["sender_name"] != "Ada Example" || got.Recent[0]["chat"] != "C0123" || got.Recent[0]["chat_name"] != "ops" {
		t.Fatalf("a recent decision lost its names: %v", got.Recent[0])
	}
	for _, k := range []string{"sender_name", "chat", "chat_name"} {
		if _, ok := got.Recent[1][k]; ok {
			t.Fatalf("an event with no names must not carry %s: %v", k, got.Recent[1])
		}
	}
	if got.Tools[0]["sender_name"] != "Ada Example" || got.Tools[0]["chat"] != "C0123" {
		t.Fatalf("a tool call lost its names: %v", got.Tools[0])
	}
}
