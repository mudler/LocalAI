// SPDX-License-Identifier: MIT
package tables_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tables databases under the real owner", func() {
	ctx := context.Background()
	alice, bob := user("alice"), user("bob")

	It("starts the real executable through the authenticated handshake and refuses unauthenticated callers", func() {
		r := newRig()
		inst := r.start(1)
		regs, e := r.sup.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(regs).To(HaveLen(1))
		Expect(regs[0].AddonID).To(Equal("tables"))
		Expect(regs[0].Capabilities.Tools).To(ConsistOf("resource-exists", "databases-create", "databases-rename", "databases-inspect", "tables-create", "tables-rename", "tables-drop", "columns-add", "columns-rename", "columns-drop", "rows-query", "rows-aggregate", "rows-export", "rows-insert", "rows-update", "rows-delete"))
		// The package declares the native pages: the entry, the style sheet, the
		// one page and the chat action that selects a database.
		ui := regs[0].Capabilities.UI
		Expect(ui).NotTo(BeNil())
		Expect(ui.Entry).To(Equal("ui/tables.js"))
		Expect(ui.CSS).To(Equal([]string{"ui/tables.css"}))
		Expect(ui.Pages).To(Equal([]string{"databases"}))
		Expect(ui.ChatActions).To(HaveLen(1))
		Expect(ui.ChatActions[0].ID).To(Equal("select-database"))
		// The data directory the owner created is private and holds the addon's data.
		fi, e := os.Stat(filepath.Join(r.dataRoot, "tables"))
		Expect(e).NotTo(HaveOccurred())
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		// Anyone who reaches the loopback port without the credential is denied.
		for _, h := range []map[string]string{
			{},
			{"X-LocalAI-Incarnation": regs[0].Incarnation},
			{"X-LocalAI-Incarnation": "stale", "Authorization": "Bearer guess"},
		} {
			for _, path := range []string{"/health", "/read", "/apply", "/receipt"} {
				req, _ := http.NewRequest("POST", regs[0].Endpoint+path, bytes.NewReader([]byte(`{}`)))
				req.Header.Set("Content-Type", "application/json")
				for k, v := range h {
					req.Header.Set(k, v)
				}
				resp, e := http.DefaultClient.Do(req)
				Expect(e).NotTo(HaveOccurred())
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized), path)
			}
		}
		_ = inst
	})

	It("creates a database under the host-assigned ID and lists it only for people who may read it", func() {
		r := newRig()
		inst := r.start(1)
		key, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		Expect(key.AddonID).To(Equal("tables"))
		fi, e := os.Stat(r.dbFile(key.ResourceID))
		Expect(e).NotTo(HaveOccurred())
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		Expect(r.policy.Authorize(ctx, alice, key, policy.ActionSchema)).To(Succeed())
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		mine, e := r.policy.Accessible(ctx, alice, "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(mine).To(Equal([]policy.ResourceKey{key}))
		theirs, e := r.policy.Accessible(ctx, bob, "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(theirs).To(BeEmpty())
		res, e := r.read(inst, alice, key.ResourceID, "databases-inspect")
		Expect(e).NotTo(HaveOccurred())
		Expect(schemaName(res)).To(Equal("Sales"))
		// Sharing is the host's decision and widens the listing.
		Expect(r.policy.SetGrant(ctx, alice, key, "bob", policy.RoleViewer)).To(Succeed())
		theirs, _ = r.policy.Accessible(ctx, bob, "tables")
		Expect(theirs).To(Equal([]policy.ResourceKey{key}))
	})

	It("drops the reservation when the addon refuses the name, and leaves no database", func() {
		r := newRig()
		inst := r.start(1)
		_, e := r.create(inst, alice, "   ")
		Expect(e).To(HaveOccurred())
		Expect(e).To(MatchError(policy.ErrNotCreated))
		got, _ := r.policy.Accessible(ctx, alice, "tables")
		Expect(got).To(BeEmpty())
		Expect(r.reservations()).To(BeEmpty())
		Expect(filesIn(filepath.Join(r.dataRoot, "tables", "tables", "databases"))).To(BeEmpty())
	})

	It("drops a reservation at reconcile when the crash came before the database was created", func() {
		r := newRig()
		r.start(1)
		key, e := r.policy.Register(ctx, alice, "tables")
		Expect(e).NotTo(HaveOccurred())
		// Pending is invisible to its own owner.
		Expect(r.policy.Authorize(ctx, alice, key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		r.closePolicy()
		r.openPolicy()
		Expect(r.policy.Reconcile(ctx, r.backing())).To(Succeed())
		Expect(r.policy.Authorize(ctx, alice, key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		Expect(r.reservations()).To(BeEmpty())
		got, _ := r.policy.Accessible(ctx, alice, "tables")
		Expect(got).To(BeEmpty())
	})

	It("commits a reservation at reconcile when the crash came after the database was created", func() {
		r := newRig()
		inst := r.start(1)
		actor := alice
		key, e := r.policy.Register(ctx, actor, "tables")
		Expect(e).NotTo(HaveOccurred())
		// The database is created; the owner dies before it commits.
		Expect(policy.RuntimeCreate(r, r, contract.DefaultLimits(), actor, args(map[string]any{"tool": "databases-create", "name": "Recovered"}))(ctx, key)).To(Succeed())
		Expect(r.policy.Authorize(ctx, actor, key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		r.closePolicy()
		r.openPolicy()
		Expect(r.policy.Reconcile(ctx, r.backing())).To(Succeed())
		Expect(r.policy.Authorize(ctx, actor, key, policy.ActionSchema)).To(Succeed())
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		res, e := r.read(inst, actor, key.ResourceID, "databases-inspect")
		Expect(e).NotTo(HaveOccurred())
		Expect(schemaName(res)).To(Equal("Recovered"))
	})

	It("never exposes a database that has no host record", func() {
		r := newRig()
		inst := r.start(1)
		// The addon holds a database for an ID the host never recorded, as after
		// a lost policy store: nobody can resolve or list it.
		const orphan = "rffffffffffffffffffffffffffffffff"
		a := args(map[string]any{"tool": "databases-create", "name": "Orphan"})
		p, e := inst.Client.Preview(ctx, r.envelope(inst, alice, "preview", "create-"+orphan, orphan, a))
		Expect(e).NotTo(HaveOccurred())
		_, e = inst.Client.Apply(ctx, r.envelope(inst, alice, "apply", "create-"+orphan, orphan, a), p.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.dbFile(orphan)).To(BeAnExistingFile())
		for _, who := range []contract.Actor{alice, bob, user("admin")} {
			_, e = r.policy.Resolve(ctx, who, contract.Request{AddonID: "tables", ResourceID: orphan})
			Expect(e).To(MatchError(policy.ErrForbidden))
			got, _ := r.policy.Accessible(ctx, who, "tables")
			Expect(got).To(BeEmpty())
		}
		Expect(r.policy.Reconcile(ctx, r.backing())).To(Succeed())
		_, e = r.policy.Resolve(ctx, alice, contract.Request{AddonID: "tables", ResourceID: orphan})
		Expect(e).To(MatchError(policy.ErrForbidden))
	})

	It("renames one database by operation, replays safely, and leaves every other database alone", func() {
		r := newRig()
		inst := r.start(1)
		a, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		b, e := r.create(inst, bob, "Costs")
		Expect(e).NotTo(HaveOccurred())
		before, _ := os.ReadFile(r.dbFile(b.ResourceID))

		rec, e := r.rename(inst, alice, a.ResourceID, "Revenue", "rename-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		again, e := r.rename(inst, alice, a.ResourceID, "Revenue", "rename-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(contract.ReceiptCommitted))
		Expect(again.OperationID).To(Equal("rename-1"))
		got, e := inst.Client.Receipt(ctx, "rename-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(contract.ReceiptCommitted))

		res, _ := r.read(inst, alice, a.ResourceID, "databases-inspect")
		Expect(schemaName(res)).To(Equal("Revenue"))
		res, _ = r.read(inst, bob, b.ResourceID, "databases-inspect")
		Expect(schemaName(res)).To(Equal("Costs"))
		after, _ := os.ReadFile(r.dbFile(b.ResourceID))
		Expect(after).To(Equal(before))
		// The rename is not portable: the same operation on another database is rejected.
		other, e := r.rename(inst, alice, b.ResourceID, "Revenue", "rename-1")
		if e == nil {
			Expect(other.State).To(Equal(contract.ReceiptRejected))
		}
		res, _ = r.read(inst, bob, b.ResourceID, "databases-inspect")
		Expect(schemaName(res)).To(Equal("Costs"))
		// Host policy: an editor may not change the schema, so no rename is sent.
		Expect(r.policy.SetGrant(ctx, bob, b, "alice", policy.RoleEditor)).To(Succeed())
		Expect(r.policy.Authorize(ctx, alice, b, policy.ActionSchema)).To(MatchError(policy.ErrForbidden))
		Expect(r.policy.Authorize(ctx, alice, b, policy.ActionWrite)).To(Succeed())
	})

	It("rejects an undeclared tool, a forged addon binding and an unknown resource", func() {
		r := newRig()
		inst := r.start(1)
		key, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.read(inst, alice, key.ResourceID, "databases-drop")
		Expect(e).To(HaveOccurred())
		forged := r.envelope(inst, alice, "read", "forged-1", key.ResourceID, args(map[string]any{"tool": "databases-inspect"}))
		forged.Request.AddonID = "other"
		_, e = inst.Client.Read(ctx, forged)
		Expect(e).To(HaveOccurred())
		forged = r.envelope(inst, alice, "read", "forged-2", key.ResourceID, args(map[string]any{"tool": "databases-inspect"}))
		forged.Request.Generation = 99
		_, e = inst.Client.Read(ctx, forged)
		Expect(e).To(HaveOccurred())
		for _, bad := range []string{"../etc/passwd", "r00000000000000000000000000000000"} {
			_, e = r.read(inst, alice, bad, "databases-inspect")
			Expect(e).To(HaveOccurred(), bad)
		}
		Expect(filesIn(filepath.Join(r.dataRoot, "tables", "tables", "databases"))).To(Equal([]string{key.ResourceID + ".db"}))
	})

	It("keeps databases and operation receipts across an addon restart", func() {
		r := newRig()
		inst := r.start(1)
		key, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.rename(inst, alice, key.ResourceID, "Renamed", "rename-keep")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.sup.Stop(ctx, inst)).To(Succeed())
		inst = r.start(2)
		res, e := r.read(inst, alice, key.ResourceID, "databases-inspect")
		Expect(e).NotTo(HaveOccurred())
		Expect(schemaName(res)).To(Equal("Renamed"))
		rec, e := inst.Client.Receipt(ctx, "rename-keep")
		Expect(e).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		replay, e := r.rename(inst, alice, key.ResourceID, "Renamed", "rename-keep")
		Expect(e).NotTo(HaveOccurred())
		Expect(replay.State).To(Equal(contract.ReceiptCommitted))
		Expect(filesIn(filepath.Join(r.dataRoot, "tables", "tables", "databases"))).To(HaveLen(1))
	})

	It("fails closed when the addon cannot answer, and drops only for an addon that is not installed", func() {
		r := newRig()
		inst := r.start(1)
		key, e := r.policy.Register(ctx, alice, "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(policy.RuntimeCreate(r, r, contract.DefaultLimits(), alice, args(map[string]any{"tool": "databases-create", "name": "Kept"}))(ctx, key)).To(Succeed())
		Expect(r.sup.Stop(ctx, inst)).To(Succeed())
		// Installed but down: the record is kept pending, not dropped, and
		// the pass does not fail.
		rep, e := r.policy.ReconcileReport(ctx, r.backing())
		Expect(e).NotTo(HaveOccurred())
		Expect(rep).To(Equal(policy.ReconcileReport{Kept: 1}))
		Expect(r.reservations()).To(ConsistOf(key.ResourceID))
		// Gone for good: nothing can back it, so the record is dropped.
		r.installed = false
		Expect(r.policy.Reconcile(ctx, r.backing())).To(Succeed())
		Expect(r.reservations()).To(BeEmpty())
	})
})

func filesIn(dir string) []string {
	es, _ := os.ReadDir(dir)
	out := []string{}
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}
