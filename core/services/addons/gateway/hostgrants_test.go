// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type submitRecord struct {
	Cmd      addons.LifecycleCommand
	Approval HostApproval
}

// fakeAccess stands in for the manager: the approval itself runs inside it.
type fakeAccess struct {
	mu        sync.Mutex
	installed []InstalledAccess
	release   ReleaseAccess
	submitted []submitRecord
	submitErr error
}

func (f *fakeAccess) HostAccess(context.Context) ([]InstalledAccess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]InstalledAccess(nil), f.installed...), nil
}

func (f *fakeAccess) PreviewHostAccess(context.Context, string, string, string) (ReleaseAccess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.release, nil
}

func (f *fakeAccess) SubmitWithApproval(_ context.Context, _ contract.Actor, cmd addons.LifecycleCommand, ap HostApproval) (addons.OperationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, submitRecord{cmd, ap})
	if f.submitErr != nil {
		return addons.OperationStatus{}, f.submitErr
	}
	return addons.OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: "completed"}, nil
}

func call(op, opID, addon string, body any) HostGrantsCall {
	raw, _ := json.Marshal(body)
	return HostGrantsCall{Op: op, OperationID: opID, AddonID: addon, Body: raw}
}

type hgFixture struct {
	svc   *HostGrantsOwner
	store *hostgrants.Store
	mgr   *fakeAccess
	cfg   HostGrantsConfig
}

func newHGFixture(mutate ...func(*HostGrantsConfig)) *hgFixture {
	store, err := hostgrants.Open(filepath.Join(GinkgoT().TempDir(), "hostgrants.json"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(store.Close)
	mgr := &fakeAccess{
		installed: []InstalledAccess{
			{AddonID: "sandbox", Title: "Sandbox", Version: "1.0.0", State: "running", Identity: "id-sbx", Generation: 1, OperationID: "op-install",
				Required: []string{"workspace.materialize"}, Optional: []string{"egress.check", "connector.inbound"}},
			{AddonID: "web", Title: "Web", Version: "2.0.0", State: "running", Identity: "id-web", Generation: 1, OperationID: "op-web", Required: []string{"egress.check"}},
		},
		release: ReleaseAccess{AddonID: "newone", Title: "New one", Version: "1.0.0", Digest: "sha256:aa", Official: true,
			Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}},
	}
	cfg := HostGrantsConfig{
		Store: store, Manager: mgr,
		Floor: hostgrants.Floor{Grants: map[string][]string{"web": {"egress."}}, Deny: map[string][]string{"*": {"connector."}}},
		Admin: func(context.Context, contract.Actor) error { return nil },
		Describe: func(op string) (HostOpDescription, bool) {
			return HostOpDescription{Title: "T " + op, Text: "text", Risk: "read"}, true
		},
	}
	for _, m := range mutate {
		m(&cfg)
	}
	svc, err := NewHostGrantsOwner(cfg)
	Expect(err).NotTo(HaveOccurred())
	return &hgFixture{svc: svc, store: store, mgr: mgr, cfg: cfg}
}

var human = WithHumanSurface(context.Background())
var admin = contract.Actor{UserID: "admin", CredentialID: "session:abc", Mode: "named-user"}

func data[T any](raw json.RawMessage, err error) T {
	GinkgoHelper()
	Expect(err).NotTo(HaveOccurred())
	var v T
	Expect(json.Unmarshal(raw, &v)).To(Succeed())
	return v
}

func typeOf(err error) string {
	var he *HostGrantsError
	if errors.As(err, &he) {
		return he.Type
	}
	return "other:" + err.Error()
}

var _ = Describe("The host access owner service", func() {
	read := func(f *hgFixture) HostGrantsView {
		return data[HostGrantsView](f.svc.Serve(context.Background(), admin, call(HostGrantsRead, "", "", nil)))
	}
	opOf := func(a HostGrantsAddon, name string) HostOpView {
		for _, o := range a.Ops {
			if o.Name == name {
				return o
			}
		}
		Fail("no op " + name)
		return HostOpView{}
	}
	addon := func(v HostGrantsView, id string) HostGrantsAddon {
		for _, a := range v.Addons {
			if a.ID == id {
				return a
			}
		}
		Fail("no add-on " + id)
		return HostGrantsAddon{}
	}

	It("reads the floor, the deny rules and the state of each add-on, with a file deny shown as blocked", func() {
		f := newHGFixture()
		v := read(f)
		Expect(v.Floor.Addons).To(Equal([]HostFloorAddon{{ID: "web", Prefixes: []string{"egress."}}}))
		Expect(v.Deny.File).To(HaveKeyWithValue("*", []string{"connector."}))
		web := addon(v, "web")
		Expect(opOf(web, "egress.check").Status).To(Equal("allowed"))
		Expect(opOf(web, "egress.check").Source).To(Equal("deployment"))
		sbx := addon(v, "sandbox")
		Expect(opOf(sbx, "workspace.materialize").Status).To(Equal("not_allowed"))
		Expect(opOf(sbx, "workspace.materialize").Required).To(BeTrue())
		Expect(opOf(sbx, "connector.inbound").Status).To(Equal("blocked"))
		Expect(opOf(sbx, "connector.inbound").Reason).To(Equal("denied_by_policy_file"))
		Expect(opOf(sbx, "workspace.materialize").Title).To(Equal("T workspace.materialize"))
		Expect(v.Blanket.Mode).To(Equal("off"))
	})

	It("refuses a non-administrator before it reads anything", func() {
		f := newHGFixture(func(c *HostGrantsConfig) {
			c.Admin = func(context.Context, contract.Actor) error { return ErrForbidden }
		})
		_, err := f.svc.Serve(context.Background(), admin, call(HostGrantsRead, "", "", nil))
		Expect(err).To(MatchError(ErrForbidden))
	})

	Describe("preview", func() {
		preview := func(f *hgFixture) HostGrantsPreviewView {
			return data[HostGrantsPreviewView](f.svc.Serve(context.Background(), admin, call(HostGrantsPreview, "", "", HostGrantsPreviewRequest{SourceID: "official", AddonID: "newone", Version: "1.0.0"})))
		}
		It("lists the verified request with its status and a nonce", func() {
			f := newHGFixture()
			v := preview(f)
			Expect(v.Digest).To(Equal("sha256:aa"))
			Expect(v.CanInstall).To(BeTrue())
			Expect(v.Nonce).NotTo(BeEmpty())
			Expect(v.Ops).To(HaveLen(2))
			Expect(v.Ops[1].Name).To(Equal("workspace.materialize"))
			Expect(v.Ops[1].Required).To(BeTrue())
			Expect(v.Ops[1].Status).To(Equal("not_allowed"))
			Expect(v.Ops[0].Status).To(Equal("not_allowed"))
			Expect(v.Blanket.Applies).To(BeFalse())
		})
		It("shows a deny winning over everything, and cannot install a blocked required operation", func() {
			f := newHGFixture(func(c *HostGrantsConfig) {
				c.Floor.Grants = map[string][]string{"newone": {"workspace."}}
			})
			_, err := f.store.Update(context.Background(), hostgrants.Patch{Deny: &map[string][]string{"newone": {"workspace.materialize"}}}, 0, "deny-1")
			Expect(err).NotTo(HaveOccurred())
			v := preview(f)
			Expect(v.CanInstall).To(BeFalse())
			Expect(v.BlockedReq).To(Equal([]string{"workspace.materialize"}))
			var got HostOpView
			for _, o := range v.Ops {
				if o.Name == "workspace.materialize" {
					got = o
				}
			}
			Expect(got.Status).To(Equal("blocked"), "the file grants it, the UI deny blocks it")
			Expect(got.Reason).To(Equal("denied_by_admin"))
		})
		It("marks what a deployment prefix holds, and an operation granted before an uninstall as a hint only", func() {
			f := newHGFixture(func(c *HostGrantsConfig) {
				c.Floor.Grants = map[string][]string{"newone": {"egress."}}
			})
			_, err := f.store.Update(context.Background(), hostgrants.Patch{Hints: map[string][]string{"newone": {"workspace.materialize"}}}, 0, "hint-1")
			Expect(err).NotTo(HaveOccurred())
			v := preview(f)
			for _, o := range v.Ops {
				switch o.Name {
				case "egress.check":
					Expect(o.Status).To(Equal("allowed"))
				case "workspace.materialize":
					Expect(o.Status).To(Equal("not_allowed"))
					Expect(o.Hint).To(BeTrue())
				}
			}
		})
		It("says when the blanket switch would grant on install", func() {
			f := newHGFixture()
			_, err := f.svc.Serve(human, admin, call(HostGrantsBlanket, "b1", "", HostGrantsBlanketRequest{OperationID: "b1", Mode: "official", Confirm: BlanketConfirmOfficial}))
			Expect(err).NotTo(HaveOccurred())
			Expect(preview(f).Blanket.Applies).To(BeTrue())
			f.mgr.release.Official = false
			Expect(preview(f).Blanket.Applies).To(BeFalse(), "an unofficial package is out of scope of official")
		})
	})

	Describe("install with approval", func() {
		req := func(v HostGrantsPreviewView, op string) HostGrantsInstallRequest {
			return HostGrantsInstallRequest{OperationID: op, Action: "install", SourceID: "official", AddonID: "newone", Version: "1.0.0",
				Approve: HostGrantsApprove{Digest: v.Digest, Ops: []string{"workspace.materialize"}, Revision: v.Revision, Nonce: v.Nonce}}
		}
		prev := func(f *hgFixture) HostGrantsPreviewView {
			return data[HostGrantsPreviewView](f.svc.Serve(context.Background(), admin, call(HostGrantsPreview, "", "", HostGrantsPreviewRequest{SourceID: "official", AddonID: "newone", Version: "1.0.0"})))
		}
		It("passes the approval to the manager and answers the operation", func() {
			f := newHGFixture()
			v := prev(f)
			st := data[addons.OperationStatus](f.svc.Serve(human, admin, call(HostGrantsInstall, "i1", "", req(v, "i1"))))
			Expect(st.OperationID).To(Equal("i1"))
			Expect(f.mgr.submitted).To(HaveLen(1))
			Expect(f.mgr.submitted[0].Approval).To(Equal(HostApproval{Digest: "sha256:aa", Ops: []string{"workspace.materialize"}, Revision: v.Revision, Nonce: v.Nonce}))
			Expect(f.mgr.submitted[0].Cmd.Action).To(Equal("install"))
		})
		It("refuses without the human surface and never reaches the manager", func() {
			f := newHGFixture()
			v := prev(f)
			_, err := f.svc.Serve(context.Background(), admin, call(HostGrantsInstall, "i1", "", req(v, "i1")))
			Expect(typeOf(err)).To(Equal("not_human_surface"))
			Expect(f.mgr.submitted).To(BeEmpty())
		})
		It("refuses a stale revision as 409 revision_conflict", func() {
			f := newHGFixture()
			v := prev(f)
			_, err := f.store.Update(context.Background(), hostgrants.Patch{Deny: &map[string][]string{}}, 0, "other")
			Expect(err).NotTo(HaveOccurred())
			_, err = f.svc.Serve(human, admin, call(HostGrantsInstall, "i1", "", req(v, "i1")))
			Expect(typeOf(err)).To(Equal("revision_conflict"))
			var he *HostGrantsError
			Expect(errors.As(err, &he)).To(BeTrue())
			Expect(he.Status).To(Equal(409))
			Expect(f.mgr.submitted).To(BeEmpty())
		})
		It("uses the nonce once, lets the same operation replay, and binds it to the credential and the digest", func() {
			f := newHGFixture()
			v := prev(f)
			r := req(v, "i1")
			_, err := f.svc.Serve(human, admin, call(HostGrantsInstall, "i1", "", r))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.svc.Serve(human, admin, call(HostGrantsInstall, "i1", "", r))
			Expect(err).NotTo(HaveOccurred(), "a replay of the same operation")
			other := r
			other.OperationID = "i2"
			_, err = f.svc.Serve(human, admin, call(HostGrantsInstall, "i2", "", other))
			Expect(typeOf(err)).To(Equal("approval_stale"), "another operation cannot reuse it")

			v2 := prev(f)
			key := admin
			key.CredentialID = "key:k1"
			_, err = f.svc.Serve(human, key, call(HostGrantsInstall, "i3", "", req(v2, "i3")))
			Expect(typeOf(err)).To(Equal("approval_stale"), "a nonce from a session is not good for another credential")
			wrongDigest := req(v2, "i4")
			wrongDigest.Approve.Digest = "sha256:bb"
			_, err = f.svc.Serve(human, admin, call(HostGrantsInstall, "i4", "", wrongDigest))
			Expect(typeOf(err)).To(Equal("approval_stale"))
			Expect(f.mgr.submitted).To(HaveLen(2), "install and its replay only")
		})
		It("expires a nonce", func() {
			now := time.Now()
			f := newHGFixture(func(c *HostGrantsConfig) { c.Now = func() time.Time { return now } })
			v := prev(f)
			now = now.Add(6 * time.Minute)
			_, err := f.svc.Serve(human, admin, call(HostGrantsInstall, "i1", "", req(v, "i1")))
			Expect(typeOf(err)).To(Equal("approval_stale"))
		})
		It("types the decisions of the manager", func() {
			for err, want := range map[error]string{ErrApprovalStale: "approval_stale", ErrBlockedByPolicy: "blocked_by_policy", ErrApprovalRequired: "approval_required"} {
				f := newHGFixture()
				f.mgr.submitErr = err
				v := prev(f)
				_, got := f.svc.Serve(human, admin, call(HostGrantsInstall, "i1", "", req(v, "i1")))
				Expect(typeOf(got)).To(Equal(want))
			}
		})
		It("rejects an upgrade with one pin, an unknown field and a body that names another operation", func() {
			f := newHGFixture()
			v := prev(f)
			r := req(v, "i1")
			r.Action, r.ExpectedDigest = "upgrade", "sha256:old"
			_, err := f.svc.Serve(human, admin, call(HostGrantsInstall, "i1", "", r))
			Expect(typeOf(err)).To(Equal("invalid_request"))
			_, err = f.svc.Serve(human, admin, HostGrantsCall{Op: HostGrantsInstall, OperationID: "i1", Body: json.RawMessage(`{"operationId":"i1","extra":1}`)})
			Expect(typeOf(err)).To(Equal("invalid_request"))
			_, err = f.svc.Serve(human, admin, call(HostGrantsInstall, "i9", "", req(v, "i1")))
			Expect(typeOf(err)).To(Equal("invalid_request"))
		})
	})

	Describe("edit, revoke", func() {
		edit := func(f *hgFixture, op string, rev uint64, ops ...string) (json.RawMessage, error) {
			return f.svc.Serve(human, admin, call(HostGrantsEdit, op, "sandbox", HostGrantsEditRequest{OperationID: op, Revision: rev, Ops: ops}))
		}
		It("grants a declared operation, shows it, and revokes it again", func() {
			f := newHGFixture()
			r := data[HostGrantsAddonResult](edit(f, "e1", 0, "workspace.materialize", "egress.check"))
			Expect(r.Revision).To(Equal(uint64(1)))
			Expect(opOf(*r.Addon, "workspace.materialize").Status).To(Equal("allowed"))
			Expect(opOf(*r.Addon, "workspace.materialize").Source).To(Equal("edit"))
			Expect(opOf(*r.Addon, "workspace.materialize").By).To(Equal("admin"))
			r = data[HostGrantsAddonResult](edit(f, "e2", 1, "egress.check"))
			Expect(opOf(*r.Addon, "workspace.materialize").Status).To(Equal("not_allowed"))
			Expect(opOf(*r.Addon, "egress.check").Status).To(Equal("allowed"))
			rv := data[HostGrantsRevision](f.svc.Serve(human, admin, call(HostGrantsRevoke, "r1", "sandbox", HostGrantsRevokeRequest{OperationID: "r1", Revision: 2})))
			Expect(rv).To(Equal(HostGrantsRevision{Revision: 3, Changed: true}))
			Expect(opOf(addon(read(f), "sandbox"), "egress.check").Status).To(Equal("not_allowed"))
		})
		It("refuses a stale revision with 409 and an operation replayed with another body with 409", func() {
			f := newHGFixture()
			_, err := edit(f, "e1", 0, "workspace.materialize")
			Expect(err).NotTo(HaveOccurred())
			_, err = edit(f, "e2", 0, "workspace.materialize")
			Expect(typeOf(err)).To(Equal("revision_conflict"))
			_, err = edit(f, "e1", 1, "egress.check")
			Expect(typeOf(err)).To(Equal("operation_conflict"))
			r := data[HostGrantsAddonResult](edit(f, "e1", 0, "workspace.materialize"))
			Expect(r.Revision).To(Equal(uint64(1)), "the same operation again is the same change")
		})
		It("refuses an operation the manifest does not declare or the host does not register, and one that is denied", func() {
			f := newHGFixture()
			_, err := edit(f, "e1", 0, "finetune.start")
			Expect(typeOf(err)).To(Equal("invalid_request"))
			_, err = edit(f, "e2", 0, "connector.inbound")
			Expect(typeOf(err)).To(Equal("blocked_by_policy"))
			_, err = f.svc.Serve(human, admin, call(HostGrantsEdit, "e3", "ghost", HostGrantsEditRequest{OperationID: "e3", Ops: []string{"egress.check"}}))
			Expect(typeOf(err)).To(Equal("not_found"))
		})
		It("does not grant an entry from a package with another identity", func() {
			f := newHGFixture()
			_, err := edit(f, "e1", 0, "workspace.materialize")
			Expect(err).NotTo(HaveOccurred())
			f.mgr.installed[0].Identity = "someone-else"
			v := read(f)
			sbx := addon(v, "sandbox")
			Expect(opOf(sbx, "workspace.materialize").Status).To(Equal("not_allowed"))
			Expect(sbx.IdentityMismatch).To(BeTrue())
		})
		It("lists a grant the manifest no longer asks for, and relabels blanket grants on request", func() {
			f := newHGFixture()
			_, err := edit(f, "e1", 0, "workspace.materialize", "egress.check")
			Expect(err).NotTo(HaveOccurred())
			f.mgr.installed[0].Optional = []string{"connector.inbound"}
			Expect(addon(read(f), "sandbox").NoLongerRequested).To(HaveLen(1))
			Expect(addon(read(f), "sandbox").NoLongerRequested[0].Name).To(Equal("egress.check"))
		})
		It("revoke of an unknown add-on is 404 and of one without grants changes nothing", func() {
			f := newHGFixture()
			_, err := f.svc.Serve(human, admin, call(HostGrantsRevoke, "r1", "ghost", HostGrantsRevokeRequest{OperationID: "r1"}))
			Expect(typeOf(err)).To(Equal("not_found"))
			rv := data[HostGrantsRevision](f.svc.Serve(human, admin, call(HostGrantsRevoke, "r2", "sandbox", HostGrantsRevokeRequest{OperationID: "r2"})))
			Expect(rv).To(Equal(HostGrantsRevision{}))
		})
		It("refuses every write without the human surface, and when the deployment locks the store", func() {
			f := newHGFixture()
			for _, c := range []HostGrantsCall{
				call(HostGrantsEdit, "x1", "sandbox", HostGrantsEditRequest{OperationID: "x1"}),
				call(HostGrantsRevoke, "x2", "sandbox", HostGrantsRevokeRequest{OperationID: "x2"}),
				call(HostGrantsBlanket, "x3", "", HostGrantsBlanketRequest{OperationID: "x3", Mode: "off"}),
				call(HostGrantsDeny, "x4", "", HostGrantsDenyRequest{OperationID: "x4"}),
				call(HostGrantsInstall, "x5", "", HostGrantsInstallRequest{OperationID: "x5"}),
			} {
				_, err := f.svc.Serve(context.Background(), admin, c)
				Expect(typeOf(err)).To(Equal("not_human_surface"), c.Op)
			}
			locked := newHGFixture(func(c *HostGrantsConfig) { c.Locked = true })
			_, err := locked.svc.Serve(human, admin, call(HostGrantsEdit, "x1", "sandbox", HostGrantsEditRequest{OperationID: "x1", Ops: []string{"egress.check"}}))
			Expect(typeOf(err)).To(Equal("locked_by_deployment"))
			Expect(read(locked).Locked).To(BeTrue(), "reads still work")
		})
	})

	Describe("blanket", func() {
		b := func(f *hgFixture, op string, rev uint64, r HostGrantsBlanketRequest) (json.RawMessage, error) {
			r.OperationID, r.Revision = op, rev
			return f.svc.Serve(human, admin, call(HostGrantsBlanket, op, "", r))
		}
		It("needs the typed text, stated for the scope", func() {
			f := newHGFixture()
			_, err := b(f, "b1", 0, HostGrantsBlanketRequest{Mode: "any", Confirm: BlanketConfirmOfficial})
			Expect(typeOf(err)).To(Equal("confirmation_mismatch"))
			_, err = b(f, "b2", 0, HostGrantsBlanketRequest{Mode: "official"})
			Expect(typeOf(err)).To(Equal("confirmation_mismatch"))
			r := data[HostGrantsBlanketResult](b(f, "b3", 0, HostGrantsBlanketRequest{Mode: "any", Confirm: BlanketConfirmAny}))
			Expect(r.Blanket.Mode).To(Equal("any"))
			Expect(r.Blanket.Revision).To(Equal(uint64(1)))
			Expect(BlanketConfirmAny).To(ContainSubstring("any library"))
		})
		It("grants the installed add-ons what they declare, never a denied one, and skips what is blocked", func() {
			f := newHGFixture()
			r := data[HostGrantsBlanketResult](b(f, "b1", 0, HostGrantsBlanketRequest{Mode: "any", IncludeOptional: true, ApplyToInstalled: true, Confirm: BlanketConfirmAny}))
			Expect(r.Changed).To(ConsistOf(HostBlanketChange{ID: "sandbox", Ops: []string{"egress.check", "workspace.materialize"}}))
			sbx := addon(read(f), "sandbox")
			Expect(opOf(sbx, "workspace.materialize").Source).To(Equal("blanket"))
			Expect(opOf(sbx, "connector.inbound").Status).To(Equal("blocked"), "a deny beats the blanket")
			Expect(sbx.BlanketGrantsCount).To(Equal(2))
		})
		It("covers only official add-ons in official mode", func() {
			f := newHGFixture()
			f.mgr.installed[0].Official = true
			r := data[HostGrantsBlanketResult](b(f, "b1", 0, HostGrantsBlanketRequest{Mode: "official", ApplyToInstalled: true, Confirm: BlanketConfirmOfficial}))
			Expect(r.Changed).To(ConsistOf(HostBlanketChange{ID: "sandbox", Ops: []string{"workspace.materialize"}}))
			Expect(r.Skipped).To(ContainElement(HostBlanketSkip{ID: "web", Reason: "out_of_scope"}))
		})
		It("keeps the grants it made when turned off, or revokes them all when asked", func() {
			f := newHGFixture()
			_, err := b(f, "b1", 0, HostGrantsBlanketRequest{Mode: "any", ApplyToInstalled: true, Confirm: BlanketConfirmAny})
			Expect(err).NotTo(HaveOccurred())
			r := data[HostGrantsBlanketResult](b(f, "b2", 1, HostGrantsBlanketRequest{Mode: "off"}))
			Expect(r.Revoked).To(BeEmpty())
			Expect(opOf(addon(read(f), "sandbox"), "workspace.materialize").Status).To(Equal("allowed"))
			r = data[HostGrantsBlanketResult](b(f, "b3", 2, HostGrantsBlanketRequest{Mode: "off", RevokeOnOff: true}))
			Expect(r.Revoked).To(Equal([]string{"sandbox"}))
			Expect(opOf(addon(read(f), "sandbox"), "workspace.materialize").Status).To(Equal("not_allowed"))
		})
		It("is refused when the deployment fixes the mode, and reads as set by the deployment", func() {
			f := newHGFixture(func(c *HostGrantsConfig) { c.BlanketOverride = "official" })
			_, err := b(f, "b1", 0, HostGrantsBlanketRequest{Mode: "off"})
			Expect(typeOf(err)).To(Equal("locked_by_deployment"))
			v := read(f)
			Expect(v.Blanket.Mode).To(Equal("official"))
			Expect(v.Blanket.SetBy).To(Equal("deployment"))
		})
		It("rejects options that do not fit the mode", func() {
			f := newHGFixture()
			_, err := b(f, "b1", 0, HostGrantsBlanketRequest{Mode: "off", IncludeOptional: true, ApplyToInstalled: true})
			Expect(err).NotTo(HaveOccurred(), "off ignores the scope options")
			_, err = b(f, "b4", 1, HostGrantsBlanketRequest{Mode: "official", RevokeOnOff: true, Confirm: BlanketConfirmOfficial})
			Expect(typeOf(err)).To(Equal("invalid_request"))
			_, err = b(f, "b2", 1, HostGrantsBlanketRequest{Mode: "sometimes"})
			Expect(typeOf(err)).To(Equal("invalid_request"))
		})
	})

	Describe("deny", func() {
		d := func(f *hgFixture, op string, rev uint64, rules map[string][]string, dry bool) (json.RawMessage, error) {
			kind := HostGrantsDeny
			id := op
			if dry {
				kind, id = HostGrantsDenyPreview, ""
			}
			return f.svc.Serve(human, admin, call(kind, id, "", HostGrantsDenyRequest{OperationID: id, Revision: rev, Deny: rules, DryRun: dry}))
		}
		It("shows what a rule takes before it applies and what makes an add-on stop", func() {
			f := newHGFixture()
			_, err := f.svc.Serve(human, admin, call(HostGrantsEdit, "e1", "sandbox", HostGrantsEditRequest{OperationID: "e1", Ops: []string{"workspace.materialize", "egress.check"}}))
			Expect(err).NotTo(HaveOccurred())
			dry := data[HostGrantsDenyResult](d(f, "", 1, map[string][]string{"sandbox": {"workspace."}, "*": {"egress."}}, true))
			Expect(dry.DryRun).To(BeTrue())
			Expect(dry.Affects).To(Equal([]HostDenyAffect{
				{ID: "sandbox", Loses: []string{"egress.check", "workspace.materialize"}, WillStop: true},
				{ID: "web", Loses: []string{"egress.check"}, WillStop: true},
			}))
			Expect(read(f).Deny.UI).To(BeEmpty(), "a dry run changes nothing")
			real := data[HostGrantsDenyResult](d(f, "d1", 1, map[string][]string{"sandbox": {"workspace."}}, false))
			Expect(real.Revision).To(Equal(uint64(2)))
			sbx := addon(read(f), "sandbox")
			Expect(opOf(sbx, "workspace.materialize").Status).To(Equal("blocked"))
			Expect(opOf(sbx, "workspace.materialize").Reason).To(Equal("denied_by_admin"))
		})
		It("refuses an invalid rule, a stale revision, and the dry run needs no human surface but a write does", func() {
			f := newHGFixture()
			_, err := d(f, "d1", 0, map[string][]string{"Bad ID": {"egress."}}, false)
			Expect(typeOf(err)).To(Equal("invalid_request"))
			_, err = d(f, "d2", 0, map[string][]string{"*": {"x", "*"}}, false)
			Expect(typeOf(err)).To(Equal("invalid_request"))
			_, err = d(f, "d3", 5, map[string][]string{}, false)
			Expect(typeOf(err)).To(Equal("revision_conflict"))
			_, err = f.svc.Serve(context.Background(), admin, call(HostGrantsDenyPreview, "", "", HostGrantsDenyRequest{Deny: map[string][]string{}, DryRun: true}))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.svc.Serve(human, admin, call(HostGrantsDeny, "d4", "", HostGrantsDenyRequest{OperationID: "d4", DryRun: true}))
			Expect(typeOf(err)).To(Equal("invalid_request"), "a dry run is its own verb")
		})
	})

	It("writes an audit line for each change, with the credential kind and never a request body", func() {
		path := filepath.Join(GinkgoT().TempDir(), "audit.jsonl")
		a, err := hostgrants.OpenAudit(path)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(a.Close)
		f := newHGFixture(func(c *HostGrantsConfig) { c.Audit = a })
		_, err = f.svc.Serve(human, admin, call(HostGrantsEdit, "e1", "sandbox", HostGrantsEditRequest{OperationID: "e1", Ops: []string{"egress.check"}}))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.svc.Serve(context.Background(), admin, call(HostGrantsEdit, "e2", "sandbox", HostGrantsEditRequest{OperationID: "e2"}))
		Expect(err).To(HaveOccurred())
		raw := readFile(path)
		Expect(raw).To(ContainSubstring(`"event":"grant"`))
		Expect(raw).To(ContainSubstring(`"credential":"session"`))
		Expect(raw).To(ContainSubstring(`"event":"refused"`))
		Expect(raw).To(ContainSubstring(`"reason":"not_human_surface"`))
	})
})

func readFile(p string) string {
	GinkgoHelper()
	b, err := os.ReadFile(p)
	Expect(err).NotTo(HaveOccurred())
	return string(b)
}

var _ = Describe("host access actions on the typed dispatch", func() {
	It("is not reachable: a dispatch of a host access action is forbidden on the owner", func() {
		g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
		for _, action := range []string{"hostgrants-edit", "hostgrants-install", "hostgrants-blanket", "hostgrants-read"} {
			_, err := g.Dispatch(human, admin, contract.Request{OperationID: "op", Action: action, Arguments: json.RawMessage(`{}`)})
			Expect(err).To(MatchError(ErrForbidden), action)
		}
	})
})

var _ = Describe("host access as the pages read it", func() {
	It("names what an upgrade adds, using the installed package to tell what is held", func() {
		f := newHGFixture()
		_, err := f.svc.Serve(human, admin, call(HostGrantsEdit, "e1", "sandbox", HostGrantsEditRequest{OperationID: "e1", Ops: []string{"workspace.materialize"}}))
		Expect(err).NotTo(HaveOccurred())
		f.mgr.release = ReleaseAccess{AddonID: "sandbox", Title: "Sandbox", Version: "1.1.0", Digest: "sha256:bb", Installed: true,
			Required: []string{"workspace.materialize", "workspace.export"}, Optional: []string{"egress.check"}}
		v := data[HostGrantsPreviewView](f.svc.Serve(context.Background(), admin, call(HostGrantsPreview, "", "", HostGrantsPreviewRequest{SourceID: "official", AddonID: "sandbox", Version: "1.1.0"})))
		Expect(v.Added).To(Equal([]string{"workspace.export"}))
		status := map[string]string{}
		for _, o := range v.Ops {
			status[o.Name] = o.Status
		}
		Expect(status).To(Equal(map[string]string{"workspace.materialize": "allowed", "workspace.export": "not_allowed", "egress.check": "not_allowed"}))
	})

	It("says why an add-on is stopped, and carries its library and new optional access", func() {
		f := newHGFixture()
		f.mgr.installed[0].SourceID = "official"
		f.mgr.installed[0].NewOptional = []string{"egress.check"}
		v := data[HostGrantsView](f.svc.Serve(context.Background(), admin, call(HostGrantsRead, "", "", nil)))
		var sbx HostGrantsAddon
		for _, a := range v.Addons {
			if a.ID == "sandbox" {
				sbx = a
			}
		}
		Expect(sbx.Stopped).To(Equal("workspace.materialize"))
		Expect(sbx.SourceID).To(Equal("official"))
		Expect(sbx.NewOptional).To(Equal([]string{"egress.check"}))
	})

	It("keeps a blanket grant as the administrator's own, so revoking the blanket grants leaves it", func() {
		f := newHGFixture()
		_, err := f.svc.Serve(human, admin, call(HostGrantsBlanket, "b1", "", HostGrantsBlanketRequest{OperationID: "b1", Mode: "any", ApplyToInstalled: true, Confirm: BlanketConfirmAny}))
		Expect(err).NotTo(HaveOccurred())
		r := data[HostGrantsAddonResult](f.svc.Serve(human, admin, call(HostGrantsEdit, "e1", "sandbox", HostGrantsEditRequest{OperationID: "e1", Revision: 1, Ops: []string{"workspace.materialize"}, Keep: []string{"workspace.materialize"}})))
		Expect(r.Addon.Ops[2].Name).To(Equal("workspace.materialize"))
		Expect(r.Addon.Ops[2].Source).To(Equal("edit"))
		_, err = f.svc.Serve(human, admin, call(HostGrantsBlanket, "b2", "", HostGrantsBlanketRequest{OperationID: "b2", Revision: 2, Mode: "off", RevokeOnOff: true}))
		Expect(err).NotTo(HaveOccurred())
		v := data[HostGrantsView](f.svc.Serve(context.Background(), admin, call(HostGrantsRead, "", "", nil)))
		for _, a := range v.Addons {
			if a.ID == "sandbox" {
				for _, o := range a.Ops {
					if o.Name == "workspace.materialize" {
						Expect(o.Status).To(Equal("allowed"))
					}
				}
			}
		}
	})
})
