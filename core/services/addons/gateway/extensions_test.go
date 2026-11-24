// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("typed installed Apps carrier", func() {
	It("round trips installed typed home metadata over mTLS with owner actor visibility and immutable asset URLs", func() {
		ctx := context.Background()
		admin := contract.Actor{UserID: "admin", CredentialID: "key:admin", Mode: "named-user"}
		reader := contract.Actor{UserID: "reader", CredentialID: "key:reader", Mode: "named-user"}
		var manifest contract.Manifest
		Expect(json.Unmarshal([]byte(uiManifest), &manifest)).To(Succeed())
		caps := contract.Capabilities{Service: true, Actions: true, Tools: []string{"run", "health"}, ToolDefinitions: []contract.ToolDefinition{
			{Name: "run", Description: "Run", Kind: "action", Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)},
			{Name: "health", Description: "Health", Kind: "read", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
		}, UI: &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 2, Max: 2}, Pages: []string{"home", "history"}, MainPage: "home", Title: &contract.Localized{ByLanguage: map[string]string{"en": "Notes", "it": "Note"}}, Icon: "icon.png", HomeCommands: []contract.HomeCommand{
			{ID: "open", Alias: "notes"},
			{ID: "history", Page: "history", Label: &contract.Localized{ByLanguage: map[string]string{"en": "History", "it": "Cronologia"}}, Icon: "clock"},
		}}, ExecutionProvider: &contract.ExecutionProvider{Tools: []string{"run"}, StatusTool: "health"}, HostOperations: &contract.HostOperations{Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}}}
		manifest.Capabilities = caps
		raw, e := json.Marshal(manifest)
		Expect(e).NotTo(HaveOccurred())
		m := ownerManagerPackage(admin, true, string(raw), map[string]string{"icon.png": "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 100)}, 2, map[string][]string{"sample": {"workspace."}})
		status, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Generations[0].Receipt.Manifest.Capabilities).To(Equal(caps))
		// Status is the actual manifest-bearing DTO; its JSON copy must retain
		// all declarations, including nested command labels, without aliases.
		statusJSON, e := json.Marshal(status)
		Expect(e).NotTo(HaveOccurred())
		var decoded addons.Status
		Expect(json.Unmarshal(statusJSON, &decoded)).To(Succeed())
		Expect(decoded.Generations[0].Receipt.Manifest.Capabilities).To(Equal(caps))
		copied := &status.Generations[0].Receipt.Manifest.Capabilities
		copied.UI.HomeCommands[0].Alias = "mutated"
		copied.UI.HomeCommands[1].Label.ByLanguage["it"] = "mutated"
		copied.ExecutionProvider.Tools[0] = "mutated"
		copied.HostOperations.Required[0] = "mutated"
		copied.HostOperations.Optional[0] = "mutated"
		status, e = m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Generations[0].Receipt.Manifest.Capabilities).To(Equal(caps))

		allow := func(context.Context, contract.Actor) error { return nil }
		owner := &Gateway{Manager: m, Revalidate: allow}
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		_, e = m.Submit(ctx, admin, addons.LifecycleCommand{OperationID: "typed-enable", AddonID: "sample", Action: "enable"})
		Expect(e).NotTo(HaveOccurred())
		for _, a := range []contract.Actor{admin, reader} {
			local, e := owner.Apps(ctx, a)
			Expect(e).NotTo(HaveOccurred())
			Expect(local.Apps).To(HaveLen(1))
			for _, id := range []string{"front-a", "front-b"} {
				front := &Gateway{Forward: cs[id], Revalidate: allow}
				remote, e := front.Apps(ctx, a)
				Expect(e).NotTo(HaveOccurred())
				localJSON, err := json.Marshal(local)
				Expect(err).NotTo(HaveOccurred())
				remoteJSON, err := json.Marshal(remote)
				Expect(err).NotTo(HaveOccurred())
				Expect(remoteJSON).To(MatchJSON(localJSON))
				app := remote.Apps[0]
				Expect(app.Generation).To(Equal(uint64(1)))
				Expect(app.UI).NotTo(BeNil())
				commands := app.UI.HomeCommands
				Expect(commands).To(HaveLen(2))
				Expect(commands[0].ID).To(Equal("open"))
				Expect(commands[0].Alias).To(Equal("notes"))
				Expect(commands[0].Page).To(Equal("home"))
				Expect(commands[0].Labels).To(Equal(caps.UI.Title.ByLanguage))
				Expect(commands[0].Label).To(Equal("Notes"))
				Expect(commands[0].Icon).To(HavePrefix("/api/addons/sample/assets/"))
				Expect(commands[0].Icon).To(HaveSuffix("/icon.png"))
				digestPath := strings.TrimSuffix(strings.TrimPrefix(commands[0].Icon, "/api/addons/sample/assets/"), "/icon.png")
				Expect(digestPath).To(MatchRegexp(`^[a-f0-9]{64}$`))
				Expect(commands[1].Page).To(Equal("history"))
				Expect(commands[1].Labels).To(Equal(caps.UI.HomeCommands[1].Label.ByLanguage))
				Expect(commands[1].Icon).To(Equal("clock"))
				Expect(app.Admin != nil).To(Equal(a == admin))
				commands[0].Alias = "mutated"
				commands[0].Labels["it"] = "mutated"
				commands[1].Labels["it"] = "mutated"
				fresh, e := owner.Apps(ctx, a)
				Expect(e).NotTo(HaveOccurred())
				Expect(fresh.Apps[0].UI.HomeCommands).To(Equal(local.Apps[0].UI.HomeCommands))
				b, e := json.Marshal(remote)
				Expect(e).NotTo(HaveOccurred())
				Expect(string(b)).NotTo(ContainSubstring("directory"))
				Expect(string(b)).NotTo(ContainSubstring("executionProvider"))
				if a != admin {
					Expect(string(b)).NotTo(ContainSubstring("hostOperations"))
				} else {
					Expect(app.Admin.HostOperations.MissingOptional).To(Equal([]string{"egress.check"}))
				}
			}
		}
		_, e = m.Submit(ctx, admin, addons.LifecycleCommand{OperationID: "typed-disable", AddonID: "sample", Action: "disable"})
		Expect(e).NotTo(HaveOccurred())
		for _, a := range []contract.Actor{admin, reader} {
			local, e := owner.Apps(ctx, a)
			Expect(e).NotTo(HaveOccurred())
			remote, e := (&Gateway{Forward: cs["front-a"], Revalidate: allow}).Apps(ctx, a)
			Expect(e).NotTo(HaveOccurred())
			localJSON, err := json.Marshal(local)
			Expect(err).NotTo(HaveOccurred())
			remoteJSON, err := json.Marshal(remote)
			Expect(err).NotTo(HaveOccurred())
			Expect(remoteJSON).To(MatchJSON(localJSON))
			if a == admin {
				Expect(remote.Apps).To(HaveLen(1))
				Expect(remote.Apps[0].UI).To(BeNil())
				Expect(remote.Apps[0].Enabled).To(BeFalse())
				Expect(remote.Apps[0].Generation).To(Equal(uint64(1)))
			} else {
				Expect(remote.Apps).To(BeEmpty())
			}
		}
		// Disabled means installed, not absent. Apps intentionally carries UI
		// projection, not execution or host-operation authority.
		installed, e := m.Installed(ctx, admin)
		Expect(e).NotTo(HaveOccurred())
		Expect(installed).To(HaveLen(1))
		status, e = m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Generations[0].Receipt.Manifest.Capabilities).To(Equal(caps))
	})
})
