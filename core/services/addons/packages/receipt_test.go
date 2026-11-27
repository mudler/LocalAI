// SPDX-License-Identifier: MIT
package packages

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The receipt of a committed package", func() {
	It("is the receipt on disk when the manifest has an empty list", func() {
		b, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		// "navigation": [] is an empty list that receipt.json leaves out.
		b = []byte(strings.Replace(string(b), `"service":true`, `"service":true,"ui":{"entry":"ui.js","sdk":{"min":1,"max":2},"pages":["home"],"navigation":[]}`, 1))
		i, r := fixture(archiveFiles(b, map[string][]byte{"ui.js": []byte("export {}")}))
		st, e := i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Receipt().Manifest.Capabilities.UI.Navigation).To(Equal([]string{}))
		receipt, e := i.Commit(context.Background(), st)
		Expect(e).NotTo(HaveOccurred())
		disk, e := os.ReadFile(filepath.Join(receipt.Directory, "receipt.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(disk)).NotTo(ContainSubstring("navigation"))
		var loaded PackageReceipt
		Expect(json.Unmarshal(disk, &loaded)).To(Succeed())
		Expect(receipt).To(Equal(loaded))
	})
})
