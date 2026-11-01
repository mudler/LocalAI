package chv

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// dumpText prints raw the way debugfs prints inode_dump: one line per 16
// bytes, and an empty line at the end.
func dumpText(raw []byte) []byte {
	var b strings.Builder
	for off := 0; off < len(raw); off += 16 {
		fmt.Fprintf(&b, "%04o  ", off)
		for i := 0; i < 16; i += 2 {
			fmt.Fprintf(&b, "%02x%02x ", raw[off+i], raw[off+i+1])
		}
		b.WriteString(" ................\n")
	}
	b.WriteString("\n")
	return []byte(b.String())
}

var _ = Describe("128-byte inodes", func() {
	// It parses the dump of a 128-byte inode (mke2fs -I 128). The dump ends
	// at 0x80, so there is no i_extra_isize to read. The last line is not
	// zero, so the parser cannot fold it into a repeat.
	It("parses the dump of a 128-byte inode", func() {
		raw := make([]byte, 128)
		raw[0x00] = 0xed
		raw[0x01] = 0x81
		raw[0x7F] = 0x01
		in, rest, err := parseInodeDump(dumpText(raw))
		Expect(err).To(Succeed())
		Expect(rest).To(BeEmpty())
		Expect(in.Mode).To(BeEquivalentTo(0o100755), "a 128-byte inode gave %+v", in)
		Expect(in.xattrBad).To(BeFalse(), "a 128-byte inode gave %+v", in)
		Expect(in.CtimeExtra).To(BeZero(), "a 128-byte inode gave %+v", in)
		Expect(in.MtimeExtra).To(BeZero(), "a 128-byte inode gave %+v", in)
		Expect(in.Version>>32).To(BeZero(), "a 128-byte inode gave %+v", in)
	})

	// It reads an upper image made with 128-byte inodes. The attributes of
	// the opaque folder go into an external block there.
	It("reads an upper image made with 128-byte inodes", func() {
		_, r := testUpperFS("-I", "128")
		src, _, err := r.read(readCtx(), nil)
		Expect(err).To(Succeed())
		got := summary(src)
		for _, want := range []string{"file w/new.txt new", "dir w/d opaque", "whiteout w/gone.txt"} {
			Expect(contains(got, want)).To(BeTrue(), "%q missing from %v", want, got)
		}
	})
})
