// SPDX-License-Identifier: MIT
package packages

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("typed package declarations", func() {
	It("persists model actions and typed declarations together separately and absent in verified immutable manifests and receipts", func() {
		for mask := 0; mask < 16; mask++ {
			b, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
			Expect(e).NotTo(HaveOccurred())
			var m contract.Manifest
			Expect(json.Unmarshal(b, &m)).To(Succeed())
			if mask&1 != 0 {
				m.Capabilities.UI = &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 1, Max: 2}, Pages: []string{"home"}, MainPage: "home", HomeCommands: []contract.HomeCommand{{ID: "open"}}}
			}
			if mask&8 != 0 {
				if m.Capabilities.UI == nil {
					m.Capabilities.UI = &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 1, Max: 2}, Pages: []string{"home"}}
				}
				m.Capabilities.UI.ModelActions = []contract.ModelAction{{Capability: "FLAG_IMAGE", Page: "home", Path: "images"}}
			}
			if mask&2 != 0 {
				m.Capabilities.Actions = true
				m.Capabilities.Tools = []string{"run", "health"}
				m.Capabilities.ToolDefinitions = []contract.ToolDefinition{{Name: "run", Description: "Run", Kind: "action", Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)}, {Name: "health", Description: "Status", Kind: "read", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)}}
				m.Capabilities.ExecutionProvider = &contract.ExecutionProvider{Tools: []string{"run"}, StatusTool: "health"}
			}
			if mask&4 != 0 {
				m.Capabilities.HostOperations = &contract.HostOperations{Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}}
			}
			b, e = json.Marshal(m)
			Expect(e).NotTo(HaveOccurred())
			parsed, e := contract.ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			data := archiveFiles(b, map[string][]byte{"ui.js": []byte("export {}")})
			i, r := fixture(data)
			st, e := i.Stage(context.Background(), r, nil)
			Expect(e).NotTo(HaveOccurred())
			receipt, e := i.Commit(context.Background(), st)
			Expect(e).NotTo(HaveOccurred())
			Expect(receipt.Digest).To(Equal(r.Digest))
			Expect(receipt.Manifest).To(Equal(parsed))
			disk, e := os.ReadFile(filepath.Join(receipt.Directory, "receipt.json"))
			Expect(e).NotTo(HaveOccurred())
			var loaded PackageReceipt
			Expect(json.Unmarshal(disk, &loaded)).To(Succeed())
			Expect(loaded).To(Equal(receipt))
			manifest, e := os.ReadFile(filepath.Join(receipt.Directory, "manifest.json"))
			Expect(e).NotTo(HaveOccurred())
			Expect(manifest).To(Equal(b))
			if mask == 0 {
				for _, key := range []string{"homeCommands", "executionProvider", "hostOperations", "modelActions"} {
					Expect(string(disk)).NotTo(ContainSubstring(key))
				}
			}
		}
	})
	It("refuses unsupported typed candidates without changing the previous package bytes digest or receipt", func() {
		b, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		i, r := fixture(archive(nil, b))
		st, e := i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
		prior, e := i.Commit(context.Background(), st)
		Expect(e).NotTo(HaveOccurred())
		before, e := os.ReadFile(filepath.Join(prior.Directory, "receipt.json"))
		Expect(e).NotTo(HaveOccurred())
		// Unknown extension keys are refused, rather than weakening strict v1.
		bad := []byte(strings.Replace(string(b), `"service":true`, `"service":true,"executionProvider":{"tools":["run"],"statusTool":"health","future":true}`, 1))
		candidate, release := fixture(archive(nil, bad))
		candidate.Root = i.Root
		_, e = candidate.Stage(context.Background(), release, nil)
		Expect(e).To(HaveOccurred())
		after, e := os.ReadFile(filepath.Join(prior.Directory, "receipt.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
		manifest, e := os.ReadFile(filepath.Join(prior.Directory, "manifest.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(manifest).To(Equal(b))
		Expect(prior.Digest).To(Equal(r.Digest))
		Expect(prior.Digest).NotTo(Equal(release.Digest))
	})
})
