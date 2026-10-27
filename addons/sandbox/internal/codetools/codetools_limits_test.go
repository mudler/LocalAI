//go:build linux || darwin

package codetools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
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

func callTool(t *testing.T, tl Tool, args string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return tl.Run(ctx, json.RawMessage(args))
}

// TestFileToolsTimeOut checks that read, glob and grep stop at their timeout
// and say so in their result.
func TestFileToolsTimeOut(t *testing.T) {
	old := fileToolTimeout
	fileToolTimeout = 50 * time.Millisecond
	t.Cleanup(func() { fileToolTimeout = old })
	tools := toolsOver(limitSandbox{})
	for name, args := range map[string]string{
		"read": `{"path":"home/a"}`,
		"glob": `{"pattern":"*"}`,
		"grep": `{"pattern":"x"}`,
	} {
		got, err := callTool(t, tools[name], args)
		if err != nil || got != "timed out after 50ms" {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
}

// TestOutputCapStopsTheProcess checks that a process that writes more than the
// cap is stopped at the cap, and does not run until its timeout.
func TestOutputCapStopsTheProcess(t *testing.T) {
	old := fileToolTimeout
	fileToolTimeout = 20 * time.Second
	t.Cleanup(func() { fileToolTimeout = old })
	tools := toolsOver(limitSandbox{out: bytes.Repeat([]byte("x"), 100000)})
	for _, c := range []struct{ name, args string }{
		{"read", `{"path":"home/a"}`},
		{"bash", `{"command":"x","timeoutSeconds":20}`},
	} {
		start := time.Now()
		got, err := callTool(t, tools[c.name], c.args)
		if err != nil || !strings.Contains(got, "[output cut: ") || len(got) > MaxOutput+64 {
			t.Errorf("%s: len %d, %v", c.name, len(got), err)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Errorf("%s took %v; the process was not stopped at the cap", c.name, el)
		}
	}
}
