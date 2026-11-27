package plugins_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

var _ = Describe("the state of each user", func() {
	var (
		ctx context.Context
		st  store.Store
		svc *plugins.Service
	)
	alice := plugins.Actor{ID: "alice"}
	bob := plugins.Actor{ID: "bob"}
	install := func(id, scope, owner string) {
		m, _ := json.Marshal(plugins.Model{Name: "fmt-" + id})
		digest := "sha256:" + id + "000000000000000000000000000000000000000000000000000000000000000"[:64-len(id)]
		Expect(st.PutPackage(ctx, store.Package{Digest: digest, Name: "fmt-" + id, Model: m})).To(Succeed())
		Expect(st.InstallTx(ctx, func(tx store.Tx) error {
			return tx.CreateInstall(store.Install{ID: id, PluginID: "fmt-" + id + "@m", Scope: scope, Owner: owner, Digest: digest, State: store.StateInstalled})
		})).To(Succeed())
	}
	active := func(a plugins.Actor) []string {
		list, err := svc.TurnView(ctx, a.ID, plugins.ChatRef{})
		Expect(err).NotTo(HaveOccurred())
		var ids []string
		for _, ap := range list {
			ids = append(ids, ap.InstallID)
		}
		return ids
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		st, err = store.NewFileStore("")
		Expect(err).NotTo(HaveOccurred())
		svc, err = plugins.New(plugins.Options{Store: st, Fetcher: newFakeFetcher()})
		Expect(err).NotTo(HaveOccurred())
		install("a1", store.ScopeInstance, "")
		install("b1", store.ScopeUser, "bob")
	})

	It("starts an install for everyone off, and turns it on and off for one user only", func() {
		Expect(active(alice)).To(BeEmpty())
		Expect(svc.SetEnabled(ctx, alice, "a1", true)).To(Succeed())
		Expect(active(alice)).To(Equal([]string{"a1"}))
		Expect(active(bob)).To(BeEmpty(), "bob did not enable it")
		Expect(svc.SetEnabled(ctx, alice, "a1", false)).To(Succeed())
		Expect(active(alice)).To(BeEmpty())
	})

	It("keeps the settings of a user who turns a plugin off", func() {
		Expect(st.SaveUserState(ctx, store.UserState{Principal: "alice", InstallID: "a1", Enabled: true, Config: json.RawMessage(`{"k":"v"}`)})).To(Succeed())
		Expect(svc.SetEnabled(ctx, alice, "a1", false)).To(Succeed())
		us, err := st.UserState(ctx, "alice", "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(us.Enabled).To(BeFalse())
		Expect(string(us.Config)).To(MatchJSON(`{"k":"v"}`))
	})

	It("hides another user's install", func() {
		Expect(svc.SetEnabled(ctx, alice, "b1", true)).To(MatchError(plugins.ErrNotFound))
		Expect(svc.SetEnabled(ctx, alice, "missing", true)).To(MatchError(plugins.ErrNotFound))
		Expect(svc.SetEnabled(ctx, plugins.Actor{}, "a1", true)).To(MatchError(plugins.ErrForbidden))
	})

	It("turns on an install that gained code only through the explicit re-enable", func() {
		Expect(svc.SetEnabled(ctx, alice, "a1", true)).To(Succeed())
		Expect(st.MarkNeedsReenable(ctx, "a1")).To(Succeed())
		Expect(active(alice)).To(BeEmpty(), "an install that waits for a re-enable contributes nothing")
		Expect(svc.SetEnabled(ctx, alice, "a1", true)).To(MatchError(plugins.ErrNeedsReenable))
		Expect(active(alice)).To(BeEmpty())
		Expect(svc.Reenable(ctx, alice, "a1")).To(Succeed())
		Expect(active(alice)).To(Equal([]string{"a1"}))
		us, _ := st.UserState(ctx, "alice", "a1")
		Expect(us.NeedsReenable).To(BeFalse())
	})
})
