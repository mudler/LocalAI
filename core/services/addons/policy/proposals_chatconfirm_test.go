// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A held write can be confirmed by a paired sender in a messaging chat when it
// waits only because the connection asks first. The host marks the call; the
// owner honors the mark for that hold and for no other.
var _ = Describe("the confirmation of a held write from a messaging chat", Label("proposals", "credentials", "approval"), func() {
	ctx := context.Background()
	who := named("owner")
	const device = "https://pikvm.local"
	manifest := shippedManifest("addons/pikvm/manifest.json")
	powerOff := json.RawMessage(`{"tool":"power-action","device":"` + device + `","command":"off"}`)
	typeText := json.RawMessage(`{"tool":"type-text","device":"` + device + `","text":"hello"}`)

	open := func(mode string) (*brokerFixture, *spyRuntime) {
		binding := &bindingSource{mode: mode, repos: []string{device}, token: "kvm-user:kvm-pass"}
		rt := newFixtureRuntime()
		spy := &spyRuntime{fixtureRuntime: rt}
		f := openBrokerFixture(ModeNamedUser, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: spy}, &clock{t: time.Now()}, &account{blocked: map[string]bool{}},
			func(o *BrokerOptions) {
				o.Tools = manifestTools{caps: manifest.Capabilities}
				o.Credentials = binding
				o.ConfirmCredential = func(a contract.Actor) bool {
					return a.CredentialID == "chats" || strings.HasPrefix(a.CredentialID, "session:")
				}
				o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
			})
		DeferCleanup(func() { f.close() })
		return f, spy
	}
	propose := func(f *brokerFixture, args json.RawMessage) Proposal {
		p, err := f.broker.Propose(ctx, who, contract.Request{AddonID: "pikvm", OperationID: "op-1", Generation: 1, Action: "write", Arguments: args})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.AwaitsPerson).To(BeTrue())
		return p
	}

	It("refuses the chat credential without the mark, runs it once with the mark, and does nothing on a replay", func() {
		f, spy := open(gateway.WritePropose)
		p := propose(f, powerOff)
		Expect(p.Hold).To(Equal(HoldWriteModePropose))
		_, err := f.broker.ConfirmBound(ctx, chat("owner"), "op-1", p.Digest)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(spy.applies).To(BeEmpty())

		_, err = f.broker.ConfirmBound(gateway.WithChatConfirmed(ctx), chat("owner"), "op-1", p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(spy.applies).To(HaveLen(1))

		_, err = f.broker.ConfirmBound(gateway.WithChatConfirmed(ctx), chat("owner"), "op-1", p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(spy.applies).To(HaveLen(1), "a replay returns the stored answer and sends nothing again")
	})

	It("refuses a digest other than the one stored, even with the mark", func() {
		f, spy := open(gateway.WritePropose)
		propose(f, powerOff)
		_, err := f.broker.ConfirmBound(gateway.WithChatConfirmed(ctx), chat("owner"), "op-1", "not-the-digest")
		Expect(err).To(MatchError(ErrProposalChanged))
		Expect(spy.applies).To(BeEmpty())
	})

	It("does not let the mark lift a tool that always asks", func() {
		f, spy := open(gateway.WriteAuto)
		p := propose(f, typeText)
		Expect(p.Hold).To(Equal("approval_always"))
		_, err := f.broker.ConfirmBound(gateway.WithChatConfirmed(ctx), chat("owner"), "op-1", p.Digest)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(spy.applies).To(BeEmpty())
	})

	It("does not let another user confirm with the mark", func() {
		f, spy := open(gateway.WritePropose)
		p := propose(f, powerOff)
		_, err := f.broker.ConfirmBound(gateway.WithChatConfirmed(ctx), chat("someone-else"), "op-1", p.Digest)
		Expect(err).To(HaveOccurred())
		Expect(spy.applies).To(BeEmpty())
	})
})
