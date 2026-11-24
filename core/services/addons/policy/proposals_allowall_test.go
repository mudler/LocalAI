// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The write mode auto is shown as "Allow all". These cases run the shipped
// manifests through the broker: a write waits for a person in propose mode,
// runs at once in auto mode. No write tool of these add-ons keeps a hard rule.
var _ = Describe("the Allow all write mode on the shipped add-ons", Label("proposals", "credentials", "approval", "allowall"), func() {
	ctx := context.Background()
	who := named("owner")

	type tool struct{ name, args string }
	cases := map[string]struct {
		resource string
		tools    []tool
	}{
		"mail": {resource: "ann@example.com", tools: []tool{
			{"send-message", `{"tool":"send-message","mailbox":"ann@example.com","to":["bob@example.org"],"subject":"Hi","body":"Hello"}`},
			{"save-draft", `{"tool":"save-draft","mailbox":"ann@example.com","to":["bob@example.org"],"subject":"Hi","body":"Hello"}`},
		}},
		"gmail": {resource: "Work", tools: []tool{
			{"create-draft", `{"tool":"create-draft","label":"Work","to":["bob@example.org"],"body":"Hello"}`},
			{"send-draft", `{"tool":"send-draft","label":"Work","draftId":"d1"}`},
		}},
		"gdrive": {resource: "folder1", tools: []tool{
			{"create-file", `{"tool":"create-file","folder":"folder1","name":"a.txt"}`},
			{"update-file", `{"tool":"update-file","folder":"folder1","fileId":"f1","name":"b.txt"}`},
			{"move-file", `{"tool":"move-file","folder":"folder1","fileId":"f1","newParent":"p1"}`},
			{"trash-file", `{"tool":"trash-file","folder":"folder1","fileId":"f1"}`},
		}},
		"pikvm": {resource: "https://pikvm.local", tools: []tool{
			{"power-action", `{"tool":"power-action","device":"https://pikvm.local","command":"off"}`},
			{"send-key", `{"tool":"send-key","device":"https://pikvm.local","keys":["escape"]}`},
			{"type-text", `{"tool":"type-text","device":"https://pikvm.local","text":"hunter2"}`},
		}},
	}

	open := func(addon, mode string) (*brokerFixture, *spyRuntime) {
		c := cases[addon]
		manifest := shippedManifest("addons/" + addon + "/manifest.json")
		binding := &bindingSource{mode: mode, repos: []string{c.resource}, token: "user:secret"}
		rt := newFixtureRuntime()
		spy := &spyRuntime{fixtureRuntime: rt}
		f := openBrokerFixture(ModeNamedUser, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: spy}, &clock{t: time.Now()}, &account{blocked: map[string]bool{}},
			func(o *BrokerOptions) {
				o.Tools = manifestTools{caps: manifest.Capabilities}
				o.Credentials = binding
				o.ConfirmCredential = func(a contract.Actor) bool {
					return a.CredentialID == "chats" || strings.HasPrefix(a.CredentialID, "session:")
				}
				o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
			})
		DeferCleanup(func() { f.close() })
		return f, spy
	}
	propose := func(f *brokerFixture, addon string, t tool) (Proposal, error) {
		return f.broker.Propose(ctx, who, contract.Request{AddonID: addon, OperationID: "op-1", Generation: 1, Action: "write", Arguments: json.RawMessage(t.args)})
	}

	for addon, c := range cases {
		addon, c := addon, c
		for _, t := range c.tools {
			t := t
			It(addon+"/"+t.name+": waits for a person in propose mode", func() {
				f, spy := open(addon, gateway.WritePropose)
				p, err := propose(f, addon, t)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.AwaitsPerson).To(BeTrue())
				_, err = f.broker.Confirm(ctx, chat("owner"), "op-1")
				Expect(err).To(MatchError(ErrForbidden))
				Expect(spy.applies).To(BeEmpty())
			})
			It(addon+"/"+t.name+": runs from a chat turn in Allow all, and the default is not Allow all", func() {
				f, spy := open(addon, gateway.WriteAuto)
				p, err := propose(f, addon, t)
				Expect(err).NotTo(HaveOccurred())
				Expect(p.AwaitsPerson).To(BeFalse())
				_, err = f.broker.Confirm(ctx, chat("owner"), "op-1")
				Expect(err).NotTo(HaveOccurred())
				Expect(spy.applies).To(HaveLen(1))
			})
		}
	}

	It("keeps no hard approval on any write tool of these add-ons", func() {
		for addon := range cases {
			for _, d := range shippedManifest("addons/" + addon + "/manifest.json").Capabilities.ToolDefinitions {
				if d.Kind == contract.ToolKindWrite {
					Expect(d.HardApproval()).To(BeFalse(), addon+"/"+d.Name)
				}
			}
		}
	})

	It("refuses every write in off mode", func() {
		for addon, c := range cases {
			f, spy := open(addon, gateway.WriteOff)
			_, err := propose(f, addon, c.tools[0])
			Expect(err).To(HaveOccurred(), addon)
			Expect(spy.applies).To(BeEmpty())
		}
	})
})
