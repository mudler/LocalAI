// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	addonsvc "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func (h *host) selection(user, conv string) reply {
	GinkgoHelper()
	return h.send("GET", "/api/addons/conversations/"+conv+"/selection/tables", "", h.bearer(user))
}

func (h *host) choose(user, conv, res string) reply {
	GinkgoHelper()
	return h.send("PUT", "/api/addons/conversations/"+conv+"/selection/tables", fmt.Sprintf(`{"resourceId":%q}`, res), h.bearer(user))
}

func (h *host) clear(user, conv string) reply {
	GinkgoHelper()
	return h.send("DELETE", "/api/addons/conversations/"+conv+"/selection/tables", "", h.bearer(user))
}

// say runs one model tool call of a chat conversation as the user's key.
func (h *host) say(user, conv, tool, body string) (tools.ToolResult, error) {
	GinkgoHelper()
	return tools.NewToolAdapter(h.app.AddonGateway()).ExecuteIn(context.Background(), h.actor(user), conv, tool, json.RawMessage(body))
}

func inspectedName(r tools.ToolResult) string {
	GinkgoHelper()
	Expect(r.Kind).To(Equal(tools.ResultData))
	var v struct {
		Name string `json:"name"`
	}
	Expect(json.Unmarshal(r.Result, &v)).To(Succeed())
	return v.Name
}

var _ = Describe("the database a conversation works on", func() {
	It("keeps one selection per user, conversation and add-on, across people, conversations and restarts", func() {
		h := ready(false)
		sales := h.database("bob", "v-1", "Sales")
		stock := h.database("bob", "v-2", "Stock")
		mine := h.database("carol", "v-3", "Carols")

		// Nothing is selected, and no process-wide default exists.
		got := h.selection("bob", "chat-1")
		Expect(got.code).To(Equal(http.StatusNotFound))
		Expect(got.errType()).To(Equal("not_selected"))

		done := h.choose("bob", "chat-1", sales)
		Expect(done.code).To(Equal(200), done.raw)
		Expect(done.body).To(HaveKeyWithValue("resourceId", sales))
		Expect(done.body).To(HaveKeyWithValue("role", "owner"))
		Expect(done.body).To(HaveKeyWithValue("name", "Sales"))
		Expect(h.choose("bob", "chat-2", stock).code).To(Equal(200))
		// Two conversations of one user keep two selections.
		Expect(h.selection("bob", "chat-1").body["resourceId"]).To(Equal(sales))
		Expect(h.selection("bob", "chat-2").body["resourceId"]).To(Equal(stock))
		// The same conversation ID of another person is another namespace and
		// never shows or inherits this selection.
		Expect(h.selection("carol", "chat-1").code).To(Equal(http.StatusNotFound))
		Expect(h.choose("carol", "chat-1", mine).code).To(Equal(200))
		Expect(h.selection("bob", "chat-1").body["resourceId"]).To(Equal(sales))
		Expect(h.selection("carol", "chat-1").body["resourceId"]).To(Equal(mine))
		// A database the person cannot read is not selectable, with the answer
		// of an unknown database; neither is a database of another add-on or a
		// malformed identifier.
		Expect(h.choose("carol", "chat-2", stock).code).To(Equal(http.StatusForbidden))
		Expect(h.choose("carol", "chat-2", "r0123456789abcdef0123456789abcde").code).To(Equal(http.StatusForbidden))
		put := h.send("PUT", "/api/addons/conversations/chat-2/selection/other", fmt.Sprintf(`{"resourceId":%q}`, mine), h.bearer("carol"))
		Expect(put.code).To(Equal(http.StatusForbidden))
		Expect(h.send("PUT", "/api/addons/conversations/bad%20conv/selection/tables", fmt.Sprintf(`{"resourceId":%q}`, mine), h.bearer("carol")).code).To(Equal(400))
		Expect(h.send("PUT", "/api/addons/conversations/chat-2/selection/tables", `{"resourceId":"../x"}`, h.bearer("carol")).code).To(Equal(http.StatusBadRequest))
		Expect(h.selection("carol", "chat-2").code).To(Equal(http.StatusNotFound))
		// A user without the feature has no selection routes.
		Expect(h.selection("erin", "chat-1").code).To(Equal(http.StatusForbidden))

		// The selection is durable on the owner.
		h.stop()
		h.start()
		h.running()
		Expect(h.selection("bob", "chat-2").body["resourceId"]).To(Equal(stock))
		Expect(h.selection("carol", "chat-1").body["resourceId"]).To(Equal(mine))

		Expect(h.clear("bob", "chat-2").code).To(Equal(http.StatusNoContent))
		Expect(h.clear("bob", "chat-2").code).To(Equal(http.StatusNoContent))
		Expect(h.selection("bob", "chat-2").code).To(Equal(http.StatusNotFound))
		Expect(h.selection("bob", "chat-1").code).To(Equal(200))
	})

	It("lets a tool call omit the database in a conversation and resolves it at the owner with typed errors", func() {
		h := ready(false)
		sales := h.database("bob", "w-1", "Sales")
		stock := h.database("bob", "w-2", "Stock")
		Expect(h.choose("bob", "chat-1", sales).code).To(Equal(200))
		inspect := "addon__tables__databases-inspect"

		got, e := h.say("bob", "chat-1", inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(inspectedName(got)).To(Equal("Sales"))
		// An explicit database is the person's own words and still works.
		got, e = h.say("bob", "chat-1", inspect, fmt.Sprintf(`{"resourceId":%q}`, stock))
		Expect(e).NotTo(HaveOccurred())
		Expect(inspectedName(got)).To(Equal("Stock"))
		// No selection is a result that asks the person to select one.
		got, e = h.say("bob", "chat-2", inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Kind).To(Equal(tools.ResultError))
		Expect(got.Code).To(Equal(tools.CodeSelectResource))
		Expect(got.Result).To(BeEmpty())
		// A caller without a conversation, or without a selection of its own,
		// never inherits another's: another user's same conversation ID, an
		// agent without a conversation.
		got, e = h.say("carol", "chat-1", inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Kind).To(Equal(tools.ResultError))
		_, e = h.say("bob", "", inspect, `{}`)
		Expect(e).To(MatchError(tools.ErrInvalidArguments))
		// The conversation is never an argument, and no field of the model
		// can name or replace it.
		for _, args := range []string{`{"conversationId":"chat-1"}`, `{"conversation":"chat-1"}`, `{"selection":"x"}`} {
			_, e = h.say("bob", "chat-9", inspect, args)
			Expect(e).To(MatchError(tools.ErrInvalidArguments), args)
		}
		_, e = h.say("bob", "chat-9", inspect, `{"resourceId":"x","tool":"databases-delete"}`)
		Expect(e).To(MatchError(tools.ErrForbiddenArgument))
		// A conversation that is not the shape of one is refused.
		_, e = h.say("bob", "../chat", inspect, `{}`)
		Expect(e).To(MatchError(tools.ErrInvalidArguments))

		// Writes use it too: a proposal names the selected database.
		created, e := h.say("bob", "chat-1", "addon__tables__tables-create", `{"table":"Orders"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(created.Kind).To(Equal(tools.ResultProposal))
		Expect(created.Proposal.ResourceID).To(Equal(sales))

		// The executor of a chat request binds the conversation once, like the
		// actor, and nothing in the arguments changes it.
		exec, e := mcpTools.NewAddonToolExecutorFor(context.Background(), tools.NewToolAdapter(h.app.AddonGateway()), h.actor("bob"), "chat-1")
		Expect(e).NotTo(HaveOccurred())
		out, e := exec.ExecuteTool(context.Background(), inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring(`"Sales"`))
		out, e = exec.ExecuteTool(context.Background(), inspect, `{"conversationId":"chat-9"}`)
		Expect(e).To(HaveOccurred())
		Expect(out).To(BeEmpty())
		none, e := mcpTools.NewAddonToolExecutor(context.Background(), tools.NewToolAdapter(h.app.AddonGateway()), h.actor("bob"))
		Expect(e).NotTo(HaveOccurred())
		_, e = none.ExecuteTool(context.Background(), inspect, `{}`)
		Expect(e).To(MatchError(tools.ErrInvalidArguments))
	})

	It("withdraws the management and the tool path while the add-on is disabled and restores them on enable", func() {
		h := ready(false)
		sales := h.database("bob", "y-1", "Sales")
		Expect(h.choose("bob", "chat-1", sales).code).To(Equal(200))
		inspect := "addon__tables__databases-inspect"
		_, e := h.say("bob", "chat-1", inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())

		ctx := context.Background()
		_, e = h.app.AddonManager().Submit(ctx, h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "y-disable", AddonID: "tables", Action: "disable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		// No management call answers from a disabled add-on, a stale tab
		// cannot create, and the model is not offered the tool.
		Expect(h.list("bob").code).To(Equal(http.StatusServiceUnavailable))
		rev := map[string]any{"schema": 1, "data": 0}
		Expect(h.rename("bob", sales, "y-r", "N", rev).code).To(BeElementOf(503, 403))
		Expect(h.create("bob", "y-c", "N").code).To(BeElementOf(503, 403))
		_, e = h.say("bob", "chat-1", inspect, `{}`)
		Expect(e).To(HaveOccurred())
		// The data and the selection are kept; only the display name needs the
		// add-on.
		kept := h.selection("bob", "chat-1")
		Expect(kept.code).To(Equal(200), kept.raw)
		Expect(kept.body).To(HaveKeyWithValue("resourceId", sales))
		Expect(kept.body).NotTo(HaveKey("name"))

		_, e = h.app.AddonManager().Submit(ctx, h.actor("alice"), addonsvc.LifecycleCommand{OperationID: "y-enable", AddonID: "tables", Action: "enable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		h.running()
		Expect(resourcesOf(h.list("bob"))).To(HaveKey(sales))
		got, e := h.say("bob", "chat-1", inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(inspectedName(got)).To(Equal("Sales"))
	})

	It("answers a revoked, deleted or tampered reference like an unknown database and never reuses it", func() {
		h := ready(false)
		shared := h.database("bob", "x-1", "Shared")
		doomed := h.database("bob", "x-2", "Doomed")
		private := h.database("bob", "x-3", "Private")
		Expect(h.grant("bob", shared, "carol", "viewer").code).To(Equal(200))
		Expect(h.grant("bob", doomed, "carol", "viewer").code).To(Equal(200))
		Expect(h.choose("carol", "chat-1", shared).code).To(Equal(200))
		Expect(h.choose("carol", "chat-2", doomed).code).To(Equal(200))
		inspect := "addon__tables__databases-inspect"
		_, e := h.say("carol", "chat-1", inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())
		// The answer for an unknown database, to compare with.
		_, unknown := h.say("carol", "chat-1", inspect, `{"resourceId":"r0123456789abcdef0123456789abcde"}`)
		Expect(unknown).To(HaveOccurred())
		Expect(gateway.StatusFor(unknown)).To(Equal(403))

		// Revoked: the same denial. The reference is no authority and stays the
		// person's own: a new grant by the owner makes it work again.
		Expect(h.revoke("bob", shared, "carol").code).To(Equal(200))
		_, e = h.say("carol", "chat-1", inspect, `{}`)
		Expect(e).To(HaveOccurred())
		Expect(e.Error()).To(Equal(unknown.Error()))
		Expect(gateway.StatusFor(e)).To(Equal(403))
		Expect(h.selection("carol", "chat-1").code).To(Equal(http.StatusNotFound))
		Expect(h.grant("bob", shared, "carol", "viewer").code).To(Equal(200))
		got, e := h.say("carol", "chat-1", inspect, `{}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(inspectedName(got)).To(Equal("Shared"))

		// Deleted: the same denial.
		rev := h.revision("bob", doomed)
		Expect(h.remove("bob", doomed, "x-del", "Doomed", rev).code).To(Equal(200))
		_, e = h.say("carol", "chat-2", inspect, `{}`)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
		Expect(e.Error()).To(Equal(unknown.Error()))
		Expect(h.selection("carol", "chat-2").code).To(Equal(http.StatusNotFound))

		// Tampered: the stored file names a database that was never shared.
		// The reference is no authority: the owner decides at every use.
		Expect(h.choose("carol", "chat-3", shared).code).To(Equal(200))
		h.stop()
		path := filepath.Join(h.owner.Root, "selections", "selections.json")
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Version int              `json:"version"`
			Entries []map[string]any `json:"entries"`
		}
		Expect(json.Unmarshal(raw, &doc)).To(Succeed())
		tampered := 0
		for _, entry := range doc.Entries {
			if entry["user"] == "carol" && entry["conversation"] == "chat-3" {
				entry["resourceId"] = private
				tampered++
			}
		}
		Expect(tampered).To(Equal(1))
		raw, err = json.Marshal(doc)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, raw, 0o600)).To(Succeed())
		h.start()
		h.running()
		_, e = h.say("carol", "chat-3", inspect, `{}`)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
		Expect(e.Error()).To(Equal(unknown.Error()))
		got2 := h.selection("carol", "chat-3")
		Expect(got2.code).To(Equal(http.StatusNotFound))
		// The owner of that database is not affected.
		name, e := h.name("bob", private)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Private"))
		_ = time.Second
	})
})
