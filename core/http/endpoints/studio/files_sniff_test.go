//go:build auth

package studio

import (
	"bytes"
	"errors"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type seekFails struct{ *bytes.Reader }

func (seekFails) Seek(int64, int) (int64, error) { return 0, errors.New("seek failed") }

type readFails struct{}

func (readFails) Read([]byte) (int, error)       { return 0, errors.New("disk error") }
func (readFails) Seek(int64, int) (int64, error) { return 0, nil }

var _ = Describe("sniffType", func() {
	It("reads a short file and rewinds", func() {
		r := bytes.NewReader([]byte("hello"))
		ct, err := sniffType(r)
		Expect(err).ToNot(HaveOccurred())
		Expect(ct).To(Equal("text/plain"))
		rest, _ := io.ReadAll(r)
		Expect(string(rest)).To(Equal("hello"), "the reader was not rewound")
	})

	It("fails when the reader cannot rewind", func() {
		// A reader that stays at the end would be served from the wrong offset
		// with status 200, so the caller must see the failure.
		_, err := sniffType(seekFails{bytes.NewReader([]byte(strings.Repeat("x", 600)))})
		Expect(err).To(HaveOccurred(), "a failed rewind was ignored")
	})

	It("fails when the file cannot be read", func() {
		_, err := sniffType(readFails{})
		Expect(err).To(HaveOccurred(), "a failed read was ignored")
	})
})
