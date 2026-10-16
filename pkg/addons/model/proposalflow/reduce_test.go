// SPDX-License-Identifier: MIT

package proposalflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// declaredTypes lists the type names in reduce.go that declare the marker
// method, so a new constructor or event that is not in AllStates or AllEvents
// fails the build of the tests.
func declaredTypes(t *testing.T, marker string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "reduce.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != marker {
			continue
		}
		if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
			out = append(out, id.Name)
		}
	}
	sort.Strings(out)
	return out
}

func TestAllStatesAndEventsListed(t *testing.T) {
	var states, events []string
	for _, s := range AllStates() {
		states = append(states, s.Name())
	}
	for _, e := range AllEvents() {
		events = append(events, e.Name())
	}
	sort.Strings(states)
	sort.Strings(events)
	if got, want := strings.Join(states, ","), strings.Join(declaredTypes(t, "isState"), ","); got != want {
		t.Fatalf("AllStates %s, declared %s", got, want)
	}
	if got, want := strings.Join(events, ","), strings.Join(declaredTypes(t, "isEvent"), ","); got != want {
		t.Fatalf("AllEvents %s, declared %s", got, want)
	}
}

// TestEveryPairIsHandled feeds every (state, event) pair, with every
// parameter variant of the event, to the reducer. No pair may panic, and every
// pair must return a listed rule id and a state of the model.
func TestEveryPairIsHandled(t *testing.T) {
	p := prop(KindWrite, RoleOwner)
	states := []State{
		Unproposed{}, Refused{Why: RefuseUndeclaredTool}, Pending{P: p}, Admitting{P: p, By: "alice"}, ExecPrepared{P: p}, ExecSent{P: p},
		OutcomeUnknown{P: p, Lookups: 0}, OutcomeUnknown{P: p, Lookups: 1}, UnknownManual{P: p}, Abandoned{P: p}, Applied{P: p},
		Rejected{P: p, Why: RejectReceipt}, NotApplied{P: p}, Cancelled{P: p, Why: CancelByHold}, Expired{P: p}, Stale{P: p}, Dropped{P: p},
	}
	seen := map[string]bool{}
	for _, s := range states {
		seen[s.Name()] = true
	}
	for _, s := range AllStates() {
		if !seen[s.Name()] {
			t.Fatalf("no sample state for %s", s.Name())
		}
	}
	events := Universe()
	named := map[string]bool{}
	for _, e := range events {
		named[e.Name()] = true
	}
	for _, e := range AllEvents() {
		if !named[e.Name()] {
			t.Fatalf("Universe has no %s", e.Name())
		}
	}
	for _, s := range states {
		for _, e := range events {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s x %s panicked: %v", s.Name(), Describe(e), r)
					}
				}()
				next, _, rule := Reduce(s, e)
				if _, ok := RuleByID(rule); !ok {
					t.Errorf("%s x %s: rule %q not in the table", s.Name(), e.Name(), rule)
				}
				if next == nil {
					t.Errorf("%s x %s: nil state", s.Name(), e.Name())
				}
			}()
		}
	}
}

func TestReducerIsPure(t *testing.T) {
	p := prop(KindWrite, RoleOwner)
	for _, s := range []State{Pending{P: p}, ExecSent{P: p}, OutcomeUnknown{P: p}} {
		for _, e := range Universe() {
			n1, f1, r1 := Reduce(s, e)
			n2, f2, r2 := Reduce(s, e)
			if Key(n1) != Key(n2) || r1 != r2 || strings.Join(EffectNames(f1), "|") != strings.Join(EffectNames(f2), "|") {
				t.Fatalf("%s x %s is not deterministic", s.Name(), e.Name())
			}
		}
	}
}

func TestRuleTableHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Rules {
		if seen[r.ID] {
			t.Fatalf("duplicate rule %s", r.ID)
		}
		seen[r.ID] = true
		if r.Text == "" {
			t.Fatalf("rule %s has no text", r.ID)
		}
	}
}

func TestActionComesFromTheToolKind(t *testing.T) {
	if ActionOf(KindSchema) != ActionSchema || ActionOf(KindWrite) != ActionWrite {
		t.Fatal("kind to action mapping is wrong")
	}
	if ActionOf(KindRead) != ActionNone || ActionOf(KindUndeclared) != ActionNone {
		t.Fatal("a read or undeclared tool must not map to a mutating action")
	}
	// The claimed action of the request never changes the result.
	for _, claimed := range []Action{ActionNone, ActionWrite, ActionSchema} {
		for _, role := range []Role{RoleViewer, RoleEditor, RoleOwner} {
			a := propose(role, KindSchema)
			a.Claimed = claimed
			b := propose(role, KindSchema)
			b.Claimed = ActionWrite
			sa, _, ra := Reduce(Unproposed{}, a)
			sb, _, rb := Reduce(Unproposed{}, b)
			if Key(sa) != Key(sb) || ra != rb {
				t.Fatalf("the claimed action %v changed the outcome for %v", claimed, role)
			}
		}
	}
}
