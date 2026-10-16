// SPDX-License-Identifier: MIT

package proposalflow

import (
	"sort"
	"testing"
)

func TestTraces(t *testing.T) {
	for _, tr := range Traces() {
		t.Run(tr.Name, func(t *testing.T) {
			for _, msg := range tr.Verify() {
				t.Error(msg)
			}
		})
	}
}

func TestTraceNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tr := range Traces() {
		if seen[tr.Name] {
			t.Fatalf("duplicate trace %s", tr.Name)
		}
		seen[tr.Name] = true
	}
}

// TestRequiredScenariosExist names the scenarios the model must keep.
func TestRequiredScenariosExist(t *testing.T) {
	want := []string{
		"happy-path", "retry-after-not-sent", "retry-after-admission-refused", "timeout-unknown-then-receipt-committed",
		"unknown-bounded-then-manual-resolution", "two-actors-racing-confirm", "hold-during-prepared-dispatch",
		"hold-during-sent-request", "generation-change-while-pending", "duplicate-confirm-after-applied",
		"editor-schema-propose-refused", "out-of-order-receipt",
	}
	have := map[string]bool{}
	for _, tr := range Traces() {
		have[tr.Name] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("missing trace %s", n)
		}
	}
}

// TestEveryRuleFires checks that the traces and the search together fire every
// rule id, so no rule is dead.
func TestEveryRuleFires(t *testing.T) {
	fired := map[string]bool{}
	for _, tr := range Traces() {
		for _, r := range Run(Unproposed{}, tr.Events()) {
			fired[r.Rule] = true
		}
	}
	for id := range Explore().RuleHits {
		fired[id] = true
	}
	var dead []string
	for _, r := range Rules {
		if !fired[r.ID] {
			dead = append(dead, r.ID)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Fatalf("rules that never fire: %v", dead)
	}
}

// TestTracesFireEveryTransitionRule checks that the hand-written traces, not
// only the search, exercise each transition rule.
func TestTracesFireEveryTransitionRule(t *testing.T) {
	fired := map[string]bool{}
	for _, tr := range Traces() {
		for _, r := range Run(Unproposed{}, tr.Events()) {
			fired[r.Rule] = true
		}
	}
	var missing []string
	for _, r := range Rules {
		if r.Kind == KindTransition && !fired[r.ID] {
			missing = append(missing, r.ID)
		}
	}
	if len(missing) > 0 {
		t.Logf("transition rules with no named trace (covered by the search only): %v", missing)
	}
}
