// SPDX-License-Identifier: MIT
package toolbroker

import (
	"crypto/sha256"
	"encoding/json"
	"math"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("binding v1 encoding integrity", func() {
	It("matches the original JSON tuple bytes and hashes across escaped and binary inputs", func() {
		values := []string{"", "plain", "\"\\\n\r\t\x00", "<>&", "é\u2028\u2029", "\xff\xfe", "a/b"}
		for _, text := range values {
			for _, args := range [][]byte{nil, {}, {0}, {0xff, 0xfe, 0, 1}, []byte(text)} {
				s := Scope{text + "p", text + "c", text + "t", math.MaxUint64, 1}
				v := Revisions{text, text, text, text, text, text, text}
				b, err := NewBinding(s, text+"r", ToolID(text+"x"), args, v, text)
				Expect(err).NotTo(HaveOccurred())
				original, err := json.Marshal([]any{"toolbroker-binding/v1", s, b.request, b.tool, b.Arguments(), v, text})
				Expect(err).NotTo(HaveOccurred())
				Expect(bindingEncoding(b)).To(Equal(original))
				Expect(b.Digest()).To(Equal(sha256.Sum256(original)))
				Expect(b.valid()).To(BeTrue())
			}
		}
	})
	It("pins JSON escaping goldens and covers every single byte", func() {
		for _, tc := range []struct{ input, want string }{
			{"", `""`}, {"<>&", `"\u003c\u003e\u0026"`},
			{"\xff\xfe", `"\ufffd\ufffd"`}, {"\u2028\u2029", `"\u2028\u2029"`},
			{"\x00\b\f\n\r\t\"\\", `"\u0000\b\f\n\r\t\"\\"`},
		} {
			Expect(string(appendBindingString(nil, tc.input))).To(Equal(tc.want))
		}
		for i := 0; i < 256; i++ {
			text := "prefix" + string([]byte{byte(i)}) + "suffix"
			original, err := json.Marshal(text)
			Expect(err).NotTo(HaveOccurred())
			Expect(appendBindingString(nil, text)).To(Equal(original))
		}
		b, err := NewBinding(Scope{"p", "c", "t", 1, 1}, "r", "x", nil, Revisions{}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(bindingEncoding(b))).To(Equal(`["toolbroker-binding/v1",{"Principal":"p","Conversation":"c","Turn":"t","Attempt":1,"Epoch":1},"r","x","",{"Catalog":"","Config":"","Authorization":"","Resource":"","Proposal":"","ParentGrant":"","Taint":""},""]`))
	})
	It("matches the original tuple for distinct fields and long binary arguments", func() {
		b, err := NewBinding(Scope{"p", "c", "t", 1, math.MaxUint64}, "r", "tool", []byte(strings.Repeat("\x00\xffhello", 1000)), Revisions{"cat", "cfg", "auth", "resource", "proposal", "parent", "taint"}, "key")
		Expect(err).NotTo(HaveOccurred())
		original, err := json.Marshal([]any{"toolbroker-binding/v1", b.scope, b.request, b.tool, b.Arguments(), b.revisions, b.providerKey})
		Expect(err).NotTo(HaveOccurred())
		Expect(bindingEncoding(b)).To(Equal(original))
		Expect(b.valid()).To(BeTrue())
	})
	It("rejects malformed required fields even with a matching recomputed digest", func() {
		for i := 0; i < 7; i++ {
			b := fixture(AllowAll).Binding
			switch i {
			case 0:
				b.scope.Principal = ""
			case 1:
				b.scope.Conversation = ""
			case 2:
				b.scope.Turn = ""
			case 3:
				b.scope.Attempt = 0
			case 4:
				b.scope.Epoch = 0
			case 5:
				b.request = ""
			case 6:
				b.tool = ""
			}
			b.digest = sha256.Sum256(bindingEncoding(b))
			Expect(b.valid()).To(BeFalse())
			_, err := NewBinding(b.scope, b.request, b.tool, b.Arguments(), b.revisions, b.providerKey)
			Expect(err).To(MatchError(ErrInvalid))
		}
	})

	It("rejects every tampered immutable field and digest after a successful validation", func() {
		b := fixture(AllowAll).Binding
		Expect(b.valid()).To(BeTrue())
		for _, changed := range BindingMismatchWitnesses(b) {
			changed.digest = b.digest
			Expect(changed.valid()).To(BeFalse())
		}
		b.digest[0] ^= 1
		Expect(b.valid()).To(BeFalse())
		Expect((Binding{}).valid()).To(BeFalse())
	})
	It("owns input and output bytes and preserves nil/empty equivalence", func() {
		raw := []byte{0xff, 0, 'x'}
		s := Scope{"p", "c", "t", 1, 1}
		b, err := NewBinding(s, "r", "x", raw, Revisions{}, "key")
		Expect(err).NotTo(HaveOccurred())
		digest := b.Digest()
		raw[0] = 0
		out := b.Arguments()
		out[0] = 1
		Expect(b.valid()).To(BeTrue())
		Expect(b.Digest()).To(Equal(digest))
		a, _ := NewBinding(s, "r", "x", nil, Revisions{}, "key")
		c, _ := NewBinding(s, "r", "x", []byte{}, Revisions{}, "key")
		Expect(a).To(Equal(c))
		Expect(a.valid()).To(BeTrue())
	})
})
