// SPDX-License-Identifier: MIT
package ssh_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mudler/LocalAI/addons/ssh/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestManifest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SSH add-on manifest")
}

var _ = Describe("the manifest", func() {
	var m contract.Manifest
	BeforeEach(func() {
		raw, err := os.ReadFile("manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err = contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
	})

	It("declares exactly the tools the runtime serves", func() {
		Expect(m.Capabilities.Tools).To(ConsistOf(tools.Declared()))
	})

	It("leaves the confirmation of run to the write mode of the connection", func() {
		var run *contract.ToolDefinition
		for i := range m.Capabilities.ToolDefinitions {
			if m.Capabilities.ToolDefinitions[i].Name == tools.ToolRun {
				run = &m.Capabilities.ToolDefinitions[i]
			}
		}
		Expect(run).NotTo(BeNil())
		Expect(run.Kind).To(Equal(contract.ToolKindWrite))
		Expect(run.Approval).To(BeEmpty(), "an approval of always would hide the per-connection mode")
		Expect(run.ApprovalOverridable).To(BeFalse())
		Expect(m.Capabilities.UserSettings).To(BeEmpty(), "the mode is the host's write mode, not a setting the model could reach")
	})

	It("names the host argument on every tool that uses the credential, and bounds run", func() {
		for _, d := range m.Capabilities.ToolDefinitions {
			var s struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			}
			Expect(json.Unmarshal(d.InputSchema, &s)).To(Succeed())
			if d.UIOnly {
				Expect(s.Properties).NotTo(HaveKey("host"), d.Name)
				continue
			}
			Expect(s.Required).To(ContainElement("host"), d.Name)
			Expect(s.Properties).NotTo(HaveKey("env"), d.Name)
			Expect(s.Properties).NotTo(HaveKey("stdin"), d.Name)
			if d.Name == tools.ToolRun {
				var t struct {
					Maximum int `json:"maximum"`
				}
				Expect(json.Unmarshal(s.Properties["timeoutSeconds"], &t)).To(Succeed())
				Expect(t.Maximum).To(Equal(tools.MaxTimeout))
				var c struct {
					MaxLength int `json:"maxLength"`
				}
				Expect(json.Unmarshal(s.Properties["command"], &c)).To(Succeed())
				Expect(c.MaxLength).To(Equal(tools.MaxCommandLength))
			}
		}
	})

	It("keeps the credential slot on the token kind with the host as its resource", func() {
		Expect(m.Capabilities.Credentials).To(HaveLen(1))
		cr := m.Capabilities.Credentials[0]
		Expect(cr.Kind).To(Equal("token"))
		Expect(cr.ResourceArgument).To(Equal("host"))
	})
})
