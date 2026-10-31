// SPDX-License-Identifier: MIT
package egress

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

// errCap says a call moved as many bytes as it may.
var errCap = errors.New("the command moved as many bytes as one call may")

// errHead says a request head is too large or malformed.
var errHead = errors.New("request head too large or malformed")

// peekHead waits until the buffer holds a whole request head and returns it,
// without consuming it. The buffer is the size limit of a head. The proxy
// needs the raw head because http.ReadRequest drops the Host header of an
// absolute-form request, and a Host that differs from the URL must be refused.
func peekHead(br *bufio.Reader) ([]byte, error) {
	for {
		if k := br.Buffered(); k > 0 {
			b, _ := br.Peek(k)
			if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
				return b[:i+4], nil
			}
			if k >= br.Size() {
				return nil, errHead
			}
		}
		// Wait for one more byte than the buffer holds.
		if _, err := br.Peek(br.Buffered() + 1); err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				return nil, errHead
			}
			return nil, err
		}
	}
}

// hostHeaders returns the values of the Host header lines of a raw head.
func hostHeaders(head []byte) []string {
	var out []string
	lines := strings.Split(string(head), "\r\n")
	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(l, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "host") {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

// blockText is the one line the model reads in the tool output after a block.
func blockText(host string, port int, v Verdict) string {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	switch v.Reason {
	case "no-answer":
		return "Blocked by LocalAI: " + target + " is not allowed for this chat (the user did not answer). Ask the user to allow it."
	case "ceiling":
		return "Blocked by LocalAI: " + target + " is blocked by the server's setting for the Sandbox network, not by a missing approval. Asking the user to approve it will not help and retrying will fail the same way. Tell the user an administrator can change it in Settings > Tools > Sandbox network."
	case "idle":
		return "Blocked by LocalAI: no command of this chat is running, so the network is closed."
	case "card", "asked":
		return "Blocked by LocalAI: " + target + " is not allowed for this chat (" + v.Reason + "). The user was asked."
	}
	return "Blocked by LocalAI: " + target + " is not allowed for this chat (" + v.Reason + "). Ask the user to allow it."
}

func writeStatus(w io.Writer, code int, text string) {
	body := text + "\n"
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}

func (p *Proxy) serve(ctx context.Context, key string, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	b := p.box(key)
	op, callCtx, done := b.call()
	defer done()
	if op == "" {
		// No call runs: a background process of an earlier call. Nothing
		// is asked; the line goes out with the next call.
		b.record(Event{Verdict: "block", Reason: "idle", At: p.o.Now()}, true)
		writeStatus(conn, http.StatusForbidden, blockText("", 0, Verdict{Reason: "idle"}))
		return
	}
	if !p.admit(b) {
		b.record(Event{Verdict: "block", Reason: "limit", At: p.o.Now()}, false)
		writeStatus(conn, http.StatusServiceUnavailable, "Blocked by LocalAI: too many connections from this command; slow down.")
		return
	}
	defer p.release(b)
	// The tunnels end with the call, or with the add-on.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(callCtx, cancel)
	defer stop()
	go func() { <-ctx.Done(); _ = conn.Close() }()

	br := bufio.NewReaderSize(conn, p.o.Limits.HeaderBytes)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(p.o.Limits.Header))
		head, err := peekHead(br)
		var req *http.Request
		if err == nil {
			req, err = http.ReadRequest(br)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				writeStatus(conn, http.StatusBadRequest, "LocalAI proxy: the request could not be read.")
			}
			return
		}
		_ = conn.SetReadDeadline(time.Time{})
		if hosts := hostHeaders(head); len(hosts) > 1 || (len(hosts) == 1 && req.URL != nil && req.URL.Host != "" && !strings.EqualFold(hosts[0], req.URL.Host)) {
			// A Host header that names another host than the URL is a
			// request for a host the proxy did not decide.
			writeStatus(conn, http.StatusBadRequest, "LocalAI proxy: the Host header does not match the URL.")
			return
		}
		switch {
		case req.Method == http.MethodConnect:
			p.connect(ctx, b, op, conn, br, req)
			return
		case req.URL != nil && strings.EqualFold(req.URL.Scheme, "http") && req.URL.Host != "" && req.Header.Get("Upgrade") == "":
			if !p.forward(ctx, b, op, conn, req) {
				return
			}
		default:
			// Origin form, https:// in absolute form, an Upgrade: the proxy
			// serves CONNECT and plain http:// only.
			writeStatus(conn, http.StatusBadRequest, "LocalAI proxy: use CONNECT for https and an absolute http:// URL for http.")
			return
		}
	}
}

// target checks the host of a request with the guard's host rules. It does
// not resolve, so a host the guard refuses is never looked up and does not
// leak through DNS.
func (p *Proxy) target(host string, port int) (string, Verdict, bool) {
	h, err := p.guard(port, false).CheckHost(host, port)
	if err == nil {
		return h, Verdict{}, true
	}
	if errors.Is(err, safehttp.ErrBlockedAddress) && !errors.Is(err, safehttp.ErrMetadataAddress) && !errors.Is(err, safehttp.ErrSelfAddress) {
		// A private literal goes to the policy, which allows it only with
		// the private switch and an administrator's rule; a name such as
		// localhost never does.
		if a, perr := netip.ParseAddr(strings.Trim(host, "[]")); perr == nil && a.Zone() == "" {
			return a.String(), Verdict{}, true
		}
	}
	reason := "host"
	switch {
	case errors.Is(err, safehttp.ErrMetadataAddress):
		reason = "metadata"
	case errors.Is(err, safehttp.ErrSelfAddress):
		reason = "self"
	case errors.Is(err, safehttp.ErrBlockedAddress):
		reason = "private"
	}
	return "", Verdict{Action: "block", Reason: reason}, false
}

func splitPort(hostport, def string) (string, int, error) {
	host, ps, err := net.SplitHostPort(hostport)
	if err != nil {
		if def == "" {
			return "", 0, err
		}
		host, ps = hostport, def
	}
	port, err := safehttp.ParsePort(ps)
	return host, port, err
}

func (p *Proxy) connect(ctx context.Context, b *box, op string, conn net.Conn, br *bufio.Reader, req *http.Request) {
	start := p.o.Now()
	host, port, err := splitPort(req.Host, "")
	if err != nil {
		writeStatus(conn, http.StatusBadRequest, "LocalAI proxy: CONNECT needs host:port.")
		return
	}
	if port == 80 && p.o.TLSPort != 80 {
		writeStatus(conn, http.StatusBadRequest, "LocalAI proxy: plain http goes as an absolute http:// URL, not through CONNECT.")
		return
	}
	host, v, ok := p.target(host, port)
	a := CheckArgs{Host: host, Port: port, Kind: "connect", TLS: port == p.o.TLSPort}
	if !ok {
		b.record(Event{Host: req.Host, Port: port, Kind: a.Kind, Verdict: "block", Reason: v.Reason, At: start}, false)
		writeStatus(conn, http.StatusForbidden, blockText(req.Host, port, v))
		return
	}
	v = p.verdict(ctx, b, op, a)
	if v.Action != "allow" {
		writeStatus(conn, http.StatusForbidden, blockText(host, port, v))
		return
	}
	up, v, err := p.dial(ctx, b, op, a, v)
	if err != nil {
		b.record(Event{Host: host, Port: port, Kind: a.Kind, Verdict: "block", Reason: dialReason(err), At: start}, false)
		if v.Action != "allow" {
			writeStatus(conn, http.StatusForbidden, blockText(host, port, v))
		} else {
			writeStatus(conn, http.StatusBadGateway, "LocalAI proxy: "+host+" could not be reached ("+dialReason(err)+").")
		}
		return
	}
	defer func() { _ = up.Close() }()
	addr := up.RemoteAddr().String()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	var first []byte
	if a.TLS {
		_ = conn.SetReadDeadline(p.o.Now().Add(p.o.Limits.Hello))
		rec, sni, err := readClientHello(br)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil || !strings.EqualFold(strings.TrimSuffix(sni, "."), host) {
			// Not TLS, no name, or the name of another tenant of the same
			// front (domain fronting).
			b.record(Event{Host: host, Port: port, Kind: a.Kind, Verdict: "block", Reason: "sni", Addr: addr, At: start}, false)
			return
		}
		first = rec
	}
	upN, downN := p.pipe(ctx, b, conn, br, up, first)
	b.record(Event{Host: host, Port: port, Kind: a.Kind, Verdict: "done", Reason: v.Reason, Addr: addr, BytesUp: upN, BytesDown: downN,
		DurationMS: p.o.Now().Sub(start).Milliseconds(), At: start}, false)
}

// room is how many bytes a direction may still move in the call.
func (b *box) room(up bool, capUp, capDown int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if up {
		return capUp - b.up
	}
	return capDown - b.down
}

// take counts n bytes of a direction against the call's cap.
func (b *box) take(up bool, n int64, capUp, capDown int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if up {
		b.up += n
		return b.up <= capUp
	}
	b.down += n
	return b.down <= capDown
}

// pipe copies both ways until either side closes, the tunnel is idle, the
// call's byte cap is reached or ctx ends. first is written upstream before the
// guest's bytes. It returns the bytes moved each way.
func (p *Proxy) pipe(ctx context.Context, b *box, guest net.Conn, guestR io.Reader, up net.Conn, first []byte) (int64, int64) {
	var upN, downN atomic.Int64
	var lastIO atomic.Int64
	lastIO.Store(p.o.Now().UnixNano())
	l := p.o.Limits
	stop := make(chan struct{})
	closeBoth := func() { _ = guest.Close(); _ = up.Close() }
	if len(first) > 0 {
		if _, err := up.Write(first); err != nil {
			return 0, 0
		}
		upN.Add(int64(len(first)))
		b.take(true, int64(len(first)), l.UpBytes, l.DownBytes)
	}
	copyDir := func(dst io.Writer, src io.Reader, isUp bool, n *atomic.Int64) {
		buf := make([]byte, 32<<10)
		for {
			room := b.room(isUp, l.UpBytes, l.DownBytes)
			if room <= 0 {
				closeBoth()
				return
			}
			chunk := buf
			if int64(len(chunk)) > room {
				chunk = chunk[:room]
			}
			k, err := src.Read(chunk)
			if k > 0 {
				lastIO.Store(p.o.Now().UnixNano())
				if !b.take(isUp, int64(k), l.UpBytes, l.DownBytes) {
					closeBoth()
					return
				}
				if _, werr := dst.Write(buf[:k]); werr != nil {
					closeBoth()
					return
				}
				n.Add(int64(k))
			}
			if err != nil {
				closeBoth()
				return
			}
		}
	}
	done := make(chan struct{}, 2)
	go func() { copyDir(up, guestR, true, &upN); done <- struct{}{} }()
	go func() { copyDir(guest, up, false, &downN); done <- struct{}{} }()
	// The idle watch: a tunnel with no bytes either way for Idle is closed.
	go func() {
		t := time.NewTicker(min(l.Idle/4, time.Second))
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				closeBoth()
				return
			case <-t.C:
				if p.o.Now().Sub(time.Unix(0, lastIO.Load())) >= l.Idle {
					closeBoth()
					return
				}
			}
		}
	}()
	<-done
	<-done
	close(stop)
	return upN.Load(), downN.Load()
}

// hopHeaders are dropped from a forwarded request and response.
var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func dropHop(h http.Header) {
	for _, f := range h.Values("Connection") {
		for name := range strings.SplitSeq(f, ",") {
			if name = textproto.TrimString(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

// countReader counts the bytes of a body against the call's cap.
type countReader struct {
	r    io.Reader
	b    *box
	up   bool
	l    Limits
	n    int64
	fail bool
}

func (c *countReader) Read(p []byte) (int, error) {
	room := c.b.room(c.up, c.l.UpBytes, c.l.DownBytes)
	if room <= 0 {
		c.fail = true
		return 0, errCap
	}
	if int64(len(p)) > room {
		p = p[:room]
	}
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		if !c.b.take(c.up, int64(n), c.l.UpBytes, c.l.DownBytes) {
			c.fail = true
			return n, errCap
		}
	}
	return n, err
}

func (c *countReader) Close() error {
	if rc, ok := c.r.(io.Closer); ok {
		return rc.Close()
	}
	return nil
}

// forward serves one absolute-form plain HTTP request. Each request of a
// kept-alive guest connection is decided on its own. It says whether the
// connection may serve another request.
func (p *Proxy) forward(ctx context.Context, b *box, op string, conn net.Conn, req *http.Request) bool {
	start := p.o.Now()
	if req.URL.User != nil {
		writeStatus(conn, http.StatusBadRequest, "LocalAI proxy: URLs with a user name or password are not forwarded.")
		return false
	}
	host, port, err := splitPort(req.URL.Host, "80")
	if err != nil {
		writeStatus(conn, http.StatusBadRequest, "LocalAI proxy: the URL has no valid host.")
		return false
	}
	host, v, ok := p.target(host, port)
	a := CheckArgs{Host: host, Port: port, Kind: "http"}
	if !ok {
		b.record(Event{Host: req.URL.Host, Port: port, Kind: a.Kind, Verdict: "block", Reason: v.Reason, At: start}, false)
		writeStatus(conn, http.StatusForbidden, blockText(req.URL.Host, port, v))
		return false
	}
	v = p.verdict(ctx, b, op, a)
	if v.Action != "allow" {
		writeStatus(conn, http.StatusForbidden, blockText(host, port, v))
		return false
	}
	var addr string
	var dialV = v
	tr := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(dctx context.Context, _, _ string) (net.Conn, error) {
			c, v2, err := p.dial(dctx, b, op, a, v)
			dialV = v2
			if err == nil {
				addr = c.RemoteAddr().String()
			}
			return c, err
		},
		ResponseHeaderTimeout:  60 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer tr.CloseIdleConnections()
	out := req.Clone(ctx)
	out.RequestURI = ""
	out.Host = ""
	dropHop(out.Header)
	if req.Body != nil && req.Body != http.NoBody {
		out.Body = &countReader{r: req.Body, b: b, up: true, l: p.o.Limits}
	}
	resp, err := tr.RoundTrip(out)
	if err != nil {
		reason := dialReason(err)
		if errors.Is(err, errCap) {
			reason = "limit"
		}
		b.record(Event{Host: host, Port: port, Kind: a.Kind, Verdict: "block", Reason: reason, Addr: addr, At: start}, false)
		if dialV.Action != "allow" {
			writeStatus(conn, http.StatusForbidden, blockText(host, port, dialV))
		} else {
			writeStatus(conn, http.StatusBadGateway, "LocalAI proxy: "+host+" could not be reached ("+reason+").")
		}
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	dropHop(resp.Header)
	body := &countReader{r: resp.Body, b: b, up: false, l: p.o.Limits}
	resp.Body = body
	// The proxy follows no redirect: a 3xx goes to the guest, whose next
	// request is checked again.
	// The upstream connection is never reused (one per request, so each is
	// dialed after its own check); the guest's may be.
	keep := !req.Close
	resp.Close = !keep
	werr := resp.Write(conn)
	var upN int64
	if cr, ok := out.Body.(*countReader); ok {
		upN = cr.n
	}
	b.record(Event{Host: host, Port: port, Kind: a.Kind, Verdict: "done", Reason: v.Reason, Addr: addr, BytesUp: upN, BytesDown: body.n,
		DurationMS: p.o.Now().Sub(start).Milliseconds(), At: start}, false)
	return keep && werr == nil && !body.fail
}
