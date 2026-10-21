// SPDX-License-Identifier: MIT
package toolargs

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// args builds an argument object of exactly n bytes with one string property.
// Bind adds `,"tool":"t"` (11 bytes) to what the add-on receives.
func args(n int) json.RawMessage {
	const frame = `{"content":""}`
	return json.RawMessage(`{"content":"` + strings.Repeat("a", n-len(frame)) + `"}`)
}

var _ = Describe("the argument limit", func() {
	It("is one MiB and one byte more is refused", func() {
		Expect(MaxArgumentBytes).To(Equal(1<<20), "limit must be 1 MiB")
		params := map[string]any{"properties": map[string]any{"content": map[string]any{"type": "string"}}}
		// The limit is on what the add-on receives: the tool key counts.
		const toolKey = len(`,"tool":"t"`)
		_, body, err := Bind("t", params, args(MaxArgumentBytes-toolKey), false)
		Expect(err).ToNot(HaveOccurred(), "a body of exactly 1 MiB must be accepted")
		Expect(body).To(HaveLen(MaxArgumentBytes))
		_, _, err = Bind("t", params, args(MaxArgumentBytes-toolKey+1), false)
		Expect(err).To(MatchError(ErrInvalid), "a body of 1 MiB + 1 byte must be refused")
		_, _, err = Bind("t", params, args(MaxArgumentBytes+1), false)
		Expect(err).To(MatchError(ErrInvalid), "an input over 1 MiB must be refused")
	})
})
