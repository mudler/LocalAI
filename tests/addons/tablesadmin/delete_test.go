// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("deleting a database is explicit, destructive and UI-only", func() {
	It("deletes the data, the grants and the pending proposals only on the owner's typed confirmation and keeps the add-on installed", func() {
		h := ready(false)
		res := h.database("bob", "d-1", "Doomed")
		Expect(h.grant("bob", res, "carol", "viewer").code).To(Equal(200))
		// A pending change of the owner, waiting for a human decision.
		propose := fmt.Sprintf(`{"addonId":"tables","resourceId":%q,"operationId":"d-p1","generation":1,"action":"schema","arguments":{"tool":"tables-create","table":"T"}}`, res)
		made := h.send("POST", "/api/addons/proposals", propose, h.bearer("bob"))
		Expect(made.code).To(Equal(200), made.raw)
		Expect(made.body["state"]).To(Equal("pending"))
		rev := h.revision("bob", res)

		// Nobody but the owner's own session deletes: not an administrator,
		// not a grantee, not an API key, and not without the confirmation.
		for user, got := range map[string]reply{
			"alice": h.remove("alice", res, "d-a", "Doomed", rev),
			"carol": h.remove("carol", res, "d-c", "Doomed", rev),
		} {
			Expect(got.code).To(Equal(http.StatusForbidden), user+got.raw)
		}
		body := `{"operationId":"d-k","generation":1,"revision":{"schema":1,"data":0},"confirm":"Doomed"}`
		Expect(h.send("POST", "/api/addons/resources/"+res+"/delete", body, h.bearer("bob")).code).To(Equal(http.StatusForbidden))
		Expect(h.send("POST", "/api/addons/resources/"+res+"/delete", body, h.session("bob")).code).To(Equal(http.StatusForbidden))
		wrong := h.remove("bob", res, "d-w", "doomed", rev)
		Expect(wrong.code).To(Equal(http.StatusBadRequest), wrong.raw)
		Expect(wrong.errType()).To(Equal("invalid_request"))
		for _, confirm := range []string{"", "Doomed ", res} {
			got := h.remove("bob", res, "d-w", confirm, rev)
			Expect(got.code).To(Equal(http.StatusBadRequest), confirm)
		}
		// Nothing happened: the database, its file and its grants are intact.
		Expect(resourcesOf(h.list("bob"))).To(HaveKey(res))
		Expect(h.dbFile(res)).To(BeARegularFile())
		name, e := h.name("carol", res)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Doomed"))

		done := h.remove("bob", res, "d-ok", "Doomed", rev)
		Expect(done.code).To(Equal(http.StatusOK), done.raw)
		Expect(done.body["deleted"]).To(Equal(true))
		Expect(done.body["resourceId"]).To(Equal(res))
		// The data is gone, the record and the grants are gone, and nothing
		// of it is reachable by anyone.
		_, statErr := os.Stat(h.dbFile(res))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		for _, user := range []string{"bob", "carol", "alice"} {
			Expect(resourcesOf(h.list(user))).NotTo(HaveKey(res), user)
			_, e := h.name(user, res)
			Expect(e).To(HaveOccurred(), user)
		}
		Expect(h.app.AddonPolicy().Deleting()).To(BeEmpty())
		_, known := h.app.AddonPolicy().Key(res)
		Expect(known).To(BeFalse())
		// The pending proposal can never run.
		card := h.send("GET", "/api/addons/proposals/d-p1", "", h.bearer("bob"))
		Expect(card.body["state"]).To(Equal("cancelled"))
		// Deleting is not an uninstall: the add-on stays installed and
		// running and other databases still work.
		h.running()
		other := h.database("bob", "d-2", "Survivor")
		name, _ = h.name("bob", other)
		Expect(name).To(Equal("Survivor"))
		// The same call again, after its answer was lost, is the same success
		// for the owner who asked, and the receipt lookup says committed. Any
		// other operation, user or database is the denial of an unknown one.
		again := h.remove("bob", res, "d-ok", "Doomed", rev)
		Expect(again.code).To(Equal(http.StatusOK), again.raw)
		Expect(again.body["deleted"]).To(Equal(true))
		status := h.send("GET", "/api/addons/resources/"+res+"/operations/d-ok?kind=delete", "", h.tab("bob", res))
		Expect(status.code).To(Equal(200), status.raw)
		Expect(status.body["state"]).To(Equal("committed"))
		Expect(h.remove("bob", res, "d-other", "Doomed", rev).code).To(Equal(http.StatusForbidden))
		Expect(h.remove("carol", res, "d-ok", "Doomed", rev).code).To(Equal(http.StatusForbidden))
		Expect(h.remove("alice", res, "d-ok", "Doomed", rev).code).To(Equal(http.StatusForbidden))
		Expect(h.send("GET", "/api/addons/resources/"+res+"/operations/d-ok?kind=delete", "", h.tab("carol", res)).code).To(Equal(http.StatusForbidden))
		Expect(h.send("GET", "/api/addons/resources/"+res+"/operations/d-ok?kind=delete", "", h.tab("alice", res)).code).To(Equal(http.StatusForbidden))
	})

	It("stops and waits for admitted work of every user before it asks the add-on to delete", func() {
		h := ready(false)
		res := h.database("bob", "w-1", "Busy")
		Expect(h.grant("bob", res, "carol", "editor").code).To(Equal(200))
		p := h.app.AddonPolicy()
		started, ended := make(chan struct{}), make(chan struct{})
		go func() {
			defer GinkgoRecover()
			_ = p.Admit(context.Background(), h.actor("carol"), h.key(res), policy.ActionWrite, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				close(ended)
				return ctx.Err()
			})
		}()
		Eventually(started).Should(BeClosed())
		done := h.remove("bob", res, "w-del", "Busy", h.revision("bob", res))
		Expect(done.code).To(Equal(http.StatusOK), done.raw)
		// The admitted work had ended when the deletion was acknowledged.
		Expect(ended).To(BeClosed())
		Expect(h.dbFile(res)).NotTo(BeAnExistingFile())
	})

	It("makes the add-on refuse its delete tool under every action but the host's delete action", func() {
		h := ready(false)
		res := h.database("bob", "a-1", "Held")
		ctx := context.Background()
		inst, e := h.app.AddonManager().Runtime(ctx, "tables")
		Expect(e).NotTo(HaveOccurred())
		envelope := func(action, scope, op string) contract.Envelope {
			return contract.Envelope{
				Request:   contract.Request{AddonID: "tables", ResourceID: res, OperationID: op, Generation: inst.Generation, Action: action, Arguments: json.RawMessage(`{"tool":"databases-delete"}`)},
				Authority: contract.Authority{Actor: h.actor("bob"), Scope: scope, ExpiresAt: time.Now().Add(time.Minute)},
			}
		}
		for i, action := range []string{"read", "write", "schema", "create", "output"} {
			op := fmt.Sprintf("a-op-%d", i)
			_, e = inst.Client.Preview(ctx, envelope(action, "preview", op))
			Expect(e).To(HaveOccurred(), action)
			_, e = inst.Client.Apply(ctx, envelope(action, "apply", op), contract.Revision{Schema: 1})
			Expect(e).To(HaveOccurred(), action)
		}
		_, e = inst.Client.Read(ctx, envelope("read", "read", "a-op-read"))
		Expect(e).To(HaveOccurred())
		Expect(h.dbFile(res)).To(BeARegularFile())
		// Under the delete action the same envelope previews and changes nothing.
		prev, e := inst.Client.Preview(ctx, envelope("delete", "preview", "a-op-del"))
		Expect(e).NotTo(HaveOccurred())
		Expect(prev.Revision.Schema).NotTo(BeZero())
		// An apply for a revision the database no longer has is a rejected
		// receipt, not a deletion.
		_, e = inst.Client.Apply(ctx, envelope("delete", "apply", "a-op-del"), contract.Revision{Schema: 99})
		Expect(e).NotTo(HaveOccurred(), "a moved revision is a rejected receipt, not a deletion")
		Expect(h.dbFile(res)).To(BeARegularFile())
		name, e := h.name("bob", res)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Held"))
	})

	It("never offers the delete tool to a model, a proposal, a typed read or an agent", func() {
		h := ready(false)
		res := h.database("bob", "t-1", "Guarded")
		ctx := context.Background()
		adapter := tools.NewToolAdapter(h.app.AddonGateway())

		defs, e := adapter.Discover(ctx, h.actor("bob"))
		Expect(e).NotTo(HaveOccurred())
		Expect(defs).NotTo(BeEmpty())
		for _, d := range defs {
			Expect(d.Tool).NotTo(Equal("databases-delete"))
		}
		_, e = adapter.Execute(ctx, h.actor("bob"), "addon__tables__databases-delete", json.RawMessage(fmt.Sprintf(`{"resourceId":%q}`, res)))
		Expect(e).To(MatchError(tools.ErrUnknownTool))
		// A typed read, a proposal of any action and a dispatch that names the
		// admin tool are denied by the declared-tool rule, as the owner.
		read := contract.Request{AddonID: "tables", ResourceID: res, OperationID: "t-r", Generation: 1, Action: "read", Arguments: json.RawMessage(`{"tool":"databases-delete"}`)}
		_, e = h.app.AddonGateway().Dispatch(ctx, h.actor("bob"), read)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
		for i, action := range []string{"write", "schema", "delete", "create"} {
			req := contract.Request{AddonID: "tables", ResourceID: res, OperationID: fmt.Sprintf("t-p%d", i), Generation: 1, Action: action, Arguments: json.RawMessage(`{"tool":"databases-delete"}`)}
			if action == "create" {
				req.ResourceID = ""
			}
			_, e = h.app.AddonGateway().Propose(ctx, h.actor("bob"), req)
			Expect(e).To(HaveOccurred(), action)
			Expect(gateway.StatusFor(e)).To(Equal(403), action)
		}
		// The HTTP routes say the same.
		for _, action := range []string{"read", "write", "schema", "delete"} {
			b := fmt.Sprintf(`{"addonId":"tables","resourceId":%q,"operationId":"t-h-%s","generation":1,"action":%q,"arguments":{"tool":"databases-delete"}}`, res, action, action)
			Expect(h.send("POST", "/api/addons/proposals", b, h.bearer("bob")).code).To(Equal(403), action)
			Expect(h.send("POST", "/api/addons/dispatch", b, h.bearer("bob")).code).To(BeElementOf(403), action)
		}
		// The Apps view lists the model tools and not the admin tool.
		apps := h.send("GET", "/api/addons/apps", "", h.bearer("bob"))
		Expect(apps.raw).NotTo(ContainSubstring("databases-delete"))
		// Nothing was deleted.
		Expect(h.dbFile(res)).To(BeARegularFile())
		Expect(resourcesOf(h.list("bob"))).To(HaveKey(res))
	})

	It("denies a stale tab: the generation and the revision the owner saw decide, and nothing is removed", func() {
		h := ready(false)
		res := h.database("bob", "s-1", "Tabbed")
		seen := h.revision("bob", res)
		// A change of the schema through the usual proposal path moves the
		// revision the old tab saw.
		propose := fmt.Sprintf(`{"addonId":"tables","resourceId":%q,"operationId":"s-p1","generation":1,"action":"schema","arguments":{"tool":"tables-create","table":"Moves"}}`, res)
		made := h.send("POST", "/api/addons/proposals", propose, h.bearer("bob"))
		Expect(made.code).To(Equal(200), made.raw)
		ok := h.send("POST", "/api/addons/proposals/s-p1/confirm", fmt.Sprintf(`{"digest":%q}`, made.body["digest"]), h.tab("bob", "s-p1"))
		Expect(ok.code).To(Equal(200), ok.raw)
		Expect(h.revision("bob", res)).NotTo(Equal(seen))

		got := h.remove("bob", res, "s-d1", "Tabbed", seen)
		Expect(got.code).To(Equal(http.StatusConflict), got.raw)
		Expect(got.errType()).To(Equal("stale"))
		got = h.rename("bob", res, "s-r1", "Renamed", seen)
		Expect(got.code).To(Equal(http.StatusConflict), got.raw)
		Expect(got.errType()).To(Equal("stale"))
		// A generation other than the installed one is stale too.
		b, _ := json.Marshal(map[string]any{"operationId": "s-d2", "generation": 7, "revision": h.revision("bob", res), "confirm": "Tabbed"})
		got = h.send("POST", "/api/addons/resources/"+res+"/delete", string(b), h.tab("bob", res))
		Expect(got.code).To(Equal(http.StatusConflict), got.raw)
		Expect(got.errType()).To(Equal("stale"))
		// The resource was returned to use at once.
		Expect(h.dbFile(res)).To(BeARegularFile())
		name, e := h.name("bob", res)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Tabbed"))
		Expect(h.app.AddonPolicy().Deleting()).To(BeEmpty())
		// With the current view it works.
		Expect(h.remove("bob", res, "s-d3", "Tabbed", h.revision("bob", res)).code).To(Equal(200))
	})

	It("finishes or undoes a deletion that a crash interrupted at every step", func() {
		h := ready(false)
		ctx := context.Background()
		p := h.app.AddonPolicy()
		bob := h.actor("bob")
		confirmed := h.database("bob", "k-1", "Confirmed")
		unconfirmed := h.database("bob", "k-2", "Unconfirmed")
		gone := h.database("bob", "k-3", "AlreadyGone")
		kept := h.database("bob", "k-4", "Untouched")
		Expect(h.grant("bob", confirmed, "carol", "editor").code).To(Equal(200))

		// A crash after the confirmation and before the add-on deleted.
		Expect(p.BeginDelete(ctx, bob, h.key(confirmed), "k-op1")).To(Succeed())
		Expect(p.ConfirmDelete(h.key(confirmed))).To(Succeed())
		// A crash while the owner was still checking its preconditions.
		Expect(p.BeginDelete(ctx, bob, h.key(unconfirmed), "k-op2")).To(Succeed())
		// A crash after the add-on removed the data and before the record went.
		Expect(p.BeginDelete(ctx, bob, h.key(gone), "k-op3")).To(Succeed())
		Expect(p.ConfirmDelete(h.key(gone))).To(Succeed())
		Expect(os.Remove(h.dbFile(gone))).To(Succeed())

		// While a deletion is in progress the database is inaccessible to
		// everyone, owner included, and nothing new is admitted.
		for _, res := range []string{confirmed, unconfirmed, gone} {
			Expect(resourcesOf(h.list("bob"))).NotTo(HaveKey(res))
			_, e := h.name("bob", res)
			Expect(e).To(HaveOccurred())
			Expect(h.grant("bob", res, "dave", "viewer").code).To(Equal(http.StatusForbidden))
		}
		Expect(p.Deleting()).To(HaveLen(3))
		Expect(resourcesOf(h.list("bob"))).To(HaveKey(kept))
		// The owner's receipt lookup says the deletion is still pending.
		pending := h.send("GET", "/api/addons/resources/"+confirmed+"/operations/k-op1?kind=delete", "", h.tab("bob", confirmed))
		Expect(pending.code).To(Equal(200), pending.raw)
		Expect(pending.body["state"]).To(Equal("pending"))
		Expect(h.send("GET", "/api/addons/resources/"+confirmed+"/operations/k-op1?kind=delete", "", h.tab("carol", confirmed)).code).To(Equal(http.StatusForbidden))

		h.stop()
		h.start()
		h.running()

		// The confirmed deletion was finished, the unconfirmed one undone.
		Expect(h.app.AddonPolicy().Deleting()).To(BeEmpty())
		Expect(h.dbFile(confirmed)).NotTo(BeAnExistingFile())
		listed := resourcesOf(h.list("bob"))
		Expect(listed).NotTo(HaveKey(confirmed))
		Expect(listed).NotTo(HaveKey(gone))
		Expect(listed).To(HaveKey(unconfirmed))
		Expect(listed).To(HaveKey(kept))
		name, e := h.name("bob", unconfirmed)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Unconfirmed"))
		Expect(resourcesOf(h.list("carol"))).NotTo(HaveKey(confirmed))
		_, known := h.app.AddonPolicy().Key(confirmed)
		Expect(known).To(BeFalse())
		_, known = h.app.AddonPolicy().Key(gone)
		Expect(known).To(BeFalse())
		// What stays is a tombstone for the owner, and it survived the restart.
		for res, op := range map[string]string{confirmed: "k-op1", gone: "k-op3"} {
			done, e := h.app.AddonPolicy().DeletedBy(ctx, bob, res, op)
			Expect(e).NotTo(HaveOccurred())
			Expect(done).To(BeTrue(), res)
			status := h.send("GET", "/api/addons/resources/"+res+"/operations/"+op+"?kind=delete", "", h.tab("bob", res))
			Expect(status.code).To(Equal(200), status.raw)
			Expect(status.body["state"]).To(Equal("committed"))
		}
		done, e := h.app.AddonPolicy().DeletedBy(ctx, h.actor("carol"), confirmed, "k-op1")
		Expect(e).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		done, e = h.app.AddonPolicy().DeletedBy(ctx, bob, unconfirmed, "k-op2")
		Expect(e).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "an undone deletion leaves no tombstone")
	})
})
