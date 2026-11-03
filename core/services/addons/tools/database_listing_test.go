// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const databaseListName = "addon__tables__databases-list"

type listingBackend struct {
	*fake
	resources  gateway.ResourceList
	err        error
	actors     []contract.Actor
	ids        []string
	selections int
}

func (b *listingBackend) ListResources(_ context.Context, a contract.Actor, id string) (gateway.ResourceList, error) {
	b.actors = append(b.actors, a)
	b.ids = append(b.ids, id)
	return b.resources, b.err
}
func (b *listingBackend) ResolveSelection(context.Context, contract.Actor, string, string) (string, error) {
	b.selections++
	return "selected", nil
}

var _ = Describe("Tables host database listing", func() {
	ctx := context.Background()
	var b *listingBackend
	var a *tools.ToolAdapter
	BeforeEach(func() {
		b = &listingBackend{fake: newFake(), resources: gateway.ResourceList{AddonID: "tables"}}
		a = tools.NewToolAdapter(b)
	})
	It("synthesizes exactly one strict host read, without enumerating resources at discovery", func() {
		defs, err := a.Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		var found []tools.ToolDefinition
		for _, d := range defs {
			if d.Name == databaseListName {
				found = append(found, d)
			}
		}
		Expect(found).To(HaveLen(1))
		d := found[0]
		Expect(d.AddonID).To(Equal("tables"))
		Expect(d.Tool).To(Equal("databases-list"))
		Expect(d.Kind).To(Equal(contract.ToolKindRead))
		Expect(d.Parameters).To(Equal(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}))
		Expect(b.ids).To(BeEmpty())
	})
	It("projects only opaque IDs, optional names and caller roles through a normal data envelope", func() {
		n := 4
		b.resources = gateway.ResourceList{AddonID: "tables", Generation: 9, Sharing: true, Operations: []string{"delete"}, Resources: []gateway.ResourceInfo{
			{ID: "b", AddonID: "tables", Role: "editor", Owner: "private", SharedWith: &n},
			{ID: "a", AddonID: "tables", Name: "Budget", Role: "viewer"},
			{ID: "c", AddonID: "tables", Name: "Budget", Role: "owner"},
		}}
		out, err := a.ExecuteIn(ctx, member, "conv1", databaseListName, args(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Kind).To(Equal(tools.ResultData))
		Expect(out.Tool).To(Equal(databaseListName))
		Expect(string(out.Result)).To(MatchJSON(`{"databases":[{"resourceId":"a","name":"Budget","role":"viewer"},{"resourceId":"b","role":"editor"},{"resourceId":"c","name":"Budget","role":"owner"}],"truncated":false}`))
		Expect(b.actors).To(Equal([]contract.Actor{member}))
		Expect(b.ids).To(Equal([]string{"tables"}))
		Expect(b.calls()).To(BeZero())
		Expect(b.selections).To(BeZero())
	})
	It("returns an array even for empty success and truthfully bounds sorted 100/101 results", func() {
		for _, n := range []int{0, 100, 101} {
			for _, truncated := range []bool{false, true} {
				b.resources.Resources = nil
				b.resources.Truncated = truncated
				for i := n; i > 0; i-- {
					b.resources.Resources = append(b.resources.Resources, gateway.ResourceInfo{ID: fmt.Sprintf("db-%03d", i), Role: "owner"})
				}
				out, err := a.Execute(ctx, member, databaseListName, args(`{}`))
				Expect(err).NotTo(HaveOccurred())
				var doc struct {
					Databases []struct {
						ResourceID string `json:"resourceId"`
					}
					Truncated bool
				}
				Expect(json.Unmarshal(out.Result, &doc)).To(Succeed())
				Expect(doc.Databases).NotTo(BeNil())
				Expect(doc.Databases).To(HaveLen(min(n, 100)))
				Expect(doc.Truncated).To(Equal(truncated || n > 100))
				if n > 0 {
					Expect(doc.Databases[0].ResourceID).To(Equal("db-001"))
				}
			}
		}
	})
	It("rejects every input property and non-object rather than resolving selection", func() {
		for _, input := range []string{`{"resourceId":"a"}`, `{"cursor":"x"}`, `{"offset":0}`, `{"addonId":"tables"}`, `{"name":"x"}`, `{"role":"owner"}`, `{"actor":{}}`, `{"tool":"databases-list"}`, `null`, `[]`, `""`} {
			_, err := a.ExecuteIn(ctx, member, "conv1", databaseListName, args(input))
			Expect(err).To(HaveOccurred(), input)
		}
		Expect(b.ids).To(BeEmpty())
		Expect(b.calls()).To(BeZero())
		Expect(b.selections).To(BeZero())
	})
	It("fails closed without the optional backend interface and never synthesizes for other add-ons", func() {
		plain := tools.NewToolAdapter(newFake())
		defs, err := plain.Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		for _, d := range defs {
			Expect(d.Name).NotTo(Equal(databaseListName))
		}
		_, err = plain.Execute(ctx, member, databaseListName, args(`{}`))
		Expect(err).To(HaveOccurred())
		b.list.Apps[0].ID = "notes"
		defs, err = a.Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		for _, d := range defs {
			Expect(d.Tool).NotTo(Equal("databases-list"))
		}
	})
	It("rejects a reserved Tables manifest collision in discovery and execution even if not advertised", func() {
		b.list.Apps[0].ToolDefinitions = append(b.list.Apps[0].ToolDefinitions, contract.ToolDefinition{Name: "databases-list", Kind: contract.ToolKindRead, UIOnly: true})
		_, err := a.Discover(ctx, member)
		Expect(err).To(HaveOccurred())
		_, err = a.Execute(ctx, member, databaseListName, args(`{}`))
		Expect(err).To(HaveOccurred())
		Expect(b.ids).To(BeEmpty())
		Expect(b.calls()).To(BeZero())
	})
	It("rechecks missing, hidden, disabled, stopped and uninitialized Tables at execution", func() {
		for _, mutate := range []func(){func() { b.list.Apps = nil }, func() { b.list.Apps[0].Enabled = false }, func() { b.list.Apps[0].State = "stopped" }, func() { b.list.Apps[0].Generation = 0 }} {
			b.list = newFake().list
			_, err := a.Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred())
			mutate()
			_, err = a.Execute(ctx, member, databaseListName, args(`{}`))
			Expect(err).To(MatchError(tools.ErrUnknownTool))
			defs, err := a.Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred())
			Expect(defs).To(BeEmpty())
		}
		Expect(b.ids).To(BeEmpty())
	})
	It("preserves typed credential, feature, owner, forwarding and registry failures after discovery", func() {
		for _, failure := range []error{gateway.ErrForbidden, gateway.ErrNotPermitted, gateway.ErrUnavailable, gateway.ErrResourceUnreachable} {
			b.err = nil
			_, err := a.Discover(ctx, member)
			Expect(err).NotTo(HaveOccurred())
			b.err = failure
			out, err := a.Execute(ctx, member, databaseListName, args(`{}`))
			Expect(err).To(MatchError(failure))
			Expect(out.Result).To(BeEmpty())
			b.appsErr = failure
			_, err = a.Execute(ctx, member, databaseListName, args(`{}`))
			Expect(err).To(HaveOccurred())
			b.appsErr = nil
		}
	})
	It("rejects missing principals and malformed projections instead of successful empty data", func() {
		for _, actor := range []contract.Actor{{}, {UserID: "bob", Mode: "named-user"}, {UserID: "bob", CredentialID: "x", Mode: "anonymous"}} {
			_, err := a.Execute(ctx, actor, databaseListName, args(`{}`))
			Expect(err).To(MatchError(gateway.ErrForbidden))
		}
		for _, r := range []gateway.ResourceInfo{{Role: "owner"}, {ID: "a", Role: "admin"}, {ID: "a", Role: ""}, {ID: "a", Role: "viewer", AddonID: "notes"}} {
			b.resources.Resources = []gateway.ResourceInfo{r}
			_, err := a.Execute(ctx, member, databaseListName, args(`{}`))
			Expect(err).To(HaveOccurred())
		}
		b.resources.Resources = []gateway.ResourceInfo{{ID: "a", Role: "viewer"}, {ID: "a", Role: "owner"}}
		_, err := a.Execute(ctx, member, databaseListName, args(`{}`))
		Expect(err).To(HaveOccurred())
	})
	It("keeps synthesized listing behind consumer allowlists and hidden add-ons", func() {
		exec, err := mcpTools.NewAddonToolExecutor(ctx, a, member)
		Expect(err).NotTo(HaveOccurred())
		for _, restricted := range []*mcpTools.AddonToolExecutor{exec.Only([]string{}), exec.Only([]string{"other"}), exec.Without(map[string]bool{"tables": true})} {
			defs, err := restricted.DiscoverTools(ctx)
			Expect(err).NotTo(HaveOccurred())
			for _, d := range defs {
				Expect(d.Name).NotTo(Equal(databaseListName))
			}
			_, err = restricted.ExecuteTool(ctx, databaseListName, `{}`)
			Expect(err).To(MatchError(tools.ErrUnknownTool))
		}
		Expect(b.ids).To(BeEmpty())
		_, err = exec.Only([]string{"tables"}).ExecuteTool(ctx, databaseListName, `{}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.actors).To(Equal([]contract.Actor{member}))
		Expect(b.calls()).To(BeZero())
	})
	It("does not let a listing-capable provider invent a named-user principal", func() {
		provider := mcpTools.NewAddonToolProvider(a, auth.AddonModeNamedUser)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.Header.Set("X-Forwarded-User", "alice")
		r.Header.Set("X-LocalAI-Actor", "alice")
		_, err := provider.ExecutorFor(echo.New().NewContext(r, httptest.NewRecorder()))
		Expect(err).To(HaveOccurred())
		Expect(mcpTools.AddonToolsHTTPError(err).(*echo.HTTPError).Code).To(Equal(http.StatusForbidden))
		Expect(b.ids).To(BeEmpty())
	})
	It("revalidates listing failures through an already-bound consumer without namespace fallback", func() {
		exec, err := mcpTools.NewAddonToolExecutor(ctx, a, member)
		Expect(err).NotTo(HaveOccurred())
		other := &greedy{}
		combined := mcpTools.CombineExecutors(other, exec)
		for _, failure := range []error{gateway.ErrForbidden, gateway.ErrNotPermitted, gateway.ErrUnavailable} {
			b.err = failure
			_, err = combined.ExecuteTool(ctx, databaseListName, `{}`)
			Expect(err).To(MatchError(failure))
		}
		Expect(b.actors).To(Equal([]contract.Actor{member, member, member}))
		Expect(other.ran).To(BeEmpty())
		Expect(b.calls()).To(BeZero())
		Expect(b.selections).To(BeZero())
	})
	It("preserves the model read byte bound", func() {
		b.resources.Resources = []gateway.ResourceInfo{{ID: "a", Role: "viewer", Name: strings.Repeat("x", tools.MaxResultBytes)}}
		_, err := a.Execute(ctx, member, databaseListName, args(`{}`))
		Expect(err).To(MatchError(tools.ErrResultTooLarge))
	})
	// A visible Tables installation can have no executable model tools: this host
	// read depends on the registry, not on the executable's tool inventory.
	It("offers the host read even when Tables has no executable definitions", func() {
		b.list = apps.List{Apps: []apps.App{{ID: "tables", Enabled: true, State: "running", Generation: 1}}}
		defs, err := a.Discover(ctx, member)
		Expect(err).NotTo(HaveOccurred())
		Expect(defs).To(HaveLen(1))
		Expect(defs[0].Name).To(Equal(databaseListName))
	})
})
