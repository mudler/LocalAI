package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// nodeFlags are the flags that every command with a host shares.
type nodeFlags struct {
	key    string
	boot   list
	listen list
}

func (n *nodeFlags) add(fs *flag.FlagSet) {
	fs.StringVar(&n.key, "key", "", "identity file")
	fs.Var(&n.boot, "bootstrap", "peer to connect to at start, for example an observer (repeatable)")
	fs.Var(&n.listen, "listen", "listen multiaddr (repeatable)")
}

// host starts a host that admits the given peers (the peer it talks to must be
// admitted, or a hole punch cannot come back to it).
func (n *nodeFlags) host(ctx context.Context, admit ...p2pnet.ID) *p2pnet.Host {
	if n.key == "" {
		fatalf("--key is required")
	}
	h, err := p2pnet.New(ctx, p2pnet.Config{
		IdentityPath:   n.key,
		ListenAddrs:    append([]string{}, n.listen...),
		BootstrapPeers: addrInfos(n.boot),
		Gate:           p2pnet.NewAllowlist(admit...),
	})
	if err != nil {
		fatalf("%v", err)
	}
	return h
}

func cmdDial(ctx context.Context, args []string) int {
	// The target comes first: dial PEER --key ...
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fatalf("dial: the first argument is the peer ID")
	}
	target := peerArgs(args[:1])[0]
	fs := flag.NewFlagSet("dial", flag.ExitOnError)
	var nf nodeFlags
	nf.add(fs)
	var hints list
	fs.Var(&hints, "hint", "address of the peer, for example a circuit address through a relay (repeatable)")
	relay := fs.String("relay", "", "relay multiaddr, needed with --pass")
	pass := fs.String("pass", "", "relay pass to present to --relay before the dial")
	mb := fs.Int64("mb", 0, "download this many MiB and report the speed")
	quick := fs.Bool("quick", false, "only connect")
	timeout := fs.Duration("timeout", 60*time.Second, "overall timeout")
	warmup := fs.Duration("warmup", 0, "wait this long after start before the first dial (a hole punch needs the host to know its public address)")
	waitDirect := fs.Duration("wait-direct", 0, "after the first connect, wait up to this long for a direct path")
	_ = fs.Parse(args[1:])

	h := nf.host(ctx, target)
	defer func() { _ = h.Close() }()
	ctx, cancel := context.WithTimeout(ctx, *timeout+*warmup)
	defer cancel()
	time.Sleep(*warmup)

	if *pass != "" {
		if *relay == "" {
			fatalf("dial: --pass needs --relay")
		}
		ai := addrInfos([]string{*relay})[0]
		if err := h.Libp2p().Connect(ctx, ai); err != nil {
			logf("pass: cannot reach the relay: %v", err)
		} else if _, err := relayproto.UsePass(ctx, h.Libp2p(), ai.ID, *pass); err != nil {
			logf("pass: refused: %v", err)
		}
	}
	addrs := make([]p2pnet.Multiaddr, 0, len(hints))
	for _, s := range hints {
		a, err := p2pnet.ParseMultiaddr(s)
		if err != nil {
			fatalf("%v", err)
		}
		addrs = append(addrs, a)
	}
	opts := []p2pnet.DialOption{p2pnet.WithAddrs(addrs...)}

	if !probe(ctx, h, target, opts) {
		return 1
	}
	if *waitDirect > 0 {
		waitForDirect(ctx, h, target, *waitDirect)
	}
	fmt.Println("PATH", pathName(h.Path(target)))
	if *quick {
		return 0
	}
	runChecks(ctx, h, target, opts, *mb)
	fmt.Println("PATH_FINAL", pathName(h.Path(target)))
	return 0
}

// probe opens one HTTP request: the connect test. It prints the RESULT line.
func probe(ctx context.Context, h *p2pnet.Host, target p2pnet.ID, opts []p2pnet.DialOption) bool {
	start := time.Now()
	c := h.HTTPClient(target, opts...)
	defer c.CloseIdleConnections()
	resp, err := c.Get("http://" + target.String() + "/hello")
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if err != nil || resp.StatusCode != http.StatusOK {
		if err == nil {
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		fmt.Printf("RESULT connect=fail err=%q\n", err)
		return false
	}
	fmt.Printf("RESULT connect=ok in=%s\n", time.Since(start).Round(time.Millisecond))
	return true
}

// waitForDirect waits until the path to target is direct or hole punched.
func waitForDirect(ctx context.Context, h *p2pnet.Host, target p2pnet.ID, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if k := h.Path(target).Kind; k == p2pnet.PathDirect || k == p2pnet.PathHolePunched {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// pathName is the text of a path in the lab's output. A limited relayed path
// also shows the limits the relay reported.
func pathName(p p2pnet.Path) string {
	switch p.Kind {
	case p2pnet.PathNone:
		return "not-connected"
	case p2pnet.PathHolePunched:
		return "hole-punched"
	case p2pnet.PathRelayed:
		if p.Limited {
			return fmt.Sprintf("relayed(limited) limit_bytes=%d limit_seconds=%d", p.LimitBytes, int64(p.LimitDuration.Seconds()))
		}
		return "relayed"
	}
	return string(p.Kind)
}

// pathKind is pathName without the limits, for a field that is parsed.
func pathKind(p p2pnet.Path) string {
	return strings.Fields(pathName(p))[0]
}

func runChecks(ctx context.Context, h *p2pnet.Host, target p2pnet.ID, opts []p2pnet.DialOption, mb int64) {
	testSSE(h, target, opts)
	testWebsocket(ctx, h, target, opts)
	testHalfClose(ctx, h, target, opts)
	if mb > 0 {
		testTransfer(h, target, opts, mb)
	}
}

func testSSE(h *p2pnet.Host, target p2pnet.ID, opts []p2pnet.DialOption) {
	c := h.HTTPClient(target, opts...)
	defer c.CloseIdleConnections()
	resp, err := c.Get("http://" + target.String() + "/sse")
	if err != nil {
		fmt.Printf("TEST sse err=%q\n", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	n := 0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data:") {
			n++
		}
	}
	fmt.Printf("TEST sse events=%d/5\n", n)
}

func testWebsocket(ctx context.Context, h *p2pnet.Host, target p2pnet.ID, opts []p2pnet.DialOption) {
	d := &websocket.Dialer{NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return h.Dial(ctx, target, p2pnet.ProtoHTTP, opts...)
	}}
	c, _, err := d.DialContext(ctx, "ws://"+target.String()+"/ws", nil)
	if err != nil {
		fmt.Printf("TEST websocket err=%q\n", err)
		return
	}
	defer func() { _ = c.Close() }()
	var echoed string
	if err = c.WriteMessage(websocket.TextMessage, []byte("ping")); err == nil {
		var b []byte
		if _, b, err = c.ReadMessage(); err == nil {
			echoed = string(b)
		}
	}
	fmt.Printf("TEST websocket echo=%q err=%v\n", echoed, err)
}

func testHalfClose(ctx context.Context, h *p2pnet.Host, target p2pnet.ID, opts []p2pnet.DialOption) {
	c, err := h.Dial(ctx, target, protoHalfClose, opts...)
	if err != nil {
		fmt.Printf("TEST halfclose err=%q\n", err)
		return
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("twelve bytes"))
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	b, err := io.ReadAll(c)
	fmt.Printf("TEST halfclose reply=%q err=%v\n", b, err)
}

// testTransfer downloads mb MiB and prints what arrived, how fast, and the
// path at the end. A circuit that ends early shows as fewer bytes and an error.
func testTransfer(h *p2pnet.Host, target p2pnet.ID, opts []p2pnet.DialOption, mb int64) {
	want := mb << 20
	c := h.HTTPClient(target, opts...)
	defer c.CloseIdleConnections()
	start := time.Now()
	var got int64
	resp, err := c.Get(fmt.Sprintf("http://%s/download?bytes=%d", target, want))
	if err == nil {
		got, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err == nil && got < want {
			err = errors.New("short body")
		}
	}
	el := time.Since(start)
	fmt.Printf("TEST transfer %d/%d bytes in %s = %.1f MB/s err=%v path=%q\n",
		got, want, el.Round(time.Millisecond), float64(got)/1e6/el.Seconds(), err, pathKind(h.Path(target)))
}
