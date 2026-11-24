// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Extension canonical wire keys", func() {
	DescribeTable("rejects status aliases without partial trust", func(raw string) {
		v, err := DecodeExecutionStatus([]byte(raw))
		Expect(err).To(HaveOccurred())
		Expect(v).To(Equal(ExecutionStatus{Readiness: "unknown", Isolation: "unknown"}))
	},
		Entry("Explanation markup", `{"readiness":"available","isolation":"host","Explanation":"<b>unsafe</b>"}`),
		Entry("Readiness override", `{"readiness":"unknown","Readiness":"available","isolation":"host"}`),
		Entry("uppercase enums", `{"READINESS":"available","ISOLATION":"host"}`),
		Entry("Explanation control", `{"readiness":"available","isolation":"host","Explanation":"a\nb"}`),
		Entry("Explanation oversized", `{"readiness":"available","isolation":"host","Explanation":"`+strings.Repeat("x", 201)+`"}`),
		Entry("benign explanation alias", `{"readiness":"available","isolation":"host","EXPLANATION":"Ready"}`),
		Entry("isolation override", `{"readiness":"available","isolation":"unknown","Isolation":"host"}`),
	)
	DescribeTable("rejects declaration aliases in manifests and direct capability echoes", func(old, replacement string) {
		raw := strings.Replace(string(extensionFixture()), old, replacement, 1)
		Expect(raw).NotTo(Equal(string(extensionFixture())))
		_, err := ParseManifest([]byte(raw))
		Expect(err).To(HaveOccurred())
		var manifest map[string]json.RawMessage
		Expect(json.Unmarshal([]byte(raw), &manifest)).To(Succeed())
		var c Capabilities
		Expect(json.Unmarshal(manifest["capabilities"], &c)).NotTo(Succeed())
	},
		Entry("Alias empty", `"alias":"work"`, `"Alias":""`),
		Entry("Label null", `"label":{"en":"Open work","it":"Apri"}`, `"Label":null`),
		Entry("ID override", `"id":"open"`, `"id":"ignored","ID":"open"`),
		Entry("Page variant", `"page":"history"`, `"Page":"history"`),
		Entry("Icon variant", `"icon":"clock"`, `"Icon":"clock"`),
		Entry("provider Tools", `"tools":["run"],"statusTool"`, `"Tools":["run"],"statusTool"`),
		Entry("provider StatusTool", `"statusTool":"health"`, `"StatusTool":"health"`),
		Entry("provider mixed duplicate", `"statusTool":"health"`, `"statusTool":"ignored","StatusTool":"health"`),
		Entry("operation Required", `"required":["workspace.materialize"]`, `"Required":["workspace.materialize"]`),
		Entry("operation Optional", `"optional":["egress.check"]`, `"OPTIONAL":["egress.check"]`),
		Entry("operation mixed duplicate", `"optional":["egress.check"]`, `"optional":[],"Optional":["egress.check"]`),
		Entry("provider entry variant", `"executionProvider":`, `"ExecutionProvider":`),
		Entry("provider entry mixed duplicate", `"executionProvider":`, `"executionProvider":null,"ExecutionProvider":`),
		Entry("operations entry variant", `"hostOperations":`, `"HOSTOPERATIONS":`),
		Entry("operations entry mixed duplicate", `"hostOperations":`, `"hostOperations":null,"HostOperations":`),
		Entry("commands entry variant", `"homeCommands":`, `"HomeCommands":`),
		Entry("commands entry mixed duplicate", `"homeCommands":`, `"homeCommands":[],"HomeCommands":`),
	)
	It("preserves arbitrary valid locale keys at the localized map boundary", func() {
		raw := strings.Replace(string(extensionFixture()), `"it":"Apri"`, `"zh-Hant":"開啟","pt-BR":"Abrir"`, 1)
		_, err := ParseManifest([]byte(raw))
		Expect(err).NotTo(HaveOccurred())
	})
})
