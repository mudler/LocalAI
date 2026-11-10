package studio

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// An unattended run that may not act gets no job started and no input
// written, as for bash on this machine; list, read and kill still work.
func TestGateJobToolByAction(t *testing.T) {
	s := &Service{log: slog.Default()}
	ran := map[string]int{}
	tool := session.Tool{Name: jobTool, Run: func(_ context.Context, args json.RawMessage) (string, error) {
		var in struct{ Action string }
		_ = json.Unmarshal(args, &in)
		ran[in.Action]++
		return "ran " + in.Action, nil
	}}
	gated := s.gateJobTool(session.ChatRecord{ID: "c"}, builtins.ActPolicy{ReadOnly: true}, tool)
	for _, a := range []string{JobList, JobRead, JobKill} {
		out, err := gated.Run(context.Background(), json.RawMessage(`{"action":"`+a+`"}`))
		if err != nil || out != "ran "+a {
			t.Fatalf("%s: got %q, %v", a, out, err)
		}
	}
	for _, a := range []string{JobStart, JobInput} {
		out, err := gated.Run(context.Background(), json.RawMessage(`{"action":"`+a+`"}`))
		if err != nil || out == "ran "+a || ran[a] != 0 {
			t.Fatalf("%s ran under a read-only policy: %q, %v", a, out, err)
		}
	}
	held := s.gateJobTool(session.ChatRecord{ID: "c"}, builtins.ActPolicy{Held: []string{builtins.ClassFiles, builtins.ClassSpend}}, tool)
	if out, _ := held.Run(context.Background(), json.RawMessage(`{"action":"start"}`)); out != "ran start" {
		t.Fatalf("a run that holds act.files and act.external should start a job, got %q", out)
	}
}
