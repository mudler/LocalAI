package guestagent

import (
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
)

// Forward accepts connections on l and connects each one with dial, then
// copies both ways until both sides finished. A transient accept error is
// retried (RetryListener); Forward returns only when l is closed.
func Forward(l net.Listener, dial func() (net.Conn, error)) error {
	rl := RetryListener{Listener: l}
	for {
		c, err := rl.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer logPanic("forward")
			up, err := dial()
			if err != nil {
				_ = c.Close()
				return
			}
			Pipe(c, up)
		}()
	}
}

type closeWriter interface{ CloseWrite() error }

// Pipe copies a to b and b to a. When one direction ends it closes the
// write side of the other connection, so a half-close reaches the peer. It
// closes both connections at the end.
func Pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	half := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Add(2)
	go half(a, b)
	go half(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// ParsePorts reads the ports of "sandbox.ports=3128,7000" from a kernel
// command line. It skips values that are not ports.
func ParsePorts(cmdline string) []int {
	var out []int
	for w := range strings.FieldsSeq(cmdline) {
		v, ok := strings.CutPrefix(w, "sandbox.ports=")
		if !ok {
			continue
		}
		out = append(out, ParsePortList(v)...)
	}
	return out
}

// ParsePortList reads a comma-separated list of ports such as "3128,7000". It
// skips values that are not ports.
func ParsePortList(v string) []int {
	var out []int
	for s := range strings.SplitSeq(v, ",") {
		if p, err := strconv.Atoi(s); err == nil && p > 0 && p < 65536 && p != AgentPort {
			out = append(out, p)
		}
	}
	return out
}
