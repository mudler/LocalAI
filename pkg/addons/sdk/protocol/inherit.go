// SPDX-License-Identifier: MIT
package protocol

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// StartupChannel returns the channel the owner writes the startup object on
// and closes when the add-on must stop. Pass it to ReadStartup.
//
// When the owner passed the channels as named handles (always on Windows, see
// contract.StartupHandleEnv), StartupChannel first binds the listener and
// reports its address to the owner, because the owner writes the startup
// object only after it knows the endpoint. Otherwise it is descriptor 4.
//
// An add-on that runs in WSL 2 (contract.ChannelEnv is contract.ChannelStdio)
// gets its channels over its standard streams; StartupChannel then takes
// them and returns the startup stream.
func StartupChannel() (*os.File, error) {
	if stdioMode() {
		c, err := openStdioOnce()
		if err != nil {
			return nil, err
		}
		return c.startup, nil
	}
	h, ok := os.LookupEnv(contract.StartupHandleEnv) //nolint:forbidigo // the owner names the inherited handles in the environment of the add-on process; it is the wire contract, not configuration
	if !ok {
		return os.NewFile(4, "startup"), nil
	}
	if _, err := Listener(); err != nil {
		return nil, err
	}
	return fileFromHandle(h, "startup")
}

// Listener returns the loopback listener the add-on serves the protocol on.
// It is descriptor 3, already bound by the owner, unless the owner passed the
// channels as named handles: then the add-on binds 127.0.0.1 on an ephemeral
// port itself and reports the address on the endpoint handle. Repeated calls
// return the same listener. In the stdio mode (an add-on in WSL 2) it accepts
// the protocol connections that the owner opens over the standard streams.
func Listener() (net.Listener, error) {
	if stdioMode() {
		c, err := openStdioOnce()
		if err != nil {
			return nil, err
		}
		return c.listener(), nil
	}
	h, ok := os.LookupEnv(contract.EndpointHandleEnv) //nolint:forbidigo // the owner names the inherited handles in the environment of the add-on process; it is the wire contract, not configuration
	if !ok {
		return net.FileListener(os.NewFile(3, "listener"))
	}
	reported.once.Do(func() {
		f, err := fileFromHandle(h, "endpoint")
		if err != nil {
			reported.err = err
			return
		}
		reported.l, reported.err = bindAndReport(f)
		// Closing the endpoint handle is the end of the report.
		reported.err = errors.Join(reported.err, f.Close())
		if reported.err != nil && reported.l != nil {
			_ = reported.l.Close()
			reported.l = nil
		}
	})
	return reported.l, reported.err
}

var reported struct {
	once sync.Once
	l    net.Listener
	err  error
}

// bindAndReport binds 127.0.0.1 on an ephemeral port and writes the address
// as one line on w.
func bindAndReport(w io.Writer) (net.Listener, error) {
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	if _, err = io.WriteString(w, l.Addr().String()+"\n"); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// fileFromHandle opens an inherited handle (a file descriptor number on
// POSIX systems) given in decimal.
func fileFromHandle(s, name string) (*os.File, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 || uint64(uintptr(v)) != v {
		return nil, fmt.Errorf("add-on %s handle %q is not valid", name, s)
	}
	f := os.NewFile(uintptr(v), name)
	if f == nil {
		return nil, fmt.Errorf("add-on %s handle %q is not valid", name, s)
	}
	return f, nil
}
