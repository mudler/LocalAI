package stderrtail_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/stderrtail"
)

func TestStderrTail(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/stderrtail test suite")
}

var _ = Describe("Buffer", func() {
	It("keeps short output as it is", func() {
		b := stderrtail.New(64)
		_, _ = fmt.Fprintln(b, "first")
		_, _ = fmt.Fprintln(b, "second")
		Expect(b.Text()).To(Equal("first\nsecond"))
	})

	It("keeps only the last bytes, however much is written", func() {
		b := stderrtail.New(32)
		for i := 0; i < 1000; i++ {
			_, _ = fmt.Fprintf(b, "line %04d\n", i)
		}
		got := b.Text()
		Expect(len(got)).To(BeNumerically("<=", 32))
		Expect(got).To(HaveSuffix("line 0999"))
		Expect(got).NotTo(ContainSubstring("line 0001"))
	})

	It("keeps the end of one write that is larger than the buffer", func() {
		b := stderrtail.New(8)
		n, err := b.Write([]byte(strings.Repeat("a", 100) + "END"))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(103))
		Expect(b.Text()).To(Equal("aaaaaEND"))
	})

	It("uses the default size for a size that is not positive", func() {
		b := stderrtail.New(0)
		_, _ = b.Write([]byte(strings.Repeat("x", 3*stderrtail.DefaultSize)))
		Expect(b.Text()).To(HaveLen(stderrtail.DefaultSize))
	})

	It("is safe for concurrent writers", func() {
		b := stderrtail.New(128)
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 500; i++ {
					_, _ = b.Write([]byte("0123456789\n"))
					_ = b.Text()
				}
			}()
		}
		wg.Wait()
		Expect(len(b.Text())).To(BeNumerically("<=", 128))
	})

	It("reads a nil buffer as empty", func() {
		var b *stderrtail.Buffer
		Expect(b.Text()).To(BeEmpty())
		b.Settle(time.Millisecond)
	})

	It("waits for late output and then returns", func() {
		b := stderrtail.New(64)
		go func() {
			time.Sleep(5 * time.Millisecond)
			_, _ = b.Write([]byte("late"))
		}()
		b.Settle(time.Second)
		Expect(b.Text()).To(Equal("late"))
	})
})

var _ = Describe("Clean", func() {
	It("removes colours, control characters and a cut character", func() {
		raw := "\x1b[31mred\x1b[0m text\r\n\x00bell\x07\x1b]0;title\x07\ndone\xe2\x82"
		Expect(stderrtail.Clean("  " + raw + "  ")).To(Equal("red text\nbell\ndone"))
	})
	It("keeps tabs and non-ASCII text", func() {
		Expect(stderrtail.Clean("a\tb è")).To(Equal("a\tb è"))
	})
})

var _ = Describe("RedactValues", func() {
	It("hides long values and leaves short ones", func() {
		got := stderrtail.RedactValues("token=abcdef and x=1", map[string]string{"K": "abcdef", "S": "1"})
		Expect(got).To(Equal("token=*** and x=1"))
	})
})

var _ = Describe("Error", func() {
	It("adds the output after the first line and splits it again", func() {
		base := errors.New("startup failed: EOF")
		err := stderrtail.Wrap(base, "fatal: no key")
		Expect(err.Error()).To(Equal("startup failed: EOF\ncommand output:\nfatal: no key"))
		Expect(errors.Is(err, base)).To(BeTrue())
		msg, out := stderrtail.Split(fmt.Errorf("outer: %w", err))
		Expect(msg).To(Equal("outer: startup failed: EOF"))
		Expect(out).To(Equal("fatal: no key"))
	})
	It("leaves an error without output alone", func() {
		base := errors.New("boom")
		Expect(stderrtail.Wrap(base, "")).To(BeIdenticalTo(base))
		msg, out := stderrtail.Split(base)
		Expect(msg).To(Equal("boom"))
		Expect(out).To(BeEmpty())
	})
})
