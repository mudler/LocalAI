// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mutableTools is a tool source whose declarations an upgrade can change.
type mutableTools struct {
	mu    sync.Mutex
	tools []string
	// kinds maps a tool to its kind. With nil every declared tool is a write
	// tool; with a map, a tool missing from it has no definition.
	kinds map[string]string
	err   error
}

// DeclaredCapabilities reports the tool definitions of the installed manifest.
func (m *mutableTools) DeclaredCapabilities(ctx context.Context, id string, gen uint64) (contract.Capabilities, error) {
	names, err := m.DeclaredTools(ctx, id, gen)
	if err != nil {
		return contract.Capabilities{}, err
	}
	c := contract.Capabilities{Tools: names}
	for _, n := range names {
		kind, ok := m.kinds[n]
		if m.kinds == nil {
			kind, ok = contract.ToolKindWrite, true
		}
		if ok {
			c.ToolDefinitions = append(c.ToolDefinitions, contract.ToolDefinition{Name: n, Kind: kind})
		}
	}
	return c, nil
}

func (m *mutableTools) DeclaredTools(context.Context, string, uint64) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	if m.tools == nil {
		return nil, errors.New("manifest unavailable")
	}
	return append([]string(nil), m.tools...), nil
}

func (m *mutableTools) set(t []string) { m.mu.Lock(); m.tools = t; m.mu.Unlock() }
func (m *mutableTools) fail(e error)   { m.mu.Lock(); m.err = e; m.mu.Unlock() }

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

	It("denies a proposal whose arguments repeat a key and stores nothing", func() {
		f := newBrokerFixture(ModeNamedUser, with(&mutableTools{tools: []string{"fill"}}))
		owner := named("owner")
		key := f.create(owner)
		for i, args := range []string{
			`{"tool":"fill","tool":"wipe","rows":[{"a":1}]}`,
			`{"tool":"wipe","tool":"fill","rows":[{"a":1}]}`,
			`{"tool":"fill","\u0074ool":"wipe"}`,
			`{"tool":"fill","rows":[{"a":1,"a":2}]}`,
			`{"tool":"fill","TOOL":"fill"}`,
		} {
			op := "op-dup-" + string(rune('a'+i))
			_, err := f.broker.Propose(ctx, owner, f.request(key, op, "write", args))
			Expect(err).To(MatchError(ErrForbidden), args)
			_, err = f.broker.Get(ctx, owner, op)
			Expect(err).To(HaveOccurred(), "nothing was stored")
		}
		Expect(f.rt.previews).To(Equal(0))
	})

	It("reports an unavailable declaration source as unavailable, never as a denial", func() {
		src := &mutableTools{tools: []string{"fill"}}
		f := newBrokerFixture(ModeNamedUser, with(src))
		owner := named("owner")
		key := f.create(owner)
		args := `{"tool":"fill","rows":[{"a":1}]}`
		for i, cause := range []error{addons.ErrNotOwner, addons.ErrRecoveryRequired, context.Canceled, errors.New("manifest unavailable")} {
			src.fail(cause)
			op := "op-un-" + string(rune('a'+i))
			_, err := f.broker.Propose(ctx, owner, f.request(key, op, "write", args))
			Expect(err).To(MatchError(ErrUnavailable), cause.Error())
			Expect(err).NotTo(MatchError(ErrForbidden), cause.Error())
			Expect(gateway.StatusFor(err)).To(Equal(http.StatusServiceUnavailable))
			_, err = f.broker.Get(ctx, owner, op)
			Expect(err).To(HaveOccurred(), "nothing was stored")
		}
		Expect(f.rt.previews).To(Equal(0))
		// A real "not declared" stays a denial.
		src.fail(nil)
		src.set([]string{"other"})
		_, err := f.broker.Propose(ctx, owner, f.request(key, "op-den", "write", args))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(gateway.StatusFor(err)).To(Equal(http.StatusForbidden))
	})

	It("does not apply at confirmation while the declaration source is unavailable, and applies once it recovers", func() {
		src := &mutableTools{tools: []string{"fill"}}
		f := newBrokerFixture(ModeNamedUser, with(src))
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "op-1", "write", `{"tool":"fill","rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		src.fail(addons.ErrRecoveryRequired)
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).To(MatchError(ErrUnavailable))
		Expect(err).NotTo(MatchError(ErrForbidden))
		Expect(gateway.StatusFor(err)).To(Equal(http.StatusServiceUnavailable))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		src.fail(nil)
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.rt.applyCalls()).To(HaveLen(1))
	})
})
