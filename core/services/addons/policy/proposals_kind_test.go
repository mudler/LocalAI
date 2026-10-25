// SPDX-License-Identifier: MIT
package policy

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newKindTools() *mutableTools {
	return &mutableTools{
		tools: []string{"fill", "rename", "peek", "make", "bare"},
		// "bare" is declared without a definition.
		kinds: map[string]string{"fill": contract.ToolKindWrite, "rename": contract.ToolKindSchema, "peek": contract.ToolKindRead, "make": contract.ToolKindCreate},
	}
}

// namesOnly reports declared tools and nothing else.
type namesOnly struct{ tools []string }

func (n namesOnly) DeclaredTools(context.Context, string, uint64) ([]string, error) {
	return n.tools, nil
}

// Rules R3 (a read tool is not a proposal), R4 (a role below the action derived
// from the tool kind is refused at Propose) and S4 (a schema action is never
// admitted for a non-owner) of the proposal flow model.
var _ = Describe("the action of a proposal follows the tool kind", Label("proposals", "tools"), func() {
	ctx := context.Background()
	var (
		f           *brokerFixture
		owner, edit = named("owner"), named("editor")
		key         ResourceKey
	)
	BeforeEach(func() {
		f = newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) { o.Tools = newKindTools() })
		key = f.create(owner)
		Expect(f.policy.SetGrant(ctx, owner, key, "editor", RoleEditor)).To(Succeed())
	})
	propose := func(who contract.Actor, op, action, tool string) (Proposal, error) {
		return f.broker.Propose(ctx, who, f.request(key, op, action, `{"tool":"`+tool+`","rows":[1]}`))
	}

	It("R4, S4: refuses an editor who claims the write action for a schema tool, and stores nothing", func() {
		_, err := propose(edit, "op-1", "write", "rename")
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.broker.Get(ctx, owner, "op-1")
		Expect(err).To(HaveOccurred(), "nothing was stored")
		Expect(f.rt.previews).To(BeZero())
	})
	It("R4: refuses an editor who names the schema action for a schema tool", func() {
		_, err := propose(edit, "op-1", "schema", "rename")
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.rt.previews).To(BeZero())
	})
	It("derives the action from the tool, so the owner needs the matching claim and gets the schema action", func() {
		_, err := propose(owner, "op-1", "write", "rename")
		Expect(err).To(MatchError(ErrForbidden), "a claim that disagrees with the tool kind is refused")
		p, err := propose(owner, "op-2", "schema", "rename")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.State).To(Equal(gateway.ProposalPending))
		stored, ok := f.broker.store.get("op-2")
		Expect(ok).To(BeTrue())
		Expect(stored.Action).To(Equal("schema"))
		// The stored action is what a demoted-editor confirmation is judged by.
		_, err = propose(owner, "op-3", "schema", "fill")
		Expect(err).To(MatchError(ErrForbidden), "a data tool is not a schema action either")
	})
	It("still lets an editor propose a data write for a write tool", func() {
		p, err := propose(edit, "op-1", "write", "fill")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.State).To(Equal(gateway.ProposalPending))
	})
	It("R3: refuses a read tool, a create tool and a tool without a definition", func() {
		for i, tool := range []string{"peek", "make", "bare"} {
			for _, who := range []contract.Actor{owner, edit} {
				for _, action := range []string{"write", "schema"} {
					_, err := propose(who, "op-"+string(rune('a'+i)), action, tool)
					Expect(err).To(MatchError(ErrForbidden), who.UserID+" "+action+" "+tool)
				}
			}
		}
		Expect(f.rt.previews).To(BeZero())
	})
	It("runs the declaration check as well as the kind check", func() {
		_, err := propose(owner, "op-1", "schema", "wipe")
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.rt.previews).To(BeZero())
	})
	It("refuses a named tool when the source cannot report kinds", func() {
		g := newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) { o.Tools = namesOnly{tools: []string{"fill"}} })
		k := g.create(owner)
		_, err := g.broker.Propose(ctx, owner, g.request(k, "op-1", "write", `{"tool":"fill","rows":[1]}`))
		Expect(err).To(MatchError(ErrForbidden))
	})
	It("R3: a creation names a create tool, never a schema or data tool", func() {
		create := func(op, tool string) error {
			_, err := f.broker.Propose(ctx, edit, contract.Request{AddonID: key.AddonID, OperationID: op, Generation: 1, Action: "create",
				Arguments: []byte(`{"tool":"` + tool + `","name":"Sales"}`)})
			return err
		}
		for i, tool := range []string{"rename", "fill", "peek"} {
			Expect(create("op-c"+string(rune('a'+i)), tool)).To(MatchError(ErrForbidden), tool)
		}
		Expect(create("op-ok", "make")).NotTo(HaveOccurred())
	})
})
