// SPDX-License-Identifier: MIT
package toolargs

import (
	"crypto/rand"
	"encoding/hex"
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

var _ = Describe("the resource ID of a conversation", func() {
	It("is valid, stable and distinct for 200 random conversation IDs", func() {
		seen := map[string]string{}
		for range 200 {
			raw := make([]byte, 12)
			_, err := rand.Read(raw)
			Expect(err).ToNot(HaveOccurred())
			id := hex.EncodeToString(raw) // the shape of a chat ID: digit-first about 6 times in 10
			Expect(ValidConversationID(id)).To(BeTrue())
			got := ActionResourceID(id)
			Expect(ValidResourceID(got)).To(BeTrue(), got)
			Expect(ActionResourceID(id)).To(Equal(got))
			Expect(seen).ToNot(HaveKey(got))
			seen[got] = id
		}
		// The longest conversation ID, in mixed case, still maps.
		Expect(ValidResourceID(ActionResourceID("A" + strings.Repeat("Z._:-", 25)))).To(BeTrue())
	})
})
