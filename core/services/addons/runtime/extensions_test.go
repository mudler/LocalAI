//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func typedCapabilities() contract.Capabilities {
	return contract.Capabilities{Service: true, Actions: true, Tools: []string{"run", "health"}, ToolDefinitions: []contract.ToolDefinition{{Name: "run", Description: "Run", Kind: "action", Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)}, {Name: "health", Description: "Status", Kind: "read", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}, UI: &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 2, Max: 2}, Pages: []string{"home"}, MainPage: "home", HomeCommands: []contract.HomeCommand{{ID: "open"}}, ModelActions: []contract.ModelAction{{Capability: "FLAG_IMAGE", Page: "home", Path: "images"}}}, ExecutionProvider: &contract.ExecutionProvider{Tools: []string{"run"}, StatusTool: "health"}, HostOperations: &contract.HostOperations{Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}}}
}

var _ = Describe("typed runtime declarations", func() {
	It("requires exact typed capability equality on authenticated registration health", func() {
		original := typedCapabilities()
		for _, change := range []func(*contract.Capabilities){func(c *contract.Capabilities) { c.UI.ModelActions[0].Path = "changed" }, func(c *contract.Capabilities) { c.UI.ModelActions = nil }, func(c *contract.Capabilities) { c.UI.HomeCommands[0].ID = "changed" }, func(c *contract.Capabilities) { c.ExecutionProvider.StatusTool = "other" }, func(c *contract.Capabilities) { c.HostOperations.Required = []string{"other.op"} }, func(c *contract.Capabilities) { c.UI.HomeCommands = nil }, func(c *contract.Capabilities) { c.ExecutionProvider = nil }, func(c *contract.Capabilities) { c.HostOperations = nil }} {
			var echo Registration
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer " + strings.Repeat("a", 64)))
				Expect(r.Header.Get("X-LocalAI-Incarnation")).To(Equal("inc"))
				Expect(json.NewEncoder(w).Encode(echo)).To(Succeed())
			}))
			expected := Registration{AddonID: "sample", Generation: 7, Incarnation: "inc", Protocol: 1, Endpoint: srv.URL, Capabilities: original}
			client, e := newClient(expected, strings.Repeat("a", 64), contract.DefaultLimits())
			Expect(e).NotTo(HaveOccurred())
			echo = expected
			Expect(client.health(context.Background(), expected)).To(Succeed())
			b, e := json.Marshal(expected)
			Expect(e).NotTo(HaveOccurred())
			Expect(json.Unmarshal(b, &echo)).To(Succeed())
			change(&echo.Capabilities)
			Expect(client.health(context.Background(), expected)).To(MatchError(ErrRuntime))
			srv.Close()
		}
	})
	It("keeps all typed declarations on automatic crash restart with a fresh incarnation", func() {
		s := supervisor()
		caps := typedCapabilities()
		i, e := s.Start(context.Background(), fixture("normal", caps), 9)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Process.Kill()).To(Succeed())
		Eventually(func() string {
			regs, e := s.Registry.Snapshot(context.Background())
			if e != nil || len(regs) != 1 {
				return ""
			}
			return regs[0].Incarnation
		}, 3*time.Second).Should(And(Not(BeEmpty()), Not(Equal(i.Incarnation))))
		regs, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(regs).To(HaveLen(1))
		Expect(regs[0].Capabilities).To(Equal(caps))
		regs[0].Capabilities.UI.ModelActions[0].Path = "mutated"
		fresh, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(fresh[0].Capabilities).To(Equal(caps))
		Expect(regs[0].Generation).To(Equal(uint64(9)))
		current, e := s.Current(context.Background(), "sample", 9)
		Expect(e).NotTo(HaveOccurred())
		Expect(current.Client.(*Client).health(context.Background(), fresh[0])).To(Succeed())
	})

	It("replays typed and legacy receipts across process restarts", func() {
		s := supervisor()
		caps := typedCapabilities()
		prior := fixture("normal", caps)
		for _, p := range []packages.PackageReceipt{prior, fixture("normal", service), prior} {
			b, e := os.ReadFile(filepath.Join(p.Directory, "receipt.json"))
			Expect(e).NotTo(HaveOccurred())
			var loaded packages.PackageReceipt
			Expect(json.Unmarshal(b, &loaded)).To(Succeed())
			Expect(loaded).To(Equal(p))
			i, e := s.Start(context.Background(), loaded, 7)
			Expect(e).NotTo(HaveOccurred())
			first := i.Incarnation
			regs, e := s.Registry.Snapshot(context.Background())
			Expect(e).NotTo(HaveOccurred())
			Expect(regs).To(HaveLen(1))
			Expect(regs[0].Capabilities).To(Equal(p.Manifest.Capabilities))
			Expect(regs[0].Generation).To(Equal(uint64(7)))
			Expect(s.Stop(context.Background(), i)).To(Succeed())
			i, e = s.Start(context.Background(), loaded, 7)
			Expect(e).NotTo(HaveOccurred())
			Expect(i.Incarnation).NotTo(Equal(first))
			regs, e = s.Registry.Snapshot(context.Background())
			Expect(e).NotTo(HaveOccurred())
			Expect(regs[0].Capabilities).To(Equal(p.Manifest.Capabilities))
			Expect(s.Stop(context.Background(), i)).To(Succeed())
		}
	})
})

var _ = Describe("supervisor observation capture ordering", func() {
	It("cannot observe a restarted same-generation process through the captured client", func() {
		ctx := context.Background()
		s := supervisor()
		p := fixture("normal", typedCapabilities())
		old, e := s.Start(ctx, p, 9)
		Expect(e).NotTo(HaveOccurred())
		captured, e := s.Current(ctx, "sample", 9)
		Expect(e).NotTo(HaveOccurred())
		client := captured.Client.(*Client)
		release, e := client.AdmitObservation(captured.Generation, captured.Incarnation)
		Expect(e).NotTo(HaveOccurred())
		release()
		Expect(s.Stop(ctx, old)).To(Succeed())
		replacement, e := s.Start(ctx, p, 9)
		Expect(e).NotTo(HaveOccurred())
		Expect(replacement.Incarnation).NotTo(Equal(captured.Incarnation))
		Expect(replacement.Client).NotTo(BeIdenticalTo(captured.Client))
		_, e = client.AdmitObservation(9, captured.Incarnation)
		Expect(e).To(HaveOccurred())
		env := contract.Envelope{Request: contract.Request{AddonID: "sample", Generation: 9, OperationID: "old-observation", Action: "read", Arguments: json.RawMessage(`{"tool":"health"}`)}, Authority: contract.Authority{Actor: contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}, Scope: "read", ExpiresAt: time.Now().Add(time.Second)}}
		value, e := client.ReadObservation(ctx, env, "q")
		Expect(e).To(MatchError(ErrNotSent))
		Expect(value.Readiness).To(Equal("unknown"))
		_, e = replacement.Client.(*Client).AdmitObservation(9, captured.Incarnation)
		Expect(e).To(HaveOccurred())
	})
})
