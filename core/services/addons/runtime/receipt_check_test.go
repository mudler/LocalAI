// SPDX-License-Identifier: MIT
package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs check the comparison between receipt.json and the journal
// receipt that a launch does before it runs an executable.
var _ = Describe("The receipt check before a launch", func() {
	// shipped is the receipt of every add-on manifest in this repository, as
	// ParseManifest gives it: an empty list in the manifest is an empty,
	// non-nil slice here.
	shipped := func() map[string]packages.PackageReceipt {
		paths, e := filepath.Glob("../../../../addons/*/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		Expect(paths).NotTo(BeEmpty())
		out := map[string]packages.PackageReceipt{}
		for _, p := range paths {
			b, e := os.ReadFile(p)
			Expect(e).NotTo(HaveOccurred())
			m, e := contract.ParseManifest(b)
			Expect(e).NotTo(HaveOccurred(), p)
			out[p] = packages.PackageReceipt{Digest: "sha256:fixture", SourceID: "official", Manifest: m, Directory: "/addons/package-stage-1"}
		}
		return out
	}

	It("accepts the receipt.json of every shipped manifest, against the receipt in memory and the receipt read back", func() {
		for path, r := range shipped() {
			b, written, e := packages.EncodeReceipt(r)
			Expect(e).NotTo(HaveOccurred(), path)
			var disk packages.PackageReceipt
			Expect(json.Unmarshal(b, &disk)).To(Succeed(), path)
			Expect(written).To(Equal(disk), "%s: the returned receipt is not the one on disk", path)
			Expect(receiptMatches(b, r)).To(BeTrue(), "%s: the in-memory receipt is refused", path)
			Expect(receiptMatches(b, written)).To(BeTrue(), "%s: the receipt read back is refused", path)
		}
	})

	It("accepts an empty list in memory that receipt.json leaves out", func() {
		r := packages.PackageReceipt{Digest: "sha256:fixture", Manifest: contract.Manifest{ID: "sample", Capabilities: contract.Capabilities{ModelSearch: &contract.ModelSearch{Source: contract.ModelSearchSource{ID: "sample", Title: "Sample"}, Sorts: []string{}}, UI: &contract.UI{Entry: "ui.js", Navigation: []string{}}}}}
		b, e := json.Marshal(r)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).NotTo(ContainSubstring("sorts"))
		Expect(string(b)).NotTo(ContainSubstring("navigation"))
		Expect(receiptMatches(b, r)).To(BeTrue())
	})

	It("refuses a receipt.json that differs from the journal receipt", func() {
		r := packages.PackageReceipt{Digest: "sha256:fixture", SourceID: "official", Manifest: contract.Manifest{ID: "sample", Version: "1.0.0", Executable: "bin/sample", Capabilities: contract.Capabilities{Tools: []string{"run"}, ModelSearch: &contract.ModelSearch{Source: contract.ModelSearchSource{ID: "sample", Title: "Sample"}, Sorts: []string{}}}}, Directory: "/addons/package-stage-1"}
		b, e := json.Marshal(r)
		Expect(e).NotTo(HaveOccurred())
		Expect(receiptMatches(b, r)).To(BeTrue())
		for name, edit := range map[string]func(*packages.PackageReceipt){
			"digest":     func(p *packages.PackageReceipt) { p.Digest = "sha256:another" },
			"directory":  func(p *packages.PackageReceipt) { p.Directory = "/addons/package-stage-2" },
			"executable": func(p *packages.PackageReceipt) { p.Manifest.Executable = "bin/other" },
			"tools":      func(p *packages.PackageReceipt) { p.Manifest.Capabilities.Tools = []string{"run", "shell"} },
			"no tools":   func(p *packages.PackageReceipt) { p.Manifest.Capabilities.Tools = nil },
			"a sort":     func(p *packages.PackageReceipt) { p.Manifest.Capabilities.ModelSearch.Sorts = []string{"likes"} },
			"signer":     func(p *packages.PackageReceipt) { p.Signer = "someone" },
		} {
			other := r
			ms := *r.Manifest.Capabilities.ModelSearch
			other.Manifest.Capabilities.ModelSearch = &ms
			edit(&other)
			Expect(receiptMatches(b, other)).To(BeFalse(), "a receipt with another %s is accepted", name)
		}
	})

	It("refuses a receipt.json that is not a receipt", func() {
		r := packages.PackageReceipt{Digest: "sha256:fixture"}
		Expect(receiptMatches([]byte(`{"digest":`), r)).To(BeFalse())
		Expect(receiptMatches([]byte(`[]`), r)).To(BeFalse())
	})
})
