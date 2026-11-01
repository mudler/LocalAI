// SPDX-License-Identifier: MIT
package memory

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"reflect"
)

var _ = Describe("Bounded safety and liveness", func() {
	It("explores two owners and sources through three attempts and prints counterexample paths", func() {
		type node struct {
			state State
			path  string
		}
		for _, source := range []string{"source-a", "source-b"} {
			queue := []node{{Initial(source, SummaryInput{Text: "input"}), source}}
			seen := map[string]bool{}
			for len(queue) > 0 {
				n := queue[0]
				queue = queue[1:]
				key := fmt.Sprintf("%#v", n.state)
				if seen[key] {
					continue
				}
				seen[key] = true
				Expect(len(seen)).To(BeNumerically("<", 12000), n.path)
				Expect(CheckInvariants(n.state)).To(Succeed(), n.path)
				if terminal(n.state) {
					continue
				}
				Expect(expires(n.state, 12)).To(BeTrue(), n.path)
				for _, e := range enabledEvents(n.state) {
					tr := Reduce(n.state, e)
					Expect(CheckInvariants(tr.State)).To(Succeed(), "%s -> %T", n.path, e)
					for _, effect := range tr.Effects {
						if w, ok := effect.(ReconcileOrWrite); ok {
							Expect(n.state).To(BeAssignableToTypeOf(Authorizing{}), n.path)
							Expect(e).To(BeAssignableToTypeOf(AuthorizationGranted{}), n.path)
							Expect(w.Work.Job.Input).To(BeAssignableToTypeOf(FrozenPayload{}), n.path)
							Expect(w.Work.Job.Identity.Destination).To(Equal("collection"))
						}
					}
					if !reflect.DeepEqual(tr.State, n.state) {
						queue = append(queue, node{tr.State, n.path + fmt.Sprintf(" -> %T/%s", e, tr.RuleID)})
					}
				}
			}
			Expect(len(seen)).To(BeNumerically(">", 20))
		}
	})
	It("models fenced backend receipts and tombstones without claiming backend atomicity", func() {
		world := newWorld()
		for _, owner := range []string{"frontend-a", "frontend-b"} {
			world.claim("key", owner)
			world.write("key", owner, world.fences["key"])
		}
		Expect(world.entries).To(HaveLen(1))
		Expect(world.write("key", "frontend-a", 1)).To(BeFalse())
		world.deleted["key"] = true
		Expect(world.write("key", "frontend-b", 2)).To(BeFalse())
		Expect(world.entries).To(HaveLen(1))
	})
})

// These assumptions belong to the future atomic backend/claim contract, not Reduce.
type modelWorld struct {
	fences  map[string]int
	owners  map[string]string
	entries map[string]string
	deleted map[string]bool
}

func newWorld() *modelWorld {
	return &modelWorld{map[string]int{}, map[string]string{}, map[string]string{}, map[string]bool{}}
}
func (w *modelWorld) claim(key, owner string) { w.fences[key]++; w.owners[key] = owner }
func (w *modelWorld) write(key, owner string, fence int) bool {
	if w.deleted[key] || w.fences[key] != fence || w.owners[key] != owner {
		return false
	}
	if _, ok := w.entries[key]; !ok {
		w.entries[key] = "exact-entry"
	}
	return true
}
func expires(s State, budget int) bool {
	if terminal(s) {
		return true
	}
	if budget == 0 {
		return false
	}
	for _, e := range enabledEvents(s) {
		tr := Reduce(s, e)
		if !reflect.DeepEqual(s, tr.State) && expires(tr.State, budget-1) {
			return true
		}
	}
	return false
}
