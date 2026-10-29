//go:build linux || darwin

package codetools

import (
	"bytes"
	"context"
	"encoding/json"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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

func toolsOver(sb sandbox.Sandbox) map[string]Tool {
	out := map[string]Tool{}
	for _, tl := range NewTools(func(context.Context) (sandbox.Sandbox, func(), error) { return sb, func() {}, nil }) {
		out[tl.Name] = tl
	}
	return out
}

func callTool(tl Tool, args string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return tl.Run(ctx, json.RawMessage(args))
}

var _ = Describe("Sandbox code tool limits", func() {
	// read, glob and grep stop at their timeout and say so in their result.
	It("stops the file tools at their timeout", func() {
		old := fileToolTimeout
		fileToolTimeout = 50 * time.Millisecond
		DeferCleanup(func() { fileToolTimeout = old })
		tools := toolsOver(limitSandbox{})
		for name, args := range map[string]string{
			"read": `{"path":"home/a"}`,
			"glob": `{"pattern":"*"}`,
			"grep": `{"pattern":"x"}`,
		} {
			got, err := callTool(tools[name], args)
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(got).To(Equal("timed out after 50ms"), name)
		}
	})

	// A process that writes more than the cap is stopped at the cap, and does
	// not run until its timeout.
	It("stops a process at the output cap", func() {
		old := fileToolTimeout
		fileToolTimeout = 20 * time.Second
		DeferCleanup(func() { fileToolTimeout = old })
		tools := toolsOver(limitSandbox{out: bytes.Repeat([]byte("x"), 100000)})
		for _, c := range []struct{ name, args string }{
			{"read", `{"path":"home/a"}`},
			{"bash", `{"command":"x","timeoutSeconds":20}`},
		} {
			start := time.Now()
			got, err := callTool(tools[c.name], c.args)
			Expect(err).NotTo(HaveOccurred(), c.name)
			Expect(got).To(ContainSubstring("[output cut: "), c.name)
			Expect(len(got)).To(BeNumerically("<=", MaxOutput+64), c.name)
			Expect(time.Since(start)).To(BeNumerically("<=", 5*time.Second), "%s: the process was not stopped at the cap", c.name)
		}
	})

	// The host stops an action call at ActTimeout. A bash call that may run as
	// long must still leave time to commit its files and to answer.
	It("ends a bash call before the action call does", func() {
		Expect(MaxTimeout + time.Minute).To(BeNumerically("<=", contract.ActTimeout))
	})
})
