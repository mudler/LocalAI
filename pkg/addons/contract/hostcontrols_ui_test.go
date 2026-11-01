// SPDX-License-Identifier: MIT
package contract

import (
	"fmt"
	"os"
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The host control registry of the React UI", func() {
	It("has the same IDs as the registry of the React UI", func() {
		b, err := os.ReadFile("../../../core/http/react-ui/src/addons/hostControls.js")
		Expect(err).NotTo(HaveOccurred())
		m := regexp.MustCompile(`(?s)export const HOST_CONTROL_IDS = Object\.freeze\(\[(.*?)\]\)`).FindSubmatch(b)
		Expect(m).NotTo(BeNil())
		var ids []string
		for _, q := range regexp.MustCompile(`'([a-z-]+)'`).FindAllSubmatch(m[1], -1) {
			ids = append(ids, string(q[1]))
		}
		Expect(ids).To(Equal(HostControlIDs()), fmt.Sprintf("hostControls.js lists %v", ids))
	})
})
