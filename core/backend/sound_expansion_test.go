// SPDX-License-Identifier: MIT
package backend_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/backend"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("Sound keyscale boundaries", func() {
	It("preserves omitted, empty, null and invalid vocabulary validation", func() {
		_, err := backend.ParseSoundExpansion(`{"caption":"piano","lyrics":""}`)
		Expect(err).NotTo(HaveOccurred())
		_, err = backend.ParseSoundExpansion(`{"caption":"piano","lyrics":"","keyscale":null}`)
		Expect(err).To(HaveOccurred())
		for _, key := range []string{"", "C Major", "C MIN", " C", "C ", "C  minor", "C\tminor", "C♯ minor", "D♭", "H major", "C minor suffix", "C harmonic minor"} {
			raw, marshalErr := json.Marshal(map[string]string{"caption": "piano", "lyrics": "", "keyscale": key})
			Expect(marshalErr).NotTo(HaveOccurred())
			_, err := backend.ParseSoundExpansion(string(raw))
			Expect(err).To(HaveOccurred(), key)
		}
	})
	It("quotes only the rejected decoded key and bounds it by runes", func() {
		for _, key := range []string{"C\n\r\t\x00\x1b\"minor", "音♯\nminor", strings.Repeat("界", 64), strings.Repeat("界", 65), strings.Repeat("\n", 10000)} {
			raw, marshalErr := json.Marshal(map[string]string{"caption": "private caption", "lyrics": "private lyrics", "keyscale": key})
			Expect(marshalErr).NotTo(HaveOccurred())
			_, err := backend.ParseSoundExpansion(string(raw))
			Expect(err).To(HaveOccurred())
			prefix := []rune(key)
			suffix := ""
			if len(prefix) > 64 {
				prefix = prefix[:64]
				suffix = fmt.Sprintf(" (truncated; %d runes total)", utf8.RuneCountInString(key))
			}
			Expect(err.Error()).To(Equal(fmt.Sprintf("keyscale %q%s must be a musical key and optional mode, such as C minor", string(prefix), suffix)))
			Expect(err.Error()).NotTo(ContainSubstring("private"))
			Expect(err.Error()).NotTo(ContainSubstring("\n"))
		}
	})
})
