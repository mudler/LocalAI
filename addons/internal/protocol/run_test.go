// SPDX-License-Identifier: MIT
package protocol

import (
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestRunRefusesWithoutTheProtocolFlagAndBuildsNoRuntime(t *testing.T) {
	built := false
	factory := func(Startup) contract.Runtime { built = true; return nil }
	for _, args := range [][]string{{"x"}, {"x", "--other"}, {"x", "--localai-addon-v1", "extra"}} {
		if got := Run(args, factory); got != 2 {
			t.Fatalf("%v: exit code %d, want 2", args, got)
		}
	}
	if built {
		t.Fatal("the factory ran before the protocol flag was checked")
	}
}
