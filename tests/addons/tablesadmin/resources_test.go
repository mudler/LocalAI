// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("UI-only resource management of the owner", func() {
	It("creates private databases by the person's own action, once per operation, and lists them only for their owner", func() {
		h := ready(false)

		// A key, an unmarked request and a tool marker are never the person.
		body := `{"addonId":"tables","operationId":"make-1","generation":1,"arguments":{"name":"Budget"}}`
		Expect(h.send("POST", "/api/addons/resources", body, h.bearer("bob")).code).To(Equal(http.StatusForbidden))
		Expect(h.send("POST", "/api/addons/resources", body, h.session("bob")).code).To(Equal(http.StatusForbidden))
		marked := func(r *http.Request) { h.tab("bob", "make-1")(r); r.Header.Set("X-LocalAI-Tool-Call", "1") }
		Expect(h.send("POST", "/api/addons/resources", body, marked).code).To(Equal(http.StatusForbidden))
		Expect(h.send("POST", "/api/addons/resources", body, h.tab("bob", "another-op")).code).To(Equal(http.StatusForbidden))
		Expect(resourcesOf(h.list("bob"))).To(BeEmpty())

		got := h.create("bob", "make-1", "Budget")
		Expect(got.code).To(Equal(http.StatusOK), got.raw)
		id := got.body["id"].(string)
		Expect(got.body["role"]).To(Equal("owner"))
		Expect(got.body["name"]).To(Equal("Budget"))
		Expect(got.body["sharedWith"]).To(BeEquivalentTo(0))
		name, e := h.name("bob", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Budget"))

		// The same operation is the same database, whoever repeats it.
		again := h.create("bob", "make-1", "Budget")
		Expect(again.code).To(Equal(http.StatusOK), again.raw)
		Expect(again.body["id"]).To(Equal(id))
		// The same operation with another request is refused.
		other := h.create("bob", "make-1", "Other name")
		Expect(other.code).To(Equal(http.StatusForbidden), other.raw)
		keys, e := h.app.AddonPolicy().Accessible(context.Background(), h.actor("bob"), "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(1))
		// Another person's operation ID is their own namespace.
		other = h.create("carol", "make-1", "Carol's")
		Expect(other.code).To(Equal(http.StatusOK), other.raw)
		Expect(other.body["id"]).NotTo(Equal(id))

		// Private by default: nobody else lists it, installation
		// administration included, and the listing shows no other user's data.
		for _, user := range []string{"alice", "dave"} {
			got := h.list(user)
			Expect(got.code).To(Equal(http.StatusOK), got.raw)
			Expect(resourcesOf(got)).NotTo(HaveKey(id), user)
		}
		mine := resourcesOf(h.list("bob"))
		Expect(mine).To(HaveLen(1))
		Expect(mine[id]["role"]).To(Equal("owner"))
		Expect(mine[id]).To(HaveKey("revision"))
		// The creation arguments are the tool's own schema: an identity field
		// or an unknown one never reaches the add-on.
		for _, args := range []string{`{"name":"X","tool":"databases-delete"}`, `{"name":"X","userId":"alice"}`, `{"name":"X","extra":1}`, `{}`} {
			b := fmt.Sprintf(`{"addonId":"tables","operationId":"bad-op","generation":1,"arguments":%s}`, args)
			Expect(h.send("POST", "/api/addons/resources", b, h.tab("bob", "bad-op")).code).To(Equal(http.StatusBadRequest), args)
		}
		// A stale generation is a stale view.
		stale := `{"addonId":"tables","operationId":"stale-op","generation":9,"arguments":{"name":"X"}}`
		got = h.send("POST", "/api/addons/resources", stale, h.tab("bob", "stale-op"))
		Expect(got.code).To(Equal(http.StatusConflict))
		Expect(got.errType()).To(Equal("stale"))
		// A person without the feature has no route at all.
		Expect(h.list("erin").code).To(Equal(http.StatusForbidden))
	})

	It("keeps the owner, editor and viewer matrix on the UI routes and shows each person only their own role", func() {
		h := ready(false)
		res := h.database("bob", "m-1", "Matrix")
		Expect(h.grant("bob", res, "carol", "viewer").code).To(Equal(200))
		Expect(h.grant("bob", res, "dave", "editor").code).To(Equal(200))

		for user, role := range map[string]string{"bob": "owner", "carol": "viewer", "dave": "editor"} {
			listed := resourcesOf(h.list(user))
			Expect(listed).To(HaveKey(res), user)
			Expect(listed[res]["role"]).To(Equal(role), user)
			if role != "owner" {
				// Grantees see who shared it and neither the grants nor the
				// revision of the owner's controls.
				Expect(listed[res]["owner"]).To(Equal("Name of bob"))
				Expect(listed[res]).NotTo(HaveKey("sharedWith"))
				Expect(listed[res]).NotTo(HaveKey("revision"))
			} else {
				Expect(listed[res]["sharedWith"]).To(BeEquivalentTo(2))
			}
			name, e := h.name(user, res)
			Expect(e).NotTo(HaveOccurred(), user)
			Expect(name).To(Equal("Matrix"))
		}
		rev := h.revision("bob", res)
		// Only the owner shares, renames, searches, reads grants or deletes;
		// everyone else gets the answer of an unknown resource.
		unknown := h.rename("bob", "r"+"0123456789abcdef0123456789abcde", "op-x", "N", rev)
		Expect(unknown.code).To(Equal(http.StatusForbidden))
		for _, user := range []string{"carol", "dave", "alice", "erin"} {
			for name, got := range map[string]reply{
				"rename": h.rename(user, res, "op-"+user, "Hijacked", rev),
				"delete": h.remove(user, res, "del-"+user, "Matrix", rev),
				"grant":  h.grant(user, res, user, "editor"),
				"revoke": h.revoke(user, res, "carol"),
				"grants": h.grants(user, res),
				"users":  h.users(user, res, "ca"),
			} {
				if user == "erin" {
					// No feature: the route gate answers first.
					Expect(got.code).To(Equal(http.StatusForbidden), user+" "+name)
					continue
				}
				Expect(got.code).To(Equal(unknown.code), user+" "+name+" "+got.raw)
				Expect(got.errType()).To(Equal("addon_error"), user+" "+name)
			}
		}
		// Nothing changed.
		name, _ := h.name("bob", res)
		Expect(name).To(Equal("Matrix"))
		Expect(resourcesOf(h.list("carol"))[res]["role"]).To(Equal("viewer"))

		// The owner renames by the person's own action: no proposal, a
		// receipt, and the name is the add-on's.
		done := h.rename("bob", res, "rename-1", "Matrix 2027", rev)
		Expect(done.code).To(Equal(http.StatusOK), done.raw)
		Expect(done.body["name"]).To(Equal("Matrix 2027"))
		name, _ = h.name("carol", res)
		Expect(name).To(Equal("Matrix 2027"))
		status := h.send("GET", "/api/addons/resources/"+res+"/operations/rename-1?kind=rename", "", h.tab("bob", res))
		Expect(status.code).To(Equal(200), status.raw)
		Expect(status.body["state"]).To(Equal("committed"))
		// A repeated operation replays the receipt and renames nothing twice.
		again := h.rename("bob", res, "rename-1", "Matrix 2027", rev)
		Expect(again.code).To(Equal(http.StatusOK), again.raw)
		// The proposal list stays empty: a UI change is not a chat proposal.
		pending := h.send("GET", "/api/addons/proposals?state=pending", "", h.bearer("bob"))
		Expect(pending.body["proposals"]).To(BeEmpty())
		// The same operation ID of two owners are two operations.
		res2 := h.database("carol", "m-2", "Carol's")
		rev2 := h.revision("carol", res2)
		Expect(h.rename("carol", res2, "rename-1", "Carol's 2027", rev2).code).To(Equal(http.StatusOK))
		name, _ = h.name("carol", res2)
		Expect(name).To(Equal("Carol's 2027"))
		// A rename carries the revision the owner saw; a name the add-on
		// refuses changes nothing.
		bad := h.rename("bob", res, "rename-bad", "", rev)
		Expect(bad.code).To(Equal(http.StatusBadRequest))
		moved := map[string]any{"schema": 99, "data": 0}
		stale := h.rename("bob", res, "rename-stale", "Never", moved)
		Expect(stale.code).To(Equal(http.StatusConflict))
		Expect(stale.errType()).To(Equal("stale"))
		name, _ = h.name("bob", res)
		Expect(name).To(Equal("Matrix 2027"))
	})

	It("shares whole databases by immutable account ID behind a bounded picker that tells nothing else", func() {
		h := ready(false)
		res := h.database("bob", "p-1", "Shared")

		// The picker: bounded prefix, minimal fields, eligible accounts only.
		Expect(h.users("bob", res, "c").code).To(Equal(http.StatusBadRequest))
		got := h.users("bob", res, "Name%20of%20c")
		Expect(got.code).To(Equal(200), got.raw)
		users, _ := got.body["users"].([]any)
		Expect(users).To(HaveLen(1))
		user := users[0].(map[string]any)
		Expect(user).To(HaveLen(2))
		Expect(user["id"]).To(Equal("carol"))
		Expect(user["displayName"]).To(Equal("Name of carol"))
		// Not offered: oneself, accounts without the feature and pending ones.
		all := h.users("bob", res, "Name%20of")
		var ids []string
		for _, x := range all.body["users"].([]any) {
			ids = append(ids, x.(map[string]any)["id"].(string))
		}
		Expect(ids).To(ConsistOf("alice", "carol", "dave"))
		Expect(all.raw).NotTo(ContainSubstring("@"))
		// The match is a prefix and the wildcard characters mean themselves.
		Expect(resourcesOfUsers(h.users("bob", res, "%25%25"))).To(BeEmpty())
		Expect(resourcesOfUsers(h.users("bob", res, "ame%20of"))).To(BeEmpty())

		// Roles are viewer or editor; the owner role is never granted.
		Expect(h.grant("bob", res, "carol", "owner").code).To(Equal(http.StatusBadRequest))
		Expect(h.grant("bob", res, "carol", "").code).To(Equal(http.StatusBadRequest))
		// Only accounts that exist, are active and hold the feature.
		for _, target := range []string{"erin", "frank", "nobody", "legacy-api-key", "local-owner", "bob"} {
			Expect(h.grant("bob", res, target, "viewer").code).To(Equal(http.StatusForbidden), target)
		}
		done := h.grant("bob", res, "carol", "viewer")
		Expect(done.code).To(Equal(200), done.raw)
		granted := done.body["grants"].([]any)
		Expect(granted).To(HaveLen(1))
		Expect(granted[0]).To(Equal(map[string]any{"userId": "carol", "displayName": "Name of carol", "role": "viewer"}))
		// Changing the role and listing the grants.
		Expect(h.grant("bob", res, "carol", "editor").code).To(Equal(200))
		listed := h.grants("bob", res)
		Expect(listed.code).To(Equal(200))
		Expect(listed.body["grants"].([]any)[0].(map[string]any)["role"]).To(Equal("editor"))
		// Revoking removes it, and a repeated revoke is the same state.
		Expect(h.revoke("bob", res, "carol").code).To(Equal(200))
		Expect(h.revoke("bob", res, "carol").code).To(Equal(200))
		Expect(resourcesOf(h.list("carol"))).NotTo(HaveKey(res))
		_, e := h.name("carol", res)
		Expect(e).To(HaveOccurred())
		// The sharing routes accept the owner's own session only.
		for _, header := range []func(*http.Request){h.bearer("bob"), h.session("bob")} {
			Expect(h.send("PUT", "/api/addons/resources/"+res+"/grants/carol", `{"role":"viewer"}`, header).code).To(Equal(http.StatusForbidden))
			Expect(h.send("DELETE", "/api/addons/resources/"+res+"/grants/carol", "", header).code).To(Equal(http.StatusForbidden))
			Expect(h.send("GET", "/api/addons/resources/"+res+"/grants", "", header).code).To(Equal(http.StatusForbidden))
			Expect(h.send("GET", "/api/addons/resources/"+res+"/users?q=ca", "", header).code).To(Equal(http.StatusForbidden))
		}
		// A cross-site browser request never reaches the handler.
		cross := func(r *http.Request) { h.tab("bob", res)(r); r.Header.Set("Sec-Fetch-Site", "cross-site") }
		Expect(h.send("PUT", "/api/addons/resources/"+res+"/grants/carol", `{"role":"viewer"}`, cross).code).To(BeElementOf(http.StatusBadRequest, http.StatusForbidden))
		Expect(resourcesOf(h.list("carol"))).NotTo(HaveKey(res))
	})

	It("keeps sharing disabled in local-owner mode with a typed answer and no named users", func() {
		h := ready(true)
		res := h.database("local-owner", "l-1", "Mine")
		listed := h.list("local-owner")
		Expect(listed.code).To(Equal(200), listed.raw)
		Expect(listed.body["sharing"]).To(Equal(false))
		Expect(resourcesOf(listed)).To(HaveKey(res))
		Expect(resourcesOf(listed)[res]["role"]).To(Equal("owner"))
		for name, got := range map[string]reply{
			"grant":  h.grant("local-owner", res, "carol", "viewer"),
			"revoke": h.revoke("local-owner", res, "carol"),
			"grants": h.grants("local-owner", res),
			"users":  h.users("local-owner", res, "ca"),
		} {
			Expect(got.code).To(Equal(http.StatusConflict), name+" "+got.raw)
			Expect(got.errType()).To(Equal("sharing_disabled"), name)
		}
		// Everything else works: the mode has one owner, not no owner.
		rev := h.revision("local-owner", res)
		done := h.rename("local-owner", res, "rl-1", "Mine too", rev)
		Expect(done.code).To(Equal(200), done.raw)
		// A request that carries an API credential is not the local owner's page.
		Expect(h.send("POST", "/api/addons/resources/"+res+"/rename", "{}", h.tab("x", res), func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }).code).To(Equal(http.StatusForbidden))
	})

	It("waits for a revoked user's admitted work and cancels it before acknowledging the revocation", func() {
		h := ready(false)
		res := h.database("bob", "c-1", "Busy")
		Expect(h.grant("bob", res, "carol", "editor").code).To(Equal(200))
		p := h.app.AddonPolicy()
		started, cancelled := make(chan struct{}), make(chan struct{})
		var once sync.Once
		go func() {
			defer GinkgoRecover()
			_ = p.Admit(context.Background(), h.actor("carol"), h.key(res), policy.ActionWrite, func(ctx context.Context) error {
				once.Do(func() { close(started) })
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			})
		}()
		Eventually(started).Should(BeClosed())
		done := h.revoke("bob", res, "carol")
		Expect(done.code).To(Equal(200), done.raw)
		// The acknowledgement came after the admitted work saw its context end.
		Expect(cancelled).To(BeClosed())
		_, e := h.name("carol", res)
		Expect(e).To(HaveOccurred())
		Expect(resourcesOf(h.list("carol"))).NotTo(HaveKey(res))
	})
})

func resourcesOfUsers(r reply) []any {
	GinkgoHelper()
	Expect(r.code).To(Equal(200), r.raw)
	users, _ := r.body["users"].([]any)
	return users
}

var _ = json.Marshal
