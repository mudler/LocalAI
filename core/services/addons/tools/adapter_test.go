// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fake is the owner side of the adapter: it records what the adapter asked
// for, and answers from fixed data.
type fake struct {
	mu         sync.Mutex
	list       apps.List
	appsErr    error
	dispatched []call
	proposed   []call
	read       contract.Result
	readErr    error
	proposal   gateway.Proposal
	proposeErr error
}

type call struct {
	Actor   contract.Actor
	Request contract.Request
}

func (f *fake) Apps(_ context.Context, a contract.Actor) (apps.List, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list, f.appsErr
}

func (f *fake) Dispatch(_ context.Context, a contract.Actor, r contract.Request) (contract.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatched = append(f.dispatched, call{a, r})
	return f.read, f.readErr
}

func (f *fake) Propose(_ context.Context, a contract.Actor, r contract.Request) (gateway.Proposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proposed = append(f.proposed, call{a, r})
	p := f.proposal
	if p.ID == "" {
		p.ID = r.OperationID
	}
	return p, f.proposeErr
}

func (f *fake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dispatched) + len(f.proposed)
}

func tablesManifest() contract.Manifest {
	GinkgoHelper()
	raw, e := os.ReadFile("../../../../addons/tables/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	m, e := contract.ParseManifest(raw)
	Expect(e).NotTo(HaveOccurred())
	return m
}

func tablesApp(m contract.Manifest) apps.App {
	return apps.App{ID: "tables", Name: "tables", Generation: 3, Enabled: true, State: "running", Tools: m.Capabilities.Tools,
		ToolDefinitions: m.Capabilities.ToolDefinitions}
}

var member = contract.Actor{UserID: "bob", CredentialID: "session:s1", Mode: "named-user"}

func newFake() *fake {
	return &fake{list: apps.List{Apps: []apps.App{tablesApp(tablesManifest())}}}
}

func args(v string) json.RawMessage { return json.RawMessage(v) }

var _ = Describe("Addon tool adapter", func() {
	var (
		f *fake
		a *tools.ToolAdapter
	)
	BeforeEach(func() {
		f = newFake()
		a = tools.NewToolAdapter(f)
	})

	Describe("discovery", func() {
		It("discovers namespaced tools with schemas from the manifest for enabled running addons only", func() {
			defs, e := a.Discover(context.Background(), member)
			Expect(e).NotTo(HaveOccurred())
			names := []string{}
			for _, d := range defs {
				names = append(names, d.Name)
				Expect(d.Name).To(MatchRegexp(`^[a-zA-Z0-9_-]{1,64}$`), "valid OpenAI function name")
				Expect(d.Name).To(HavePrefix("addon__tables__"))
				Expect(d.Parameters).To(HaveKeyWithValue("type", "object"))
				Expect(d.Parameters).To(HaveKeyWithValue("additionalProperties", false))
				Expect(d.Description).NotTo(BeEmpty())
			}
			sort.Strings(names)
			Expect(names).To(Equal([]string{
				"addon__tables__columns-add", "addon__tables__columns-drop", "addon__tables__columns-rename",
				"addon__tables__databases-create", "addon__tables__databases-inspect", "addon__tables__databases-rename",
				"addon__tables__rows-aggregate", "addon__tables__rows-delete", "addon__tables__rows-export", "addon__tables__rows-insert",
				"addon__tables__rows-query", "addon__tables__rows-update",
				"addon__tables__tables-create", "addon__tables__tables-drop", "addon__tables__tables-rename",
			}))
		})

		It("offers nothing for a disabled, stopped or failed addon and nothing when the owner cannot answer", func() {
			for _, mutate := range []func(*apps.App){
				func(x *apps.App) { x.Enabled = false },
				func(x *apps.App) { x.State = "stopped" },
				func(x *apps.App) { x.State = "failed" },
				func(x *apps.App) { x.ToolDefinitions = nil },
			} {
				g := newFake()
				mutate(&g.list.Apps[0])
				defs, e := tools.NewToolAdapter(g).Discover(context.Background(), member)
				Expect(e).NotTo(HaveOccurred())
				Expect(defs).To(BeEmpty())
			}
			f.appsErr = gateway.ErrUnavailable
			_, e := a.Discover(context.Background(), member)
			Expect(e).To(MatchError(gateway.ErrUnavailable))
		})

		It("has no tool for sharing, deleting a database, purging retained data or approving", func() {
			defs, e := a.Discover(context.Background(), member)
			Expect(e).NotTo(HaveOccurred())
			banned := regexp.MustCompile(`(?i)share|grant|revoke|delet|purge|confirm|approv|apply|accept|resolve|cancel|yes|owner|install|enable|disable|uninstall|upgrade`)
			for _, d := range defs {
				_, tool, ok := tools.ParseToolName(d.Name)
				Expect(ok).To(BeTrue())
				// rows-delete removes rows through a proposal; it is the only
				// name that may contain "delet".
				if tool == "rows-delete" {
					continue
				}
				Expect(banned.MatchString(tool)).To(BeFalse(), tool)
				for k := range d.Parameters["properties"].(map[string]any) {
					Expect(contract.ForbiddenToolProperty(k)).To(BeFalse(), d.Name+"."+k)
				}
			}
		})

		It("drops a definition whose namespaced name is not a valid function name or does not fit", func() {
			long := apps.App{ID: strings.Repeat("a", 40), Generation: 1, Enabled: true, State: "running",
				ToolDefinitions: []contract.ToolDefinition{{Name: strings.Repeat("t", 30), Description: "x", Kind: "read", InputSchema: args(`{"type":"object","additionalProperties":false,"properties":{"resourceId":{"type":"string"}},"required":["resourceId"]}`)}}}
			f.list.Apps = append(f.list.Apps, long)
			defs, e := a.Discover(context.Background(), member)
			Expect(e).NotTo(HaveOccurred())
			for _, d := range defs {
				Expect(len(d.Name)).To(BeNumerically("<=", 64))
				Expect(d.Name).NotTo(ContainSubstring(long.ID))
			}
		})
	})

	Describe("reads", func() {
		It("reads through the gateway with the immutable actor, the listed generation and the resource taken out of the arguments", func() {
			f.read = contract.Result{Payload: args(`{"table":"t","rows":[]}`)}
			res, e := a.Execute(context.Background(), member, "addon__tables__rows-query", args(`{"resourceId":"db-one","table":"t","limit":5}`))
			Expect(e).NotTo(HaveOccurred())
			Expect(res.Kind).To(Equal(tools.ResultData))
			Expect(string(res.Result)).To(MatchJSON(`{"table":"t","rows":[]}`))
			Expect(f.dispatched).To(HaveLen(1))
			got := f.dispatched[0]
			Expect(got.Actor).To(Equal(member))
			Expect(got.Request.Action).To(Equal("read"))
			Expect(got.Request.AddonID).To(Equal("tables"))
			Expect(got.Request.ResourceID).To(Equal("db-one"))
			Expect(got.Request.Generation).To(Equal(uint64(3)))
			Expect(got.Request.OperationID).To(MatchRegexp(`^[a-z][a-z0-9-]{0,63}$`))
			Expect(string(got.Request.Arguments)).To(MatchJSON(`{"tool":"rows-query","table":"t","limit":5}`))
			Expect(f.proposed).To(BeEmpty())
		})

		It("refuses an oversized result instead of truncating it", func() {
			f.read = contract.Result{Payload: args(`{"x":"` + strings.Repeat("a", tools.MaxResultBytes) + `"}`)}
			_, e := a.Execute(context.Background(), member, "addon__tables__rows-query", args(`{"resourceId":"db-one","table":"t"}`))
			Expect(e).To(MatchError(tools.ErrResultTooLarge))
		})

		It("returns file results as private artifact references with no bytes", func() {
			f.read = contract.Result{Outputs: []contract.OutputDescription{{Handle: "art-1", Name: "rows.csv", MediaType: "text/csv", Size: 12}}}
			res, e := a.Execute(context.Background(), member, "addon__tables__rows-query", args(`{"resourceId":"db-one","table":"t"}`))
			Expect(e).NotTo(HaveOccurred())
			Expect(res.Kind).To(Equal(tools.ResultArtifact))
			Expect(res.Artifacts).To(Equal([]tools.ArtifactRef{{ID: "art-1", Name: "rows.csv", MediaType: "text/csv", Size: 12}}))
			Expect(res.Result).To(BeEmpty())
		})

		It("passes a denial, an outage and an unknown outcome through unchanged", func() {
			for _, want := range []error{gateway.ErrForbidden, gateway.ErrUnavailable, &gateway.OutcomeUnknown{OperationID: "x"}} {
				f.readErr = want
				_, e := a.Execute(context.Background(), member, "addon__tables__databases-inspect", args(`{"resourceId":"db-one"}`))
				Expect(e).To(HaveOccurred())
				Expect(gateway.StatusFor(e)).To(Equal(gateway.StatusFor(want)))
			}
		})
	})

	Describe("refusals of the add-on", func() {
		It("gives the model an error result that says the request was invalid, for a read and for a change", func() {
			for _, refused := range []error{gateway.ErrInvalidRequest, fmt.Errorf("%w: %w", gateway.ErrInvalidRequest, contract.ErrRefused)} {
				f.readErr, f.proposeErr = refused, refused
				res, e := a.Execute(context.Background(), member, "addon__tables__databases-inspect", args(`{"resourceId":"db-one"}`))
				Expect(e).NotTo(HaveOccurred())
				Expect(res.Kind).To(Equal(tools.ResultError))
				Expect(res.Code).To(Equal(tools.CodeInvalidRequest))
				Expect(res.Note).To(ContainSubstring("Nothing was changed"))
				res, e = a.Execute(context.Background(), member, "addon__tables__rows-insert", args(`{"resourceId":"db-one","table":"t","rows":[{}]}`))
				Expect(e).NotTo(HaveOccurred())
				Expect(res.Kind).To(Equal(tools.ResultError))
				Expect(res.Code).To(Equal(tools.CodeInvalidRequest))
				Expect(res.Proposal).To(BeNil())
			}
		})
		It("keeps an outage an error, never the refused result", func() {
			f.readErr = gateway.ErrUnavailable
			_, e := a.Execute(context.Background(), member, "addon__tables__databases-inspect", args(`{"resourceId":"db-one"}`))
			Expect(e).To(HaveOccurred())
		})
	})

	Describe("changes", func() {
		It("turns a row change into a proposal reference and never applies", func() {
			exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
			f.proposal = gateway.Proposal{AddonID: "tables", ResourceID: "db-one", Action: "write", Digest: "d1", State: gateway.ProposalPending, ExpiresAt: exp,
				Effects: gateway.ProposalEffects{Count: 2, Sample: args(`[{"id":1}]`)}}
			res, e := a.Execute(context.Background(), member, "addon__tables__rows-insert", args(`{"resourceId":"db-one","table":"t","rows":[{"a":1},{"a":2}]}`))
			Expect(e).NotTo(HaveOccurred())
			Expect(res.Kind).To(Equal(tools.ResultProposal))
			Expect(res.Proposal).NotTo(BeNil())
			Expect(res.Proposal.ID).To(Equal(f.proposed[0].Request.OperationID))
			Expect(res.Proposal.Digest).To(Equal("d1"))
			Expect(res.Proposal.Count).To(Equal(uint64(2)))
			Expect(res.Proposal.State).To(Equal("pending"))
			Expect(res.Proposal.ExpiresAt).To(Equal(exp))
			Expect(res.Note).To(ContainSubstring("person"))
			Expect(f.dispatched).To(BeEmpty())
			Expect(f.proposed).To(HaveLen(1))
			Expect(f.proposed[0].Actor).To(Equal(member))
			Expect(f.proposed[0].Request.Action).To(Equal("write"))
			Expect(f.proposed[0].Request.ResourceID).To(Equal("db-one"))
			Expect(f.proposed[0].Request.Generation).To(Equal(uint64(3)))
			Expect(string(f.proposed[0].Request.Arguments)).To(MatchJSON(`{"tool":"rows-insert","table":"t","rows":[{"a":1},{"a":2}]}`))
		})

		It("proposes schema changes under the schema action and creation with no resource", func() {
			_, e := a.Execute(context.Background(), member, "addon__tables__tables-drop", args(`{"resourceId":"db-one","table":"t"}`))
			Expect(e).NotTo(HaveOccurred())
			Expect(f.proposed[0].Request.Action).To(Equal("schema"))
			_, e = a.Execute(context.Background(), member, "addon__tables__databases-create", args(`{"name":"Orders"}`))
			Expect(e).NotTo(HaveOccurred())
			Expect(f.proposed[1].Request.Action).To(Equal("create"))
			Expect(f.proposed[1].Request.ResourceID).To(BeEmpty())
			Expect(string(f.proposed[1].Request.Arguments)).To(MatchJSON(`{"tool":"databases-create","name":"Orders"}`))
		})

		It("gives every call its own operation ID so one call can never replay another", func() {
			for i := 0; i < 3; i++ {
				_, e := a.Execute(context.Background(), member, "addon__tables__rows-delete", args(`{"resourceId":"db-one","table":"t","ids":[1]}`))
				Expect(e).NotTo(HaveOccurred())
			}
			ids := map[string]bool{}
			for _, p := range f.proposed {
				ids[p.Request.OperationID] = true
			}
			Expect(ids).To(HaveLen(3))
		})

		It("reports a proposal outage with the typed error and no reference", func() {
			f.proposeErr = &gateway.ProposalUnavailable{ProposalID: "p1"}
			res, e := a.Execute(context.Background(), member, "addon__tables__rows-insert", args(`{"resourceId":"db-one","table":"t","rows":[{}]}`))
			var typed *gateway.ProposalUnavailable
			Expect(errors.As(e, &typed)).To(BeTrue())
			Expect(res.Proposal).To(BeNil())
		})
	})

	Describe("hostile input", func() {
		forged := []string{
			`"actor":"alice"`, `"userId":"alice"`, `"user_id":"alice"`, `"credentialId":"key:1"`, `"role":"owner"`,
			`"mode":"local-owner"`, `"approved":true`, `"Approved":true`, `" approved":true`, `"approve":true`,
			`"confirm":true`, `"confirmed":true`, `"digest":"d"`, `"proposalId":"p1"`, `"proposal_id":"p1"`,
			`"operationId":"op"`, `"generation":9`, `"addonId":"other"`, `"action":"read"`, `"tool":"rows-query"`,
			`"scope":"apply"`, `"authority":{"actor":"a"}`, `"force":true`, `"yes":true`,
		}
		It("rejects forged identity, role, approval and operation fields before anything reaches the owner", func() {
			for _, field := range forged {
				body := `{"resourceId":"db-one","table":"t","rows":[{"a":1}],` + field + `}`
				_, e := a.Execute(context.Background(), member, "addon__tables__rows-insert", args(body))
				Expect(e).To(MatchError(tools.ErrForbiddenArgument), field)
			}
			// The same fields nested in a read.
			for _, field := range forged {
				body := `{"resourceId":"db-one","table":"t",` + field + `}`
				_, e := a.Execute(context.Background(), member, "addon__tables__rows-query", args(body))
				Expect(e).To(MatchError(tools.ErrForbiddenArgument), field)
			}
			Expect(f.calls()).To(BeZero())
		})

		It("rejects approval aliases, undefined tools and names outside the namespace", func() {
			for _, name := range []string{
				"confirm", "approve", "apply_proposal", "yes", "confirm_proposal",
				"addon__tables__confirm", "addon__tables__approve", "addon__tables__apply", "addon__tables__share",
				"addon__tables__databases-delete", "addon__tables__databases-purge", "addon__tables__resolve",
				"addon__tables__resource-exists", "addon__tables__ROWS-QUERY", "addon__tables__rows-query ",
				"addon__other__rows-query", "tables__rows-query", "addon__tables", "addon____rows-query", "",
			} {
				_, e := a.Execute(context.Background(), member, name, args(`{"resourceId":"db-one","table":"t"}`))
				Expect(e).To(MatchError(tools.ErrUnknownTool), name)
			}
			Expect(f.calls()).To(BeZero())
		})

		It("rejects arguments that are not exactly one bounded object with known, typed properties", func() {
			for _, body := range []string{
				``, `null`, `[]`, `"x"`, `{"resourceId":"db-one","table":"t"} {}`, `{"resourceId":"db-one","resourceId":"db-two","table":"t"}`,
				`{"resourceId":"db-one","table":"t","filter":{"column":"a","column":"b"}}`,
				`{"resourceId":"db-one","table":"t","unknown":1}`, `{"table":"t"}`, `{"resourceId":"db-one"}`,
				`{"resourceId":"DB-ONE","table":"t"}`, `{"resourceId":"../x","table":"t"}`, `{"resourceId":"db-one","table":5}`,
				`{"resourceId":"db-one","table":"t","limit":"5"}`, `{"resourceId":"db-one","table":"t","columns":"a"}`,
				`{"resourceId":"db-one","table":"` + strings.Repeat("a", tools.MaxArgumentBytes) + `"}`,
			} {
				_, e := a.Execute(context.Background(), member, "addon__tables__rows-query", args(body))
				Expect(e).To(HaveOccurred(), body)
				Expect(errors.Is(e, tools.ErrInvalidArguments) || errors.Is(e, tools.ErrForbiddenArgument)).To(BeTrue(), body)
			}
			Expect(f.calls()).To(BeZero())
		})
	})

	Describe("the argument limit", func() {
		It("accepts a call whose bound body is exactly 1 MiB, end to end, and refuses one byte more", func() {
			echo := contract.ToolDefinition{Name: "echo", Kind: "read", Description: "x",
				InputSchema: args(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"],"additionalProperties":false}`)}
			g := &fake{list: apps.List{Apps: []apps.App{{ID: "sample", Name: "sample", Generation: 1, Enabled: true, State: "running",
				Tools: []string{"echo"}, ToolDefinitions: []contract.ToolDefinition{echo}}}}}
			ad := tools.NewToolAdapter(g)
			const toolKey = len(`,"tool":"echo"`)
			body := func(n int) json.RawMessage {
				const frame = `{"content":""}`
				return args(`{"content":"` + strings.Repeat("a", n-len(frame)) + `"}`)
			}
			_, e := ad.Execute(context.Background(), member, "addon__sample__echo", body(tools.MaxArgumentBytes-toolKey))
			Expect(e).NotTo(HaveOccurred())
			Expect(g.dispatched).To(HaveLen(1))
			req := g.dispatched[0].Request
			Expect(len(req.Arguments)).To(Equal(tools.MaxArgumentBytes))
			// What the add-on receives passes the contract's own envelope check.
			env := contract.Envelope{Request: req, Authority: contract.Authority{Actor: member, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)}}
			req.Generation = 1
			env.Request = req
			Expect(env.Validate(time.Now(), contract.DefaultLimits())).To(Succeed())
			_, e = ad.Execute(context.Background(), member, "addon__sample__echo", body(tools.MaxArgumentBytes-toolKey+1))
			Expect(errors.Is(e, tools.ErrInvalidArguments)).To(BeTrue())
			Expect(g.dispatched).To(HaveLen(1), "a refused call never reaches the owner")
		})
	})

	Describe("authority", func() {
		It("has no way to confirm, cancel or resolve a proposal", func() {
			var backend tools.Backend
			t := reflect.TypeOf(&backend).Elem()
			methods := []string{}
			for i := 0; i < t.NumMethod(); i++ {
				methods = append(methods, t.Method(i).Name)
			}
			Expect(methods).To(ConsistOf("Apps", "Dispatch", "Propose"))
			at := reflect.TypeOf(a)
			for i := 0; i < at.NumMethod(); i++ {
				Expect(strings.ToLower(at.Method(i).Name)).NotTo(MatchRegexp(`confirm|cancel|resolve|approve|reconcile`))
			}
		})

		It("uses the actor of each call and nothing a previous call left behind", func() {
			other := contract.Actor{UserID: "carol", CredentialID: "key:k9", Mode: "named-user"}
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				who := member
				if i%2 == 1 {
					who = other
				}
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					_, e := a.Execute(context.Background(), who, "addon__tables__rows-query", args(`{"resourceId":"db-one","table":"t"}`))
					Expect(e).NotTo(HaveOccurred())
				}()
			}
			wg.Wait()
			counts := map[string]int{}
			for _, c := range f.dispatched {
				counts[c.Actor.UserID+"/"+c.Actor.CredentialID]++
			}
			Expect(counts).To(Equal(map[string]int{"bob/session:s1": 10, "carol/key:k9": 10}))
		})

		It("refuses an actor without a user or credential before any call", func() {
			for _, bad := range []contract.Actor{{}, {UserID: "bob"}, {CredentialID: "key:1"}} {
				_, e := a.Execute(context.Background(), bad, "addon__tables__rows-query", args(`{"resourceId":"db-one","table":"t"}`))
				Expect(e).To(HaveOccurred())
				_, e = a.Discover(context.Background(), bad)
				Expect(e).To(HaveOccurred())
			}
			Expect(f.calls()).To(BeZero())
		})

		It("serves the real gateway type as its backend", func() {
			var _ tools.Backend = (*gateway.Gateway)(nil)
		})
	})
})
