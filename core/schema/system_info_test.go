// SPDX-License-Identifier: MIT
package schema_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SysInfoModel memory", func() {
	It("omits unavailable VRAM while preserving a measured zero", func() {
		entry := schema.SysInfoModel{ID: "model"}
		encoded, err := json.Marshal(entry)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(MatchJSON(`{"id":"model"}`))

		zero := uint64(0)
		entry.SizeVRAM = &zero
		encoded, err = json.Marshal(entry)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(MatchJSON(`{"id":"model","size_vram":0}`))
	})
})
