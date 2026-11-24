package guestagent

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("frames", func() {
	It("round-trips", func() {
		in := []Frame{
			{Type: TypeExec, Args: []string{"sh", "-c", "echo hi"}, Env: []string{"A=1"}, Dir: "/workspace", TTY: &TTY{Rows: 24, Cols: 80}},
			{Type: TypeStdin, Data: []byte{0, 1, 2, 255}},
			{Type: TypeExit, Code: 3},
			{Type: TypeExit, Signal: 15},
			{Type: TypeReady, Version: Version},
		}
		var buf bytes.Buffer
		for _, f := range in {
			Expect(WriteFrame(&buf, f)).To(Succeed())
		}
		for _, want := range in {
			got, err := ReadFrame(&buf)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		}
		_, err := ReadFrame(&buf)
		Expect(err).To(MatchError(io.EOF))
	})

	It("enforces the size limits", func() {
		Expect(WriteFrame(io.Discard, Frame{Type: TypeStdout, Data: make([]byte, MaxFrame)})).To(MatchError(ErrFrameTooLarge))
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], MaxFrame+1)
		_, err := ReadFrame(bytes.NewReader(hdr[:]))
		Expect(err).To(MatchError(ErrFrameTooLarge))
		for _, body := range []string{"{", `{"data":"x"}`, "[]"} {
			var buf bytes.Buffer
			binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
			buf.Write(hdr[:])
			buf.WriteString(body)
			_, err := ReadFrame(&buf)
			Expect(err).To(HaveOccurred(), "%q was accepted", body)
		}
		short := append(binary.BigEndian.AppendUint32(nil, 10), []byte(`{"ty`)...)
		_, err = ReadFrame(bytes.NewReader(short))
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
	})
})

var _ = Describe("SameMajor", func() {
	It("compares the major versions", func() {
		for _, c := range []struct {
			a, b string
			ok   bool
		}{{"1.0", "1.3", true}, {"1.0", "2.0", false}, {"", "1.0", false}, {"1", "1.0", true}} {
			Expect(SameMajor(c.a, c.b)).To(Equal(c.ok), "SameMajor(%q, %q)", c.a, c.b)
		}
		Expect(Version).To(HavePrefix("1."), "update the tests for a new major version")
	})
})

var _ = Describe("mergeEnv", func() {
	It("lets the request override the base", func() {
		got := mergeEnv([]string{"PATH=/bin", "HOME=/home/agent", "A=1"}, []string{"A=2", "B=3", "PATH=/opt/bin"})
		Expect(got).To(Equal([]string{"PATH=/opt/bin", "HOME=/home/agent", "A=2", "B=3"}))
	})
})

var _ = Describe("credit", func() {
	// The host caps a credit at the bytes outstanding and ignores a negative one,
	// so a buggy guest cannot drive the window out of range.
	It("clamps", func() {
		p := &Process{outstanding: 100}
		p.fcond = sync.NewCond(&p.fmu)
		for _, c := range []struct{ n, want int }{
			{-5, 100}, {0, 100}, {30, 70}, {500, 0}, {-1 << 30, 0}, {1 << 30, 0},
		} {
			p.credit(c.n)
			Expect(p.outstanding).To(Equal(c.want), "credit(%d)", c.n)
		}
	})
})
