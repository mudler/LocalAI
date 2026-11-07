// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rejectingRuntime rejects the credentials in reject at preview and apply,
// as the runtime client reports the add-on's 401 credential_rejected.
type rejectingRuntime struct {
	*spyRuntime
	mu     sync.Mutex
	reject map[string]bool
}

func (r *rejectingRuntime) rejects(e contract.Envelope) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reject[e.Request.Credential]
}

func (r *rejectingRuntime) Preview(ctx context.Context, e contract.Envelope) (contract.Preview, error) {
	if r.rejects(e) {
		r.smu.Lock()
		r.previews = append(r.previews, e)
		r.smu.Unlock()
		return contract.Preview{}, addonruntime.ErrCredentialRejected
	}
	return r.spyRuntime.Preview(ctx, e)
}

func (r *rejectingRuntime) Apply(ctx context.Context, e contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	if r.rejects(e) {
		r.smu.Lock()
		r.applies = append(r.applies, e)
		r.smu.Unlock()
		return contract.Receipt{}, addonruntime.ErrCredentialRejected
	}
	return r.spyRuntime.Apply(ctx, e, rev)
}

// loginSource is an OAuth login: version n holds "token-n".
type loginSource struct {
	mu      sync.Mutex
	version int64
	ended   bool
	renews  []string
}

func (l *loginSource) grant() gateway.CredentialGrant {
	return gateway.CredentialGrant{Credential: fmt.Sprintf("token-%d", l.version), Version: l.version, ResourceKey: "acme/widgets", ResourceID: "r-0123456789",
		WriteMode: gateway.WritePropose, BindingID: "b1", GrantedScopes: []string{"write"}}
}

func (l *loginSource) Credential(context.Context, contract.Actor, string, string) (gateway.CredentialGrant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeReconnectNeeded}
	}
	return l.grant(), nil
}

func (l *loginSource) RenewCredential(_ context.Context, _ contract.Actor, _ string, g gateway.CredentialGrant, final bool) (gateway.CredentialGrant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.renews = append(l.renews, fmt.Sprintf("%d|%v", g.Version, final))
	if final {
		l.ended = true
		return gateway.CredentialGrant{}, &gateway.CredentialRefusal{Code: gateway.CodeReconnectNeeded}
	}
	if g.Version == l.version {
		l.version++
	}
	return l.grant(), nil
}

var _ = Describe("a write tool whose OAuth token the add-on rejects", Label("proposals", "credentials"), func() {
	ctx := context.Background()
	var (
		f     *brokerFixture
		rt    *rejectingRuntime
		login *loginSource
		who   = named("owner")
	)
	BeforeEach(func() {
		login = &loginSource{version: 1}
		base := newFixtureRuntime()
		rt = &rejectingRuntime{spyRuntime: &spyRuntime{fixtureRuntime: base}, reject: map[string]bool{}}
		f = openBrokerFixture(ModeNamedUser, GinkgoT().TempDir(), base, &fixtureInstances{gen: 1, rt: rt}, &clock{t: time.Now()}, &account{blocked: map[string]bool{}},
			func(o *BrokerOptions) {
				o.Tools = credentialTools{}
				o.Credentials = login
				o.ConfirmCredential = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
				o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
			})
		DeferCleanup(func() { f.close() })
	})
	request := contract.Request{AddonID: "providera", OperationID: "op-1", Generation: 1, Action: "write",
		Arguments: json.RawMessage(`{"tool":"comment","repo":"acme/widgets","body":"hello"}`)}

	It("renews it once at the preview and gives the add-on the scopes of the login", func() {
		rt.reject["token-1"] = true
		_, err := f.broker.Propose(ctx, who, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(rt.previews).To(HaveLen(2))
		Expect(rt.previews[1].Request.Credential).To(Equal("token-2"))
		Expect(rt.previews[1].Request.GrantedScopes).To(Equal([]string{"write"}))
		Expect(login.renews).To(Equal([]string{"1|false"}))
	})

	It("refuses the preview with reconnect_needed after a second rejection, and stores nothing", func() {
		rt.reject["token-1"], rt.reject["token-2"] = true, true
		_, err := f.broker.Propose(ctx, who, request)
		var refusal *gateway.CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue(), "%v", err)
		Expect(refusal.Code).To(Equal(gateway.CodeReconnectNeeded))
		_, err = f.broker.Get(ctx, who, "op-1")
		Expect(err).To(HaveOccurred())
	})

	It("renews it once at the apply, and after a second rejection keeps the proposal pending", func() {
		_, err := f.broker.Propose(ctx, who, request)
		Expect(err).NotTo(HaveOccurred())
		rt.reject["token-1"] = true
		receipt, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(rt.applies).To(HaveLen(2))
		Expect(rt.applies[1].Request.Credential).To(Equal("token-2"))

		second := request
		second.OperationID = "op-2"
		_, err = f.broker.Propose(ctx, who, second)
		Expect(err).NotTo(HaveOccurred())
		rt.reject["token-2"], rt.reject["token-3"] = true, true
		_, err = f.broker.Confirm(ctx, session("owner"), "op-2")
		var refusal *gateway.CredentialRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue(), "%v", err)
		Expect(refusal.Code).To(Equal(gateway.CodeReconnectNeeded))
		got, err := f.broker.Get(ctx, who, "op-2")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalPending), "nothing was applied, so the proposal waits")
	})
})
