// SPDX-License-Identifier: MIT

package proposalflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// declaredTypes lists the type names in reduce.go that declare the marker
// method, so a new constructor or event that is not in AllStates or AllEvents
// fails the build of the tests.
func declaredTypes(marker string) []string {
	ginkgo.GinkgoHelper()
	f, err := parser.ParseFile(token.NewFileSet(), "reduce.go", nil, 0)
	Expect(err).NotTo(HaveOccurred())
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

var _ = ginkgo.Describe("the reducer", func() {
	ginkgo.It("lists every declared state and event", func() {
		var states, events []string
		for _, s := range AllStates() {
			states = append(states, s.Name())
		}
		for _, e := range AllEvents() {
			events = append(events, e.Name())
		}
		sort.Strings(states)
		sort.Strings(events)
		Expect(states).To(Equal(declaredTypes("isState")), "AllStates")
		Expect(events).To(Equal(declaredTypes("isEvent")), "AllEvents")
	})

	// Every (state, event) pair, with every parameter variant of the event,
	// goes to the reducer. No pair may panic, and every pair must return a
	// listed rule id and a state of the model.
	ginkgo.It("handles every state and event pair", func() {
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
			Expect(seen[s.Name()]).To(BeTrue(), "no sample state for %s", s.Name())
		}
		events := Universe()
		named := map[string]bool{}
		for _, e := range events {
			named[e.Name()] = true
		}
		for _, e := range AllEvents() {
			Expect(named[e.Name()]).To(BeTrue(), "Universe has no %s", e.Name())
		}
		for _, s := range states {
			for _, e := range events {
				var next State
				var rule string
				Expect(func() { next, _, rule = Reduce(s, e) }).NotTo(Panic(), "%s x %s", s.Name(), Describe(e))
				_, ok := RuleByID(rule)
				Expect(ok).To(BeTrue(), "%s x %s: rule %q not in the table", s.Name(), e.Name(), rule)
				Expect(next).NotTo(BeNil(), "%s x %s: nil state", s.Name(), e.Name())
			}
		}
	})

	ginkgo.It("is pure", func() {
		p := prop(KindWrite, RoleOwner)
		for _, s := range []State{Pending{P: p}, ExecSent{P: p}, OutcomeUnknown{P: p}} {
			for _, e := range Universe() {
				n1, f1, r1 := Reduce(s, e)
				n2, f2, r2 := Reduce(s, e)
				Expect(Key(n1)).To(Equal(Key(n2)), "%s x %s", s.Name(), e.Name())
				Expect(r1).To(Equal(r2), "%s x %s", s.Name(), e.Name())
				Expect(strings.Join(EffectNames(f1), "|")).To(Equal(strings.Join(EffectNames(f2), "|")), "%s x %s", s.Name(), e.Name())
			}
		}
	})

	ginkgo.It("has a rule table without duplicates", func() {
		seen := map[string]bool{}
		for _, r := range Rules {
			Expect(seen[r.ID]).To(BeFalse(), "duplicate rule %s", r.ID)
			seen[r.ID] = true
			Expect(r.Text).NotTo(BeEmpty(), "rule %s has no text", r.ID)
		}
	})

	ginkgo.It("takes the action from the tool kind", func() {
		Expect(ActionOf(KindSchema)).To(Equal(ActionSchema))
		Expect(ActionOf(KindWrite)).To(Equal(ActionWrite))
		Expect(ActionOf(KindRead)).To(Equal(ActionNone), "a read tool must not map to a mutating action")
		Expect(ActionOf(KindUndeclared)).To(Equal(ActionNone), "an undeclared tool must not map to a mutating action")
		// The claimed action of the request never changes the result.
		for _, claimed := range []Action{ActionNone, ActionWrite, ActionSchema} {
			for _, role := range []Role{RoleViewer, RoleEditor, RoleOwner} {
				a := propose(role, KindSchema)
				a.Claimed = claimed
				b := propose(role, KindSchema)
				b.Claimed = ActionWrite
				sa, _, ra := Reduce(Unproposed{}, a)
				sb, _, rb := Reduce(Unproposed{}, b)
				Expect(Key(sa)).To(Equal(Key(sb)), "the claimed action %v changed the outcome for %v", claimed, role)
				Expect(ra).To(Equal(rb))
			}
		}
	})
})
