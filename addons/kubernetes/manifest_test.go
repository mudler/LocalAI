// SPDX-License-Identifier: MIT
package kubernetes_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestManifest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubernetes add-on manifest")
}

type prop struct {
	Type     string `json:"type"`
	Maximum  int    `json:"maximum"`
	MaxItems int    `json:"maxItems"`
}

type schema struct {
	Required   []string        `json:"required"`
	Properties map[string]prop `json:"properties"`
}

var _ = Describe("the manifest", func() {
	var m contract.Manifest
	BeforeEach(func() {
		raw, err := os.ReadFile("manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err = contract.ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
	})

	def := func(name string) contract.ToolDefinition {
		for _, d := range m.Capabilities.ToolDefinitions {
			if d.Name == name {
				return d
			}
		}
		Fail("no tool " + name)
		return contract.ToolDefinition{}
	}
	schemaOf := func(d contract.ToolDefinition) schema {
		var s schema
		Expect(json.Unmarshal(d.InputSchema, &s)).To(Succeed())
		return s
	}

	It("declares exactly the tools the runtime serves", func() {
		Expect(m.Capabilities.Tools).To(ConsistOf(tools.Declared()))
		Expect(tools.WriteTools()).To(HaveLen(10))
	})

	It("asks for every write tool by default and lets the person relax each one", func() {
		for _, name := range tools.WriteTools() {
			d := def(name)
			Expect(d.Kind).To(Equal(contract.ToolKindWrite), name)
			Expect(d.Approval).To(Equal(contract.ApprovalAlways), name)
			Expect(d.ApprovalOverridable).To(BeTrue(), name)
			Expect(d.Title).NotTo(BeEmpty(), name)
			s, ok := contract.UserSettingOf(m.Capabilities.UserSettings, contract.ApprovalSettingKey(name))
			Expect(ok).To(BeTrue(), name)
			Expect(s.Default).To(Equal(contract.ApprovalAsk), name)
			Expect(s.Values).To(ConsistOf(contract.ApprovalAsk, contract.ApprovalSkip), name)
		}
	})

	It("keeps the credential slot on cluster, optional, token kind", func() {
		Expect(m.Capabilities.Credentials).To(HaveLen(1))
		cr := m.Capabilities.Credentials[0]
		Expect(cr.Kind).To(Equal(contract.CredentialKindToken))
		Expect(cr.ResourceArgument).To(Equal(tools.ResourceArg))
		Expect(cr.ResourceOptional).To(BeTrue())
	})

	It("never requires cluster and never declares resourceId", func() {
		for _, d := range m.Capabilities.ToolDefinitions {
			s := schemaOf(d)
			Expect(s.Required).NotTo(ContainElement(tools.ResourceArg), d.Name)
			Expect(s.Properties).NotTo(HaveKey(contract.ResourceArgument), d.Name)
			if d.UIOnly {
				Expect(s.Properties).NotTo(HaveKey(tools.ResourceArg), d.Name)
			} else {
				Expect(s.Properties).To(HaveKey(tools.ResourceArg), d.Name)
			}
		}
	})

	It("bounds exec and drain", func() {
		e := schemaOf(def(tools.ToolExec))
		Expect(e.Properties["timeoutSeconds"].Maximum).To(Equal(tools.MaxExecTimeout))
		Expect(e.Properties["command"].MaxItems).To(Equal(32))
		d := schemaOf(def(tools.ToolDrain))
		Expect(d.Properties["timeoutSeconds"].Maximum).To(Equal(tools.MaxDrainTimeout))
	})

	It("tells the model that cluster output is data", func() {
		for _, d := range m.Capabilities.ToolDefinitions {
			if !d.UIOnly {
				Expect(strings.HasSuffix(d.Description, tools.Notice)).To(BeTrue(), d.Name)
			}
		}
	})

	It("fits the host bounds with headroom", func() {
		total := 0
		for _, d := range m.Capabilities.ToolDefinitions {
			total += len(d.InputSchema) + len(d.Description)
		}
		Expect(total).To(BeNumerically("<", 28<<10))
	})
})
