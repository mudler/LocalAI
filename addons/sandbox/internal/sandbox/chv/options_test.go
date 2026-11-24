package chv

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseVersion", func() {
	It("reads the major and minor version of each tool", func() {
		for out, want := range map[string][2]int{
			"cloud-hypervisor v54.0\n": {54, 0},
			"virtiofsd 1.14.0\n":       {1, 14},
			"bubblewrap 0.11.1\n":      {0, 11},
			"mke2fs 1.47.2 (1-Jan-2025)\n\tUsing EXT2FS Library version 1.47.2\n": {1, 47},
			"debugfs 1.47.2 (1-Jan-2025)\n":                                       {1, 47},
		} {
			got, ok := parseVersion([]byte(out))
			Expect(ok).To(BeTrue(), "%q", out)
			Expect(got).To(Equal(want), "%q", out)
		}
		_, ok := parseVersion([]byte("no version here"))
		Expect(ok).To(BeFalse(), "found a version in text without one")
	})
})
