// SPDX-License-Identifier: MIT
package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	yamux "github.com/libp2p/go-yamux/v5"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/stdiomux"
)

// stdioAcceptWait bounds the wait for the startup stream. The owner opens it
// as soon as the process runs, so a long wait means there is no owner.
var stdioAcceptWait = 30 * time.Second

// stdioExitGrace is how long an add-on in the stdio mode may outlive the end
// of the session, after the shutdown budget, before the SDK ends it. In WSL
// the owner cannot kill the process by its handle, so the SDK does it.
var stdioExitGrace = 2 * time.Second

// stdioChannel holds the channels of an add-on started with ChannelEnv set to
// ChannelStdio (see contract.ChannelEnv): the startup stream, the protocol
// connections the owner opens and the relay of the host operation endpoint.
type stdioChannel struct {
	session *yamux.Session
	// startup is the startup channel as the add-on reads it: the startup
	// object with the host endpoint of the relay, then the rest of the stream.
	startup *os.File
	relay   net.Listener
}

// stdioMode reports whether the owner started this process in the stdio mode.
func stdioMode() bool {
	v, _ := os.LookupEnv(contract.ChannelEnv) //nolint:forbidigo // the owner selects the channel mode in the environment of the add-on process; it is the wire contract, not configuration
	return v == contract.ChannelStdio
}

var stdio struct {
	once sync.Once
	c    *stdioChannel
	err  error
}

// openStdioOnce takes the standard streams for the session the first time it
// is called and returns the same channel after.
func openStdioOnce() (*stdioChannel, error) {
	stdio.once.Do(func() {
		conn, err := stdiomux.Stdio()
		if err != nil {
			stdio.err = err
			return
		}
		stdio.c, stdio.err = openStdio(conn, os.Getpid())
		if stdio.err != nil {
			_ = conn.Close()
			return
		}
		go func() {
			<-stdio.c.session.CloseChan()
			time.Sleep(shutdownBudget + stdioExitGrace)
			os.Exit(1)
		}()
	})
	return stdio.c, stdio.err
}

// openStdio runs the add-on's side of the session on conn: it accepts the
// startup stream, writes the process ID on it, reads the startup object and
// serves the relay of the host endpoint when the object names one.
func openStdio(conn net.Conn, pid int) (*stdioChannel, error) {
	session, err := stdiomux.Server(conn)
	if err != nil {
		return nil, err
	}
	first, err := acceptWithin(session, stdioAcceptWait)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("add-on startup stream: %w", err)
	}
	if _, err := io.WriteString(first, strconv.Itoa(pid)+"\n"); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("add-on startup stream: %w", err)
	}
	d := json.NewDecoder(first)
	var object map[string]json.RawMessage
	if err := d.Decode(&object); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("add-on startup object: %w", err)
	}
	c := &stdioChannel{session: session}
	var host string
	if raw, ok := object["hostEndpoint"]; ok {
		if err := json.Unmarshal(raw, &host); err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("add-on startup object: %w", err)
		}
	}
	if host != "" {
		// The host endpoint is a loopback port of the owner, which this
		// side of WSL cannot reach. Serve one here and carry each
		// connection to the owner as a stream.
		l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			_ = session.Close()
			return nil, err
		}
		c.relay = l
		object["hostEndpoint"], _ = json.Marshal("http://" + l.Addr().String())
		go stdiomux.Relay(l, func() (net.Conn, error) { return session.Open(context.Background()) })
		go func() { <-session.CloseChan(); _ = l.Close() }()
	}
	b, err := json.Marshal(object)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	go func() {
		// The end of the stream, or of the session, is the end of this pipe,
		// which is the add-on's signal to stop.
		if _, err := w.Write(append(b, '\n')); err == nil {
			_, _ = io.Copy(w, io.MultiReader(d.Buffered(), first))
		}
		_ = w.Close()
	}()
	c.startup = r
	return c, nil
}

func acceptWithin(s *yamux.Session, wait time.Duration) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := s.Accept()
		done <- result{c, err}
	}()
	select {
	case r := <-done:
		return r.c, r.err
	case <-time.After(wait):
		_ = s.Close()
		return nil, errors.New("the owner opened no startup stream")
	}
}

// listener returns the protocol connections that the owner opens.
func (c *stdioChannel) listener() net.Listener { return sessionListener{c.session} }

type sessionListener struct{ s *yamux.Session }

func (l sessionListener) Accept() (net.Conn, error) { return l.s.Accept() }
func (l sessionListener) Close() error              { return l.s.Close() }
func (l sessionListener) Addr() net.Addr            { return l.s.LocalAddr() }

// InWSL reports whether the owner runs this add-on in WSL 2 for a Windows
// host: the channels come over the standard streams. An add-on uses it to
// turn off what would act on the Windows computer through WSL, such as
// running commands with no isolation.
func InWSL() bool { return stdioMode() }
