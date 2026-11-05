// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The agent's list is one link of the chain, after the owner's own rule (the
// gateway decides owner or grant) and before the add-on is called.
var _ = Describe("The resource list of a saved agent", func() {
	ctx := context.Background()
	const (
		read   = "addon__tables__rows-query"
		insert = "addon__tables__rows-insert"
		drop   = "addon__tables__tables-drop"
		create = "addon__tables__databases-create"
	)
	var b *listingBackend
	var a *tools.ToolAdapter
	limit := func(layers ...tools.ResourceLimit) context.Context {
		return tools.WithResourceLimits(ctx, tools.ResourceLimits{Layers: layers})
	}
	list := tools.ResourceLimit{"tables": {"db-read": "read", "db-write": "write"}}
	callArgs := map[string]string{
		read:   `{"resourceId":"%s","table":"t"}`,
		insert: `{"resourceId":"%s","table":"t","rows":[{"a":1}]}`,
		drop:   `{"resourceId":"%s","table":"t"}`,
	}
	run := func(c context.Context, tool, id string) tools.ToolResult {
		GinkgoHelper()
		body := json.RawMessage(fmt.Sprintf(callArgs[tool], id))
		out, err := a.ExecuteIn(c, member, "conv1", tool, body)
		Expect(err).NotTo(HaveOccurred())
		return out
	}
	BeforeEach(func() {
		b = &listingBackend{fake: newFake(), resources: gateway.ResourceList{AddonID: "tables"}}
		a = tools.NewToolAdapter(b)
	})

	DescribeTable("a call that names a resource",
		func(c func() context.Context, tool, id, code string) {
			out := run(c(), tool, id)
			if code == "" {
				Expect(out.Kind).NotTo(Equal(tools.ResultError), "note %q", out.Note)
				Expect(b.calls()).To(Equal(1), "the add-on host is called")
				return
			}
			Expect(out.Kind).To(Equal(tools.ResultError))
			Expect(out.Code).To(Equal(code))
			Expect(out.Note).NotTo(BeEmpty())
			Expect(b.calls()).To(BeZero(), "nothing reaches the add-on host")
		},
		Entry("no agent: not limited", func() context.Context { return ctx }, read, "db-other", ""),
		Entry("no agent: a write is not limited either", func() context.Context { return ctx }, insert, "db-other", ""),
		Entry("the list is empty: a read is refused", func() context.Context { return limit(tools.ResourceLimit{}) }, read, "db-read", tools.CodeResourceNotInAgentList),
		Entry("the list is empty: a write is refused", func() context.Context { return limit(tools.ResourceLimit{}) }, insert, "db-write", tools.CodeResourceNotInAgentList),
		Entry("listed read: a read goes through", func() context.Context { return limit(list) }, read, "db-read", ""),
		Entry("listed read: a row change is refused", func() context.Context { return limit(list) }, insert, "db-read", tools.CodeResourceReadOnlyForAgent),
		Entry("listed read: a schema change is refused", func() context.Context { return limit(list) }, drop, "db-read", tools.CodeResourceReadOnlyForAgent),
		Entry("listed write: a read goes through", func() context.Context { return limit(list) }, read, "db-write", ""),
		Entry("listed write: a row change goes through", func() context.Context { return limit(list) }, insert, "db-write", ""),
		Entry("listed write: a schema change goes through", func() context.Context { return limit(list) }, drop, "db-write", ""),
		Entry("not listed: a read is refused", func() context.Context { return limit(list) }, read, "db-other", tools.CodeResourceNotInAgentList),
		Entry("not listed: a write is refused", func() context.Context { return limit(list) }, insert, "db-other", tools.CodeResourceNotInAgentList),
		Entry("a sub-agent: the narrower layer wins", func() context.Context {
			return limit(list, tools.ResourceLimit{"tables": {"db-write": "read"}})
		}, insert, "db-write", tools.CodeResourceReadOnlyForAgent),
		Entry("a sub-agent: a resource only the parent lists is refused", func() context.Context {
			return limit(list, tools.ResourceLimit{"tables": {"db-read": "read"}})
		}, read, "db-write", tools.CodeResourceNotInAgentList),
	)

	It("does not touch a tool that names no resource", func() {
		out, err := a.ExecuteIn(limit(tools.ResourceLimit{}), member, "conv1", create, args(`{"name":"New"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Kind).NotTo(Equal(tools.ResultError))
		Expect(b.calls()).To(Equal(1))
	})

	It("does not limit an add-on that does not opt in", func() {
		b.list.Apps[0].Resources.AgentScoped = false
		out := run(limit(tools.ResourceLimit{}), insert, "db-other")
		Expect(out.Kind).NotTo(Equal(tools.ResultError))
		Expect(b.calls()).To(Equal(1))
	})

	It("applies the list to the resource the chat selected, not only to one the model named", func() {
		// The fake backend selects "selected"; it is on no list.
		out, err := a.ExecuteIn(limit(list), member, "conv1", read, args(`{"table":"t"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Code).To(Equal(tools.CodeResourceNotInAgentList))
		Expect(b.calls()).To(BeZero())
		out, err = a.ExecuteIn(limit(tools.ResourceLimit{"tables": {"selected": "read"}}), member, "conv1", read, args(`{"table":"t"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Kind).NotTo(Equal(tools.ResultError))
	})

	It("names the allowed resources in the refusal, so the model can choose one", func() {
		out := run(limit(list), read, "db-other")
		Expect(out.Allowed).To(Equal([]string{"db-read", "db-write"}))
	})

	Describe("the list tool", func() {
		BeforeEach(func() {
			b.resources = gateway.ResourceList{AddonID: "tables", Resources: []gateway.ResourceInfo{
				{ID: "db-other", Name: "Other", Role: "owner"},
				{ID: "db-read", Name: "Read", Role: "owner"},
				{ID: "db-write", Name: "Write", Role: "editor"},
			}}
		})
		It("shows the whole list to a chat with no agent", func() {
			out, err := a.ExecuteIn(ctx, member, "conv1", databaseListName, args(`{}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(out.Result)).To(ContainSubstring("db-other"))
		})
		It("shows an agent only its list, and a read resource as viewer", func() {
			out, err := a.ExecuteIn(limit(list), member, "conv1", databaseListName, args(`{}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(out.Result)).To(MatchJSON(`{"databases":[{"resourceId":"db-read","name":"Read","role":"viewer"},{"resourceId":"db-write","name":"Write","role":"editor"}],"truncated":false}`))
		})
		It("shows an agent with an empty list nothing", func() {
			out, err := a.ExecuteIn(limit(tools.ResourceLimit{}), member, "conv1", databaseListName, args(`{}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(out.Result)).To(MatchJSON(`{"databases":[],"truncated":false}`))
		})
	})

	Describe("the choices a person can give an agent", func() {
		It("lists what the owner's listing returns, before any limit", func() {
			b.resources = gateway.ResourceList{AddonID: "tables", Resources: []gateway.ResourceInfo{{ID: "db-b", Role: "viewer"}, {ID: "db-a", Name: "A", Role: "owner"}}}
			rows, truncated, err := a.ResourceChoices(limit(tools.ResourceLimit{}), member, "tables")
			Expect(err).NotTo(HaveOccurred())
			Expect(truncated).To(BeFalse())
			Expect(rows).To(Equal([]tools.ResourceChoice{{ID: "db-a", Name: "A", Role: "owner"}, {ID: "db-b", Role: "viewer"}}))
		})
		It("is unknown for an add-on that does not opt in or does not exist", func() {
			_, _, err := a.ResourceChoices(ctx, member, "notes")
			Expect(err).To(MatchError(tools.ErrUnknownTool))
			b.list.Apps[0].Resources.AgentScoped = false
			_, _, err = a.ResourceChoices(ctx, member, "tables")
			Expect(err).To(MatchError(tools.ErrUnknownTool))
		})
		It("carries the label and the translations of the nouns in the listing", func() {
			b.list.Apps[0].ResourceTexts = map[string]contract.ResourceText{"it": {Noun: "database", NounPlural: "database"}}
			ls, err := a.Listings(ctx, member)
			Expect(err).NotTo(HaveOccurred())
			Expect(ls).To(HaveLen(1))
			Expect(ls[0].Resources).NotTo(BeNil())
			Expect(ls[0].Resources.Noun).To(Equal("database"))
			Expect(ls[0].Resources.NounPlural).To(Equal("databases"))
			Expect(ls[0].Resources.Translations).To(HaveKey("it"))
		})
	})
})
