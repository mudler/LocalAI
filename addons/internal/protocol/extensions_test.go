// SPDX-License-Identifier: MIT
package protocol

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("typed declaration echo", func() {
	It("echoes model actions and typed declarations together separately and absent over authenticated health", func() {
		for mask := 0; mask < 16; mask++ {
			caps := contract.Capabilities{Service: true}
			if mask&1 != 0 {
				caps.UI = &contract.UI{Entry: "ui/main.js", SDK: contract.VersionRange{Min: 2, Max: 2}, Pages: []string{"home"}, MainPage: "home", HomeCommands: []contract.HomeCommand{{ID: "open"}}}
			}
			if mask&8 != 0 {
				if caps.UI == nil {
					caps.UI = &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 2, Max: 2}, Pages: []string{"home"}}
				}
				caps.UI.ModelActions = []contract.ModelAction{{Capability: "FLAG_IMAGE", Page: "home", Path: "images"}}
			}
			if mask&2 != 0 {
				caps.Actions = true
				caps.Tools = []string{"run", "health"}
				caps.ToolDefinitions = []contract.ToolDefinition{{Name: "run", Description: "Run", Kind: "action", Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)}, {Name: "health", Description: "Status", Kind: "read", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}
				caps.ExecutionProvider = &contract.ExecutionProvider{Tools: []string{"run"}, StatusTool: "health"}
			}
			if mask&4 != 0 {
				caps.HostOperations = &contract.HostOperations{Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}}
			}
			v := contract.VersionRange{Min: 1, Max: 1}
			manifest := contract.Manifest{SchemaVersion: 1, ID: "sample", Version: "1.0.0", Executable: "bin/sample", OS: "linux", Arch: "amd64", Host: v, API: v, Protocol: v, DataFormat: v, Capabilities: caps}
			b, e := json.Marshal(manifest)
			Expect(e).NotTo(HaveOccurred())
			parsed, e := contract.ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			Expect(parsed.Capabilities).To(Equal(caps))
			original := Startup{Registration: Registration{AddonID: "sample", Generation: 7, Incarnation: "inc", Protocol: 1, Capabilities: parsed.Capabilities}, Credential: "secret", Limits: contract.DefaultLimits()}
			b, e = json.Marshal(original)
			Expect(e).NotTo(HaveOccurred())
			var startup Startup
			Expect(json.Unmarshal(b, &startup)).To(Succeed())
			req := httptest.NewRequest(http.MethodPost, "/health", bytes.NewBufferString(`{}`))
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("X-LocalAI-Incarnation", "inc")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			Handler(startup, nil).ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK))
			var echo Registration
			Expect(json.Unmarshal(rec.Body.Bytes(), &echo)).To(Succeed())
			Expect(echo).To(Equal(original.Registration))
			if mask == 0 {
				for _, key := range []string{"homeCommands", "executionProvider", "hostOperations", "modelActions"} {
					Expect(rec.Body.String()).NotTo(ContainSubstring(key))
				}
			}
		}
	})
})
