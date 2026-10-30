//go:build linux || darwin

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// limitSandbox is a guest whose every process writes out to stdout, then
// runs until its context ends.
type limitSandbox struct {
	sandbox.Sandbox
	out []byte
}

func (s limitSandbox) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	if len(s.out) > 0 && c.Stdout != nil {
		_, _ = c.Stdout.Write(s.out)
	}
	return blockProc{ctx}, nil
}

// blockProc is a process that exits only when its context ends.
type blockProc struct{ ctx context.Context }

func (p blockProc) Wait() (int, error)        { <-p.ctx.Done(); return -1, p.ctx.Err() }
func (blockProc) Signal(syscall.Signal) error { return nil }
func (blockProc) Resize(uint16, uint16) error { return nil }

var _ = Describe("bash limits", func() {
	// A process that writes more than the cap is stopped at the cap, and does
	// not run until its timeout.
	It("stops a process at the output cap", func() {
		var got BashResult
		tl := Bash(func(context.Context) (sandbox.Sandbox, func(), error) {
			return limitSandbox{out: bytes.Repeat([]byte("x"), 100000)}, func() {}, nil
		}, BashOptions{MaxTimeout: 30 * time.Second, Done: func(_ context.Context, r BashResult) { got = r }})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		out, err := tl.Run(ctx, json.RawMessage(`{"command":"x","timeoutSeconds":20}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("[output cut: "))
		Expect(len(out)).To(BeNumerically("<=", MaxOutput+64))
		Expect(time.Since(start)).To(BeNumerically("<=", 5*time.Second), "the process was not stopped at the cap")
		Expect(got.Command).To(Equal("x"))
	})

	It("caps the timeout a model asks for at the maximum and says it in the schema", func() {
		tl := Bash(nil, BashOptions{MaxTimeout: 1800 * time.Second})
		Expect(string(tl.Schema)).To(ContainSubstring(`"maximum":1800`))
		Expect(tl.Description).To(ContainSubstring("at most 1800"))
	})
})
