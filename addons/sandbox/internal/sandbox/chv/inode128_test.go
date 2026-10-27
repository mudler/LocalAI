package chv

import (
	"fmt"
	"strings"
	"testing"
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

// TestInodeDump128 parses the dump of a 128-byte inode (mke2fs -I 128). The
// dump ends at 0x80, so there is no i_extra_isize to read. The last line is
// not zero, so the parser cannot fold it into a repeat.
func TestInodeDump128(t *testing.T) {
	raw := make([]byte, 128)
	raw[0x00] = 0xed
	raw[0x01] = 0x81
	raw[0x7F] = 0x01
	in, rest, err := parseInodeDump(dumpText(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("rest %q", rest)
	}
	if in.Mode != 0o100755 || in.xattrBad || in.CtimeExtra != 0 || in.MtimeExtra != 0 || in.Version>>32 != 0 {
		t.Fatalf("a 128-byte inode gave %+v", in)
	}
}

// TestReadUpper128ByteInodes reads an upper image made with 128-byte inodes.
// The attributes of the opaque folder go into an external block there.
func TestReadUpper128ByteInodes(t *testing.T) {
	_, r := testUpperFS(t, "-I", "128")
	src, _, err := r.read(readCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := summary(t, src)
	for _, want := range []string{"file w/new.txt new", "dir w/d opaque", "whiteout w/gone.txt"} {
		if !contains(got, want) {
			t.Errorf("%q missing from %v", want, got)
		}
	}
}
