package chv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// dialGuest connects to a guest vsock port through the hybrid vsock socket of
// Cloud Hypervisor: the host writes "CONNECT <port>\n" and the VMM answers
// "OK <host port>\n". When nothing listens on the port, the VMM closes the
// connection without an answer.
func dialGuest(ctx context.Context, sock string, port uint32) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, err
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(c, "CONNECT %d\n", port); err != nil {
		_ = c.Close()
		return nil, err
	}
	// Read one byte at a time: the bytes after the newline belong to the guest.
	var line []byte
	b := make([]byte, 1)
	for len(line) < 64 {
		if _, err := c.Read(b); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("chv: vsock connect to port %d: %w", port, err)
		}
		if b[0] == '\n' {
			break
		}
		line = append(line, b[0])
	}
	if !strings.HasPrefix(string(line), "OK ") {
		_ = c.Close()
		return nil, fmt.Errorf("chv: vsock connect to port %d: %q", port, line)
	}
	_ = c.SetDeadline(time.Time{})
	return c, nil
}

// dialGuestRetry retries dialGuest until it works or wait passed. After a
// restore the guest needs a moment before it accepts connections again.
func dialGuestRetry(ctx context.Context, sock string, port uint32, wait time.Duration) (net.Conn, error) {
	end := time.Now().Add(wait)
	for {
		c, err := dialGuest(ctx, sock, port)
		if err == nil || time.Now().After(end) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return c, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
