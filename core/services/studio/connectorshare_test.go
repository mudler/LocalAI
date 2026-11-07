package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeShares stands for the sharing policy: which bindings are shared with
// whom, and the work admitted under each share.
type fakeShares struct {
	mu       sync.Mutex
	grants   map[string]map[string]bool // binding -> user
	owners   map[string]string          // binding -> owner name
	admitted int
	forgot   []string
	ensured  []string
}

func newFakeShares() *fakeShares {
	return &fakeShares{grants: map[string]map[string]bool{}, owners: map[string]string{}}
}

func (f *fakeShares) share(binding, user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grants[binding] == nil {
		f.grants[binding] = map[string]bool{}
	}
	f.grants[binding][user] = true
	f.owners[binding] = "Alice"
}

func (f *fakeShares) revoke(binding, user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.grants[binding], user)
}

func (f *fakeShares) Enabled() bool { return true }

func (f *fakeShares) Shared(_ context.Context, user string) ([]studio.SharedBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []studio.SharedBinding
	for b, users := range f.grants {
		if users[user] {
			out = append(out, studio.SharedBinding{BindingID: b, OwnerName: f.owners[b]})
		}
	}
	return out, nil
}

func (f *fakeShares) Admit(ctx context.Context, user, _ string, binding string, run func(context.Context) error) error {
	f.mu.Lock()
	ok := f.grants[binding][user]
	if ok {
		f.admitted++
	}
	f.mu.Unlock()
	if !ok {
		return connectors.ErrNoCredential
	}
	return run(ctx)
}

func (f *fakeShares) ShareOf(_ context.Context, _ string, binding string) (studio.ShareState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grants[binding] == nil {
		return studio.ShareState{}, false
	}
	return studio.ShareState{ResourceID: "r" + strings.Repeat("a", 31), SharedWith: len(f.grants[binding])}, true
}

func (f *fakeShares) Ensure(_ context.Context, _ string, binding string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, binding)
	return "r" + strings.Repeat("a", 31), nil
}

func (f *fakeShares) Forget(_ context.Context, _ string, binding string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgot = append(f.forgot, binding)
	delete(f.grants, binding)
	return nil
}

// credentialField matches a field name that could carry a secret or the
// owner's private settings of a binding.
var credentialField = regexp.MustCompile(`(?i)token|secret|credential|password|key|allow|paired|sender|notify|grant|caps|resources|channel|option`)

// fieldNames lists the field names of a struct type and of the structs it
// holds, so an embedded type cannot hide one.
func fieldNames(t reflect.Type, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		out = append(out, f.Name, f.Tag.Get("json"))
		out = append(out, fieldNames(f.Type, seen)...)
	}
	return out
}

var _ = Describe("a binding shared with another person", func() {
	ctx := context.Background()
	const bob = "bob"
	var (
		r      *connRig
		shares *fakeShares
		b      connectors.Binding
	)
	BeforeEach(func() {
		shares = newFakeShares()
		bot := newScriptedBot()
		tr := studiobot.NewBotTransport(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		r = newConnRigOpts(newShared(), connStore(), tr, connSay("x"), nil, func(o *studio.ConnectorOptions) { o.Shares = shares })
		r.bot = bot
		b = r.bind(studio.ConnectorInput{Name: "Team bot", Token: "token-alpha", Resources: []string{"acme/widgets"}})
		shares.share(b.ID, bob)
	})

	It("lets the grantee's calls use the credential inside the share and lists the binding as shared", func() {
		list, err := r.conns.Connections(ctx, bob, "telegram")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(b.ID))
		Expect(list[0].Shared).To(BeTrue())
		Expect(list[0].Owner).To(Equal("Alice"))
		Expect(r.conns.ConnectionName(ctx, bob, b.ID)).To(Equal("Team bot"))

		grant, err := r.conns.CredentialFor(ctx, bob, "telegram", b.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(grant.Secret).To(Equal("token-alpha"), "the add-on call gets the token, never the person")
		Expect(grant.Hold).NotTo(BeNil(), "the call of a shared binding is admitted work of the share")
		ran := false
		Expect(grant.Hold(ctx, func(context.Context) error { ran = true; return nil })).To(Succeed())
		Expect(ran).To(BeTrue())
		Expect(shares.admitted).To(Equal(1))

		own, err := r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(own.Hold).To(BeNil(), "the owner's own call has no share to hold")
	})

	It("refuses the grantee a resource off the list without the owner's list, and gives the owner the list", func() {
		_, err := r.conns.CredentialFor(ctx, bob, "telegram", b.ID, "acme/secret-plans")
		var refused *connectors.NotAllowedError
		Expect(errors.As(err, &refused)).To(BeTrue(), "the refusal stays a refusal of the resource")
		Expect(refused.Allowed).To(BeEmpty(), "the grantee never learns the owner's allowlist")
		_, err = r.conns.CredentialFor(ctx, alice, "telegram", b.ID, "acme/secret-plans")
		Expect(errors.As(err, &refused)).To(BeTrue())
		Expect(refused.Allowed).To(Equal([]string{"acme/widgets"}), "the owner may pick one from the list")
	})

	It("refuses the grantee every operation that is not a send, as for a binding that does not exist", func() {
		in := studio.ConnectorInput{Name: "Mine now", Kind: "telegram"}
		_, err := r.conns.GetConnector(ctx, bob, b.ID)
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = r.conns.UpdateConnector(ctx, bob, b.ID, in)
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = r.conns.SetConnectorEnabled(ctx, bob, b.ID, false)
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = r.conns.TestConnector(ctx, bob, b.ID)
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = r.conns.ConnectorSenders(ctx, bob, b.ID)
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = r.conns.AllowSender(ctx, bob, b.ID, "11")
		Expect(err).To(MatchError(connectors.ErrNotFound))
		_, err = r.conns.EnsureShare(ctx, bob, b.ID)
		Expect(err).To(MatchError(connectors.ErrNotFound), "a grantee cannot share it again")
		Expect(r.conns.DeleteConnector(ctx, bob, b.ID)).To(MatchError(connectors.ErrNotFound))
		_, err = r.conns.GetConnector(ctx, bob, "no-such-binding")
		Expect(err).To(MatchError(connectors.ErrNotFound), "an unknown binding is the same answer")

		still, err := r.conns.GetConnector(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(still.Name).To(Equal("Team bot"))
		Expect(still.Enabled).To(BeTrue())
		Expect(shares.forgot).To(BeEmpty())
	})

	It("shows the grantee the name and state of a shared binding and no secret", func() {
		views, err := r.conns.SharedConnectors(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(HaveLen(1))
		Expect(views[0].ID).To(Equal(b.ID))
		Expect(views[0].Name).To(Equal("Team bot"))
		Expect(views[0].OwnerName).To(Equal("Alice"))
		Expect(views[0].Role).To(Equal("use"))
		Expect(views[0].Enabled).To(BeTrue())
		raw, err := json.Marshal(views)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("token-alpha"))
		Expect(string(raw)).NotTo(ContainSubstring("acme/widgets"))
		Expect(r.logs.String()).NotTo(ContainSubstring("token-alpha"))

		mine, err := r.conns.SharedConnectors(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(BeEmpty(), "the owner's own bindings are not shared with the owner")
	})

	It("has no field that can carry a credential on any type of the grantee's path", func() {
		for _, t := range []reflect.Type{reflect.TypeOf(studio.SharedConnectorView{}), reflect.TypeOf(schema.ChatsSharedConnector{}),
			reflect.TypeOf(studio.SharedBinding{}), reflect.TypeOf(studio.ShareState{}), reflect.TypeOf(schema.ChatsConnectorShare{})} {
			for _, n := range fieldNames(t, map[reflect.Type]bool{}) {
				Expect(credentialField.MatchString(n)).To(BeFalse(), "%s has the field %q", t, n)
			}
		}
	})

	It("has no flag for a grant to everyone, which a binding never has", func() {
		for _, t := range []reflect.Type{reflect.TypeOf(studio.SharedConnectorView{}), reflect.TypeOf(schema.ChatsSharedConnector{}), reflect.TypeOf(studio.SharedBinding{})} {
			Expect(fieldNames(t, map[reflect.Type]bool{})).NotTo(ContainElement(MatchRegexp(`(?i)everyone`)), t.String())
		}
	})

	It("stops at once when the share goes: the next call is the answer of an unknown binding", func() {
		shares.revoke(b.ID, bob)
		_, err := r.conns.CredentialFor(ctx, bob, "telegram", b.ID, "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		_, err = r.conns.CredentialFor(ctx, bob, "telegram", "no-such-binding", "acme/widgets")
		Expect(err).To(MatchError(connectors.ErrNoCredential))
		list, err := r.conns.Connections(ctx, bob, "telegram")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		Expect(r.conns.ConnectionName(ctx, bob, b.ID)).To(BeEmpty())
		views, err := r.conns.SharedConnectors(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(BeEmpty())
	})

	It("is never chosen for the grantee: only a set that names it uses it", func() {
		set, err := r.conns.ConnectionsOf(ctx, bob, session.ChatRecord{}, "telegram")
		Expect(err).NotTo(HaveOccurred())
		Expect(set.Items).To(BeEmpty(), "the only connection rule counts the person's own bindings")
		only, err := r.conns.SoleConnection(ctx, bob, "telegram")
		Expect(err).NotTo(HaveOccurred())
		Expect(only.ID).To(BeEmpty())

		set, err = r.conns.ConnectionsOf(ctx, bob, session.ChatRecord{Connections: map[string][]string{"telegram": {b.ID}}}, "telegram")
		Expect(err).NotTo(HaveOccurred())
		Expect(set.IDs()).To(Equal([]string{b.ID}))
		Expect(set.By).To(Equal(studio.ConnectionByChat))
	})

	It("lists the share of each binding for its owner and forgets it when the binding is deleted", func() {
		list, err := r.conns.ListConnectors(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Share).NotTo(BeNil())
		Expect(list[0].Share.SharedWith).To(Equal(1))

		id, err := r.conns.EnsureShare(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(id).NotTo(BeEmpty())
		Expect(shares.ensured).To(Equal([]string{b.ID}))

		Expect(r.conns.DeleteConnector(ctx, alice, b.ID)).To(Succeed())
		Expect(shares.forgot).To(Equal([]string{b.ID}))
	})

	It("keeps working alone, with no sharing at all", func() {
		plain := newConnRig(newShared(), connStore(), newScriptedBot(), connSay("x"), nil)
		own := plain.bind(studio.ConnectorInput{Token: "t", Resources: []string{"acme/widgets"}})
		_, err := plain.conns.EnsureShare(ctx, alice, own.ID)
		Expect(errors.Is(err, studio.ErrSharingOff)).To(BeTrue())
		views, err := plain.conns.SharedConnectors(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(BeEmpty())
		Expect(plain.conns.DeleteConnector(ctx, alice, own.ID)).To(Succeed())
	})
})
