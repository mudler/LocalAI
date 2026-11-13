// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Only registration is exercised: no workspace method or handler may run.
type admissionWorkspaceStore struct{ workspace.Store }
type admissionInbox struct{}

func (admissionInbox) Ingest(context.Context, string, contract.ConnectorInbound) error {
	panic("admission parity must not invoke handlers")
}

type admissionClientJobs struct{ ClientJobHost }
type admissionClientLister struct{ ClientLister }
type admissionGrants struct{ ClientGrantSource }

func registeredBuiltinOperations() []string {
	b := New(NewInFlight(), nil, contract.DefaultLimits())
	Expect(RegisterEngineJobs(b, &fakeFT{}, &fakeQ{}, backends{})).To(Succeed())
	Expect(RegisterEngineDatasets(b, &fakeDatasets{})).To(Succeed())
	Expect(RegisterEngineModels(b, &fakeExporter{}, &fakeImporter{})).To(Succeed())
	p := NewProgresser(newFakeSource())
	Expect(RegisterEngineProgress(b, p, p)).To(Succeed())
	Expect(RegisterWorkspace(b, &WorkspaceHost{
		Store:     &admissionWorkspaceStore{},
		Principal: func(contract.Actor) string { panic("registration only") },
		DataDir:   func(string) string { panic("registration only") },
		Access:    func(context.Context, string, string) (session.Access, error) { panic("registration only") },
	})).To(Succeed())
	Expect(RegisterEgress(b, &fakeEgress{})).To(Succeed())
	Expect(RegisterConnector(b, admissionInbox{})).To(Succeed())
	Expect(RegisterConnectorTransfer(b, NewTransfers(nil))).To(Succeed())
	Expect(RegisterClientJobs(b, admissionClientJobs{})).To(Succeed())
	Expect(RegisterClientList(b, admissionClientLister{})).To(Succeed())
	Expect(RegisterClientFolders(b, &ClientFolderHost{Files: files.New(&admissionWorkspaceStore{}), Grants: admissionGrants{}})).To(Succeed())
	// Each registration error is checked above, so a duplicate cannot be hidden
	// by the broker's map. Ops includes dynamic progress/stop and inbound specs.
	return b.Ops()
}

var _ = Describe("Host operation request admission", func() {
	It("matches the supported vocabulary exactly to every real builtin registration", func() {
		registered := registeredBuiltinOperations()
		supported := SupportedOperations()
		for _, names := range [][]string{registered, supported} {
			Expect(names).NotTo(BeEmpty())
			seen := map[string]bool{}
			for _, name := range names {
				Expect(strings.TrimSpace(name)).NotTo(BeEmpty())
				Expect(seen[name]).To(BeFalse(), "duplicate operation %q", name)
				seen[name] = true
			}
		}
		Expect(supported).To(ConsistOf(registered), "startup admission must match the case-sensitive runtime registry")
	})
	It("lists the client job operations as supported, so a manifest that requests them is admitted", func() {
		ops := []string{"client.jobs.start", "client.jobs.get", "client.jobs.cancel"}
		Expect(SupportedOperations()).To(ContainElements(ops))
		s := Requests(&contract.HostOperations{Required: ops}, SupportedOperations(), nil)
		Expect(s.MissingRequired).To(ConsistOf(ops), "requested and supported, not yet granted")
		Expect(Requests(&contract.HostOperations{Required: ops}, SupportedOperations(), []string{"client.jobs."}).Granted).To(ConsistOf(ops))
	})
	It("lists client.list as supported with a description, so a manifest that requests it is admitted", func() {
		Expect(SupportedOperations()).To(ContainElement("client.list"))
		_, text, risk, ok := DescribeOperation("client.list")
		Expect(ok).To(BeTrue())
		Expect(text).NotTo(BeEmpty())
		Expect(risk).To(Equal(RiskRead))
		ops := &contract.HostOperations{Required: []string{"client.list"}}
		Expect(Requests(ops, SupportedOperations(), []string{"client."}).Granted).To(ConsistOf("client.list"))
		Expect(Requests(ops, SupportedOperations(), []string{"client.jobs."}).MissingRequired).To(ConsistOf("client.list"))
	})
	It("lists the client folder operations as supported with descriptions", func() {
		ops := []string{"client.workspace.list", "client.workspace.read", "client.workspace.write"}
		Expect(SupportedOperations()).To(ContainElements(ops))
		for _, op := range ops {
			_, text, _, ok := DescribeOperation(op)
			Expect(ok).To(BeTrue(), op)
			Expect(text).NotTo(BeEmpty(), op)
		}
		_, _, risk, _ := DescribeOperation("client.workspace.write")
		Expect(risk).To(Equal(RiskChangesData))
		Expect(Requests(&contract.HostOperations{Required: ops}, SupportedOperations(), []string{"client.workspace."}).Granted).To(ConsistOf(ops))
	})
	It("detects missing extra duplicate and case-changed operations with the parity oracle", func() {
		registered := registeredBuiltinOperations()
		Expect(registered).NotTo(BeEmpty())
		oracle := ConsistOf(registered)
		for _, mutated := range [][]string{
			slices.Clone(registered[1:]),
			append(slices.Clone(registered), "parity.negative.control"),
			append(slices.Clone(registered), registered[0]),
			append([]string{strings.ToUpper(registered[0])}, registered[1:]...),
		} {
			matches, err := oracle.Match(mutated)
			Expect(err).NotTo(HaveOccurred())
			Expect(matches).To(BeFalse(), "parity oracle accepted mutated vocabulary %v", mutated)
		}
	})
	It("uses registered exact names and literal nonempty grant prefixes without self grants", func() {
		req := &contract.HostOperations{Required: []string{"workspace.materialize", "future.op"}, Optional: []string{"egress.check"}}
		s := Requests(req, []string{"workspace.materialize", "egress.check"}, []string{"workspace."})
		Expect(s.MissingRequired).To(Equal([]string{"future.op"}))
		Expect(s.MissingOptional).To(Equal([]string{"egress.check"}))
		Expect(s.Granted).To(Equal([]string{"workspace.materialize"}))
		Expect(Requests(req, []string{"workspace.materialize"}, []string{""}).MissingRequired).To(HaveLen(2))
		Expect(Requests(&contract.HostOperations{Required: []string{"workspace.materialize"}}, []string{"workspace.materialize"}, []string{"workspace.materialize.narrow."}).MissingRequired).To(HaveLen(1))
		Expect(Requests(nil, nil, nil).MissingRequired).To(BeEmpty())
	})
	It("permits missing optional requests and bounds diagnostics to declaration names", func() {
		req := &contract.HostOperations{Optional: []string{"egress.check", "future.op"}}
		s := Requests(req, SupportedOperations(), []string{"egress."})
		Expect(s.MissingRequired).To(BeEmpty())
		Expect(s.MissingOptional).To(Equal([]string{"future.op"}))
		Expect(s.OptionalCount).To(Equal(2))
		Expect(s.GrantedCount).To(Equal(1))
	})
})
