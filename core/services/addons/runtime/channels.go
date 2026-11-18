// SPDX-License-Identifier: MIT
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// channels hands an add-on process its loopback listener and its startup
// channel. There are two ways (see contract.StartupHandleEnv):
//
//   - descriptors (Linux and macOS): the owner binds the listener and the
//     child inherits it as descriptor 3, and the startup pipe as descriptor 4.
//     The endpoint is known before the start.
//   - reported (Windows): the child inherits two pipe handles named in its
//     environment, binds its own listener and reports the address on one of
//     them. The endpoint is known only after the start.
type channels struct {
	// parent is the owner's write end of the startup pipe. Closing it is the
	// add-on's signal to stop.
	parent *os.File
	// endpoint is the endpoint of the child; empty until reported.
	endpoint string
	// report is the owner's read end of the endpoint pipe (reported only).
	report *os.File
	// child are the child's ends, closed once the child holds its own
	// copies.
	child []*os.File
	// held is closed when the launch returns: the owner's listener copies.
	held []io.Closer
}

// openDescriptorChannels binds the listener for the child and makes the
// startup pipe. The child gets them as descriptors 3 and 4.
func openDescriptorChannels(cmd *exec.Cmd) (*channels, error) {
	listener, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		return nil, ErrRuntime
	}
	socket, e := listener.File()
	if e != nil {
		_ = listener.Close()
		return nil, ErrRuntime
	}
	child, parent, e := os.Pipe()
	if e != nil {
		_ = socket.Close()
		_ = listener.Close()
		return nil, ErrRuntime
	}
	cmd.ExtraFiles = []*os.File{socket, child}
	return &channels{parent: parent, endpoint: "http://" + listener.Addr().String(), child: []*os.File{child}, held: []io.Closer{listener, socket}}, nil
}

// openReportedChannels makes the startup and endpoint pipes, has the child
// inherit its ends and names them in its environment.
func openReportedChannels(cmd *exec.Cmd) (*channels, error) {
	startupChild, parent, e := os.Pipe()
	if e != nil {
		return nil, ErrRuntime
	}
	report, endpointChild, e := os.Pipe()
	if e != nil {
		_ = startupChild.Close()
		_ = parent.Close()
		return nil, ErrRuntime
	}
	c := &channels{parent: parent, report: report, child: []*os.File{startupChild, endpointChild}}
	handles, e := inheritFiles(cmd, startupChild, endpointChild)
	if e != nil {
		c.release()
		_ = parent.Close()
		return nil, ErrRuntime
	}
	cmd.Env = append(cmd.Env, contract.StartupHandleEnv+"="+handles[0], contract.EndpointHandleEnv+"="+handles[1])
	return c, nil
}

// started closes the child's ends in the owner once the child runs. In the
// reported way this is required: the end of the report is the close of the
// child's copy, which is not seen while the owner holds one too.
func (c *channels) started() {
	if c.report == nil {
		return
	}
	for _, f := range c.child {
		_ = f.Close()
	}
	c.child = nil
}

// release closes everything the owner holds except the startup pipe.
func (c *channels) release() {
	for _, f := range c.child {
		_ = f.Close()
	}
	for _, h := range c.held {
		_ = h.Close()
	}
	if c.report != nil {
		_ = c.report.Close()
	}
}

// awaitEndpoint waits for the child to report its endpoint. The read runs
// apart, since a Windows pipe has no deadline: it ends when the child closes
// its end, also when it dies.
func (c *channels) awaitEndpoint(ctx context.Context) (string, error) {
	if c.endpoint != "" {
		return c.endpoint, nil
	}
	type result struct {
		b []byte
		e error
	}
	done := make(chan result, 1)
	go func() {
		b, e := io.ReadAll(io.LimitReader(c.report, contract.MaxEndpointReport+1))
		done <- result{b, e}
	}()
	select {
	case <-ctx.Done():
		return "", ErrRuntime
	case r := <-done:
		if r.e != nil {
			return "", ErrRuntime
		}
		endpoint, e := parseEndpointReport(r.b)
		if e != nil {
			return "", e
		}
		c.endpoint = endpoint
		return endpoint, nil
	}
}

// parseEndpointReport accepts exactly one line "127.0.0.1:<port>\n" and
// returns its endpoint URL. The owner binds 127.0.0.1 on the other platforms,
// so a child may not report anything else.
func parseEndpointReport(b []byte) (string, error) {
	if len(b) > contract.MaxEndpointReport || !bytes.HasSuffix(b, []byte("\n")) {
		return "", ErrRuntime
	}
	line := string(b[:len(b)-1])
	host, port, e := net.SplitHostPort(line)
	if e != nil || host != "127.0.0.1" {
		return "", ErrRuntime
	}
	n, e := strconv.ParseUint(port, 10, 16)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != port {
		return "", ErrRuntime
	}
	return "http://" + line, nil
}

// writeStartup writes the startup object with a bound wait. A pipe that
// takes a deadline gets one; a Windows pipe does not, so the write runs apart
// and the caller stops the process when ctx ends, which ends the write.
func writeStartup(ctx context.Context, w *os.File, until time.Time, v Startup) error {
	if w.SetWriteDeadline(until) == nil {
		return json.NewEncoder(w).Encode(v)
	}
	done := make(chan error, 1)
	go func() { done <- json.NewEncoder(w).Encode(v) }()
	select {
	case e := <-done:
		return e
	case <-ctx.Done():
		return errors.Join(ErrRuntime, ctx.Err())
	}
}
