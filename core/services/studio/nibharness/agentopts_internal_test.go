package nibharness

import (
	"testing"

	"github.com/mudler/nib/types"
)

func TestApplyAgentOptionsOverridesOnlyWhatIsSet(t *testing.T) {
	cfg := types.Config{AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 2, MaxRetries: 5}}

	applyAgentOptions(&cfg, AgentOptions{})
	if got := cfg.AgentOptions; got.Iterations != 10 || got.MaxAttempts != 2 || got.MaxRetries != 5 || got.ForceReasoning {
		t.Fatalf("zero options changed the config: %+v", got)
	}

	applyAgentOptions(&cfg, AgentOptions{Iterations: 4, MaxAttempts: 7, ForceReasoning: true})
	if got := cfg.AgentOptions; got.Iterations != 4 || got.MaxAttempts != 7 || !got.ForceReasoning || got.MaxRetries != 5 {
		t.Fatalf("options not applied: %+v", got)
	}
}
