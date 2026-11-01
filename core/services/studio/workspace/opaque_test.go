package workspace_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("IsOpaque", func() {
	DescribeTable("reads the overlay attribute of a folder",
		func(x map[string][]byte, want bool) {
			Expect(workspace.IsOpaque(x)).To(Equal(want))
		},
		Entry("no attributes", nil, false),
		Entry("trusted namespace", map[string][]byte{"trusted.overlay.opaque": []byte("y")}, true),
		Entry("user namespace", map[string][]byte{"user.overlay.opaque": []byte("y")}, true),
		Entry("other value", map[string][]byte{"trusted.overlay.opaque": []byte("n")}, false),
		Entry("other attribute", map[string][]byte{"user.mime": []byte("y")}, false),
	)
})
