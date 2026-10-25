// SPDX-License-Identifier: MIT
package cli

import (
	"reflect"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the add-on host operations flag", func() {
	It("defaults to the documented setting", func() {
		f, ok := reflect.TypeOf(RunCMD{}).FieldByName("AddonHostOps")
		Expect(ok).To(BeTrue(), "the flag is missing")
		Expect(f.Tag.Get("default")).To(Equal(config.DefaultAddonHostOps))
		Expect(f.Tag.Get("env")).To(Equal("LOCALAI_ADDON_HOST_OPS"))
	})
})
