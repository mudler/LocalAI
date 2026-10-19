// SPDX-License-Identifier: MIT
package backend_test

import (
	"github.com/mudler/LocalAI/core/backend"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
)

var _ = Describe("Sound Expansion schema", func() {
	It("accepts typed neutral fields", func() {
		result, err := backend.ParseSoundExpansion(`{"caption":"warm piano","lyrics":"","bpm":120,"keyscale":"C minor","timesignature":"4/4","language":"en","duration_seconds":12.5}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Caption).To(Equal("warm piano"))
		Expect(*result.BPM).To(Equal(120))
	})
	It("rejects missing, unknown, null, mistyped, oversized and out-of-range fields", func() {
		for _, raw := range []string{`{}`, `null`, `{"caption":"piano"}`, `{"caption":"piano","lyrics":null}`, `{"caption":null,"lyrics":""}`, `{"caption":"piano","lyrics":"","style":"x"}`, `{"caption":"piano","lyrics":"","bpm":1.5}`, `{"caption":"piano","lyrics":"","bpm":401}`, `{"caption":"piano","lyrics":"","duration_seconds":0}`, `{"caption":"piano","lyrics":"","duration_seconds":600.00001}`, `{"caption":"piano","lyrics":"","timesignature":"33/4"}`, `{"caption":"piano","lyrics":"","keyscale":"nonsense"}`, `{"caption":"piano","lyrics":""} trailing`, `{"caption":"` + strings.Repeat("x", 501) + `","lyrics":""}`} {
			_, err := backend.ParseSoundExpansion(raw)
			Expect(err).To(HaveOccurred(), raw)
		}
	})
})
