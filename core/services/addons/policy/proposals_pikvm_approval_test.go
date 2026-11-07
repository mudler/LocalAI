// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// manifestTools serves the tool definitions of a shipped manifest, so the
// spec runs on what the add-on really declares and not on a copy.
type manifestTools struct{ caps contract.Capabilities }

func (m manifestTools) DeclaredTools(context.Context, string, uint64) ([]string, error) {
	return m.caps.Tools, nil
}

func (m manifestTools) DeclaredCapabilities(context.Context, string, uint64) (contract.Capabilities, error) {
	return m.caps, nil
}

func shippedManifest(path string) contract.Manifest {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", path))
	Expect(err).NotTo(HaveOccurred())
	var m contract.Manifest
	Expect(json.Unmarshal(raw, &m)).To(Succeed())
	return m
}

// What the PiKVM add-on declares for its three write tools, run through the
// broker with the person's setting on and off. The tool that types arbitrary
// text can never be relaxed, whatever the setting or the caller says.
var _ = Describe("the confirmation of the PiKVM write tools", Label("proposals", "credentials", "approval", "pikvm"), func() {
	ctx := context.Background()
	who := named("owner")
	const device = "https://pikvm.local"
	args := map[string]string{
		"power-action": `{"tool":"power-action","device":"` + device + `","command":"off"}`,
		"send-key":     `{"tool":"send-key","device":"` + device + `","keys":["escape"]}`,
		"type-text":    `{"tool":"type-text","device":"` + device + `","text":"hunter2"}`,
	}
	manifest := shippedManifest("addons/pikvm/manifest.json")

	DescribeTable("a chat turn of the person, with the setting on skip or ask",
		func(tool string, skip, wantAsk bool) {
			binding := &bindingSource{mode: gateway.WritePropose, repos: []string{device}, token: "kvm-user:kvm-pass"}
			rt := newFixtureRuntime()
			spy := &spyRuntime{fixtureRuntime: rt}
			asked := false
			f := openBrokerFixture(ModeNamedUser, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: spy}, &clock{t: time.Now()}, &account{blocked: map[string]bool{}},
				func(o *BrokerOptions) {
					o.Tools = manifestTools{caps: manifest.Capabilities}
					o.Credentials = binding
					o.ConfirmCredential = func(a contract.Actor) bool {
						return a.CredentialID == "chats" || strings.HasPrefix(a.CredentialID, "session:")
					}
					o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
					// The setting is on skip for every tool, even the one the
					// manifest does not allow it for: the manifest decides.
					o.RelaxApproval = func(context.Context, contract.Actor, string, string) bool { asked = true; return skip }
				})
			DeferCleanup(func() { f.close() })
			req := contract.Request{AddonID: "pikvm", OperationID: "op-1", Generation: 1, Action: "write", Arguments: json.RawMessage(args[tool])}
			p, err := f.broker.Propose(ctx, who, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.AwaitsPerson).To(Equal(wantAsk))
			Expect(asked).To(Equal(tool != "type-text"), "the setting is asked for the tools the manifest lets the person relax, and only those")
			_, err = f.broker.Confirm(ctx, chat("owner"), "op-1")
			if wantAsk {
				Expect(err).To(MatchError(ErrForbidden))
				Expect(spy.applies).To(BeEmpty())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(spy.applies).To(HaveLen(1))
		},
		Entry("power-action, don't ask", "power-action", true, false),
		Entry("power-action, ask each time", "power-action", false, true),
		Entry("send-key, don't ask", "send-key", true, false),
		Entry("send-key, ask each time", "send-key", false, true),
		Entry("type-text, even when the host would say skip", "type-text", true, true),
		Entry("type-text, ask each time", "type-text", false, true),
	)

	It("declares the setting of each relaxable tool with the default ask, and none for type-text", func() {
		Expect(contract.ValidateManifest(manifest, contract.HostCompatibility{Host: 1, API: 2, Protocol: 1, SDK: 2, DataFormat: 1, OS: "linux", Arch: "amd64"})).To(Succeed())
		for _, d := range manifest.Capabilities.ToolDefinitions {
			_, has := contract.UserSettingOf(manifest.Capabilities.UserSettings, contract.ApprovalSettingKey(d.Name))
			switch d.Name {
			case "power-action", "send-key":
				Expect(d.ApprovalOverridable).To(BeTrue(), d.Name)
				Expect(has).To(BeTrue(), d.Name)
			default:
				Expect(d.ApprovalOverridable).To(BeFalse(), d.Name)
				Expect(has).To(BeFalse(), d.Name)
			}
		}
	})
})
