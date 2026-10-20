// SPDX-License-Identifier: MIT
package idle

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ contract.Runtime = Runtime{}

func TestEveryOperationRefusesBecauseTheAddonDeclaresNoTools(t *testing.T) {
	ctx := context.Background()
	rt := Runtime{}
	if _, err := rt.Read(ctx, contract.Envelope{}); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Read: %v", err)
	}
	if _, err := rt.Preview(ctx, contract.Envelope{}); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := rt.Apply(ctx, contract.Envelope{}, contract.Revision{}); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := rt.Receipt(ctx, "x"); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Receipt: %v", err)
	}
	if _, err := rt.OpenOutput(ctx, contract.Envelope{}, "x"); !errors.Is(err, ErrNoTools) {
		t.Fatalf("OpenOutput: %v", err)
	}
}

func TestRunRefusesWithoutTheProtocolFlag(t *testing.T) {
	if got := Run([]string{"voice"}); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
	if got := Run([]string{"voice", "--other"}); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
}
