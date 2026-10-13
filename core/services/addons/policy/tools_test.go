// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mutableTools is a tool source whose declarations an upgrade can change.
type mutableTools struct {
	mu    sync.Mutex
	tools []string
}

func (m *mutableTools) DeclaredTools(context.Context, string, uint64) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tools == nil {
		return nil, errors.New("manifest unavailable")
	}
	return append([]string(nil), m.tools...), nil
}

func (m *mutableTools) set(t []string) { m.mu.Lock(); m.tools = t; m.mu.Unlock() }

var _ = Describe("proposal tool declarations", Label("proposals", "tools"), func() {
	ctx := context.Background()
	with := func(src gateway.ToolSource) func(*BrokerOptions) {
		return func(o *BrokerOptions) { o.Tools = src }
	}

	It("denies a proposal for an undeclared tool before any preview and stores nothing", func() {
		f := newBrokerFixture(ModeNamedUser, with(&mutableTools{tools: []string{"fill"}}))
		owner := named("owner")
		key := f.create(owner)
		for i, args := range []string{`{"tool":"wipe","rows":[{"a":1}]}`, `{"tool":"Fill","rows":[{"a":1}]}`, `{"tool":"fill ","rows":[{"a":1}]}`, `{"Tool":"fill","rows":[{"a":1}]}`} {
			_, err := f.broker.Propose(ctx, owner, f.request(key, "op-bad-"+string(rune('a'+i)), "write", args))
			Expect(err).To(MatchError(ErrForbidden), args)
			_, err = f.broker.Get(ctx, owner, "op-bad-"+string(rune('a'+i)))
			Expect(err).To(HaveOccurred(), "nothing was stored")
		}
		Expect(f.rt.previews).To(Equal(0))
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-ok", "write", `{"tool":"fill","rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.State).To(Equal(gateway.ProposalPending))
		Expect(f.rt.previews).To(Equal(1))
	})

	It("denies a proposal that names a tool when no tool source is installed", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		_, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"tool":"fill","rows":[{"a":1}]}`))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.rt.previews).To(Equal(0))
	})

	It("rechecks the stored tool at confirmation and never applies a withdrawn tool", func() {
		src := &mutableTools{tools: []string{"fill"}}
		f := newBrokerFixture(ModeNamedUser, with(src))
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"tool":"fill","rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		src.set([]string{"other"})
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.rt.applyCalls()).To(BeEmpty())
	})
})
