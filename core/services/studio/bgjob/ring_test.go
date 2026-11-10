package bgjob_test

import (
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
)

var _ = Describe("Ring", func() {
	It("reads from an absolute offset and returns the next one", func() {
		r := bgjob.NewRing(1024)
		_, _ = r.Write([]byte("hello "))
		_, _ = r.Write([]byte("world"))
		out, from, next := r.Page(0, 100)
		Expect(string(out)).To(Equal("hello world"))
		Expect([]int64{from, next}).To(Equal([]int64{0, 11}))
		out, from, next = r.Page(6, 100)
		Expect(string(out)).To(Equal("world"))
		Expect([]int64{from, next}).To(Equal([]int64{6, 11}))
		out, from, next = r.Page(99, 100)
		Expect(out).To(BeEmpty())
		Expect([]int64{from, next}).To(Equal([]int64{11, 11}))
		Expect(r.Total()).To(Equal(int64(11)))
	})

	It("keeps the last bytes only, moves a lost offset to the oldest kept byte, and keeps counting", func() {
		r := bgjob.NewRing(100)
		for i := range 50 {
			_, _ = r.Write([]byte(strings.Repeat(string(rune('a'+i%26)), 10)))
		}
		Expect(r.Total()).To(Equal(int64(500)))
		Expect(r.Dropped()).To(Equal(int64(400)))
		out, from, next := r.Page(0, 1000)
		Expect(from).To(Equal(int64(400)))
		Expect(out).To(HaveLen(100))
		Expect(next).To(Equal(int64(500)))
		Expect(r.Tail(10)).To(Equal(strings.Repeat("x", 10)))
	})

	It("never splits a UTF-8 character at either edge of a page", func() {
		r := bgjob.NewRing(1000)
		_, _ = r.Write([]byte(strings.Repeat("é", 50))) // 100 bytes of two
		out, from, next := r.Page(0, 7)
		Expect(utf8.Valid(out)).To(BeTrue())
		Expect(out).To(HaveLen(6))
		Expect([]int64{from, next}).To(Equal([]int64{0, 6}))

		small := bgjob.NewRing(11)
		_, _ = small.Write([]byte(strings.Repeat("é", 10))) // 20 bytes, the last 11 kept: starts mid-character
		out, from, _ = small.Page(0, 100)
		Expect(utf8.Valid(out)).To(BeTrue())
		Expect(from).To(Equal(int64(10)))
		Expect(utf8.ValidString(small.Tail(5))).To(BeTrue())
	})

	It("survives a write larger than the ring", func() {
		r := bgjob.NewRing(8)
		_, _ = r.Write([]byte(strings.Repeat("z", 100)))
		out, from, _ := r.Page(0, 100)
		Expect(out).To(HaveLen(8))
		Expect(from).To(Equal(int64(92)))
	})
})
