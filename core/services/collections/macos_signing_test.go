package collections

import (
	"os"
	"regexp"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// The collections read PDFs with go-pdfium, which runs PDFium as WebAssembly
// under wazero. On macOS arm64 wazero compiles that code into memory mapped
// without MAP_JIT, and the hardened runtime kills a release binary that lacks
// allow-unsigned-executable-memory (issue 13471). The signing happens only in
// a tagged release, so this test reads the files that configure it.
var _ = Describe("the macOS release signature", func() {
	readRepoFile := func(path string) []byte {
		b, err := os.ReadFile("../../../" + path)
		Expect(err).ToNot(HaveOccurred())
		return b
	}

	It("signs the server binary with allow-unsigned-executable-memory", func() {
		var cfg struct {
			Notarize struct {
				MacOS []struct {
					IDs  []string `yaml:"ids"`
					Sign struct {
						Entitlements string `yaml:"entitlements"`
					} `yaml:"sign"`
				} `yaml:"macos"`
			} `yaml:"notarize"`
		}
		Expect(yaml.Unmarshal(readRepoFile(".goreleaser.yaml"), &cfg)).To(Succeed())

		var entitlements string
		for _, m := range cfg.Notarize.MacOS {
			if slices.Contains(m.IDs, "local-ai") {
				entitlements = m.Sign.Entitlements
			}
		}
		Expect(entitlements).ToNot(BeEmpty(), "notarize.macos must sign the local-ai build with an entitlements file")

		plist := string(readRepoFile(entitlements))
		Expect(plist).To(MatchRegexp(`<key>com\.apple\.security\.cs\.allow-unsigned-executable-memory</key>\s*<true/>`))

		// A local `make osx-signed` must give the same result as a release.
		Expect(string(readRepoFile("Makefile"))).To(MatchRegexp(`--entitlements "` + regexp.QuoteMeta(entitlements) + `"`))
	})
})
