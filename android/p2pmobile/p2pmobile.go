// SPDX-License-Identifier: MIT

// Package p2pmobile is a small facade over p2pnet and pairclient for
// gomobile bind. It exports only types gomobile supports: strings, bools,
// error, interfaces and pointers to structs.
//
// It imports only p2pnet, p2ppair and pairclient, never libp2p directly, so
// the import boundary of p2pnet holds for the Android library too.
package p2pmobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

// Version names the library and its API revision. The app calls it first to
// prove that the native library loaded; bump the number when the exported
// API changes.
//
// p2pmobile/3 adds the routes state line of StartForward; the exports and
// their arguments are those of p2pmobile/2, so a caller of p2pmobile/2 works
// unchanged if it ignores state lines it does not know.
//
// p2pmobile/4 adds device_credential, sign_in and owner_name to the result
// of Pair and PairJSON for a sign-in invite; the exports are unchanged.
func Version() string { return "p2pmobile/4" }

// StateCallback receives one JSON object per state change of a forward.
type StateCallback interface {
	OnState(json string)
}

// pairTimeout bounds a pairing. A server reached only through a hint relay
// needs up to two waits for a hole punch (p2pnet.DefaultPunchWait) on top
// of the dials.
const pairTimeout = 2 * time.Minute

// Error codes of PairJSON. They are stable: the app chooses its own words
// for each one.
const (
	CodeInvalidLink  = "invalid_link"
	CodeExpired      = "expired"
	CodeOpenRelay    = "open_relay"
	CodeGone         = "gone"
	CodeTooManyTries = "too_many_tries"
	CodeWrongServer  = "wrong_server"
	CodeNeedsRelay   = "needs_relay"
	CodeNoRoute      = "no_route"
	CodeUnreachable  = "unreachable"
	CodeFailed       = "failed"
)

// newHost starts the device host. It listens on ports that the system
// picks, so that a hole punch can reach it; its empty allowlist refuses
// every inbound connection except the one of a hole punch that a dial of
// this host waits for.
func newHost(ctx context.Context, identityPath string) (*p2pnet.Host, error) {
	return p2pnet.New(ctx, p2pnet.Config{IdentityPath: identityPath})
}

// pair redeems an invite link and returns the view the app stores.
func pair(identityPath, inviteLink, deviceName string, allowOpenRelay bool) (map[string]any, error) {
	link, err := p2ppair.ParseLink(inviteLink)
	if err != nil {
		return nil, &linkError{err}
	}
	if link.Expired(time.Now()) {
		return nil, pairclient.ErrExpired
	}
	if len(link.Open) > 0 && !allowOpenRelay {
		return nil, pairclient.ErrOpenRelay
	}
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout)
	defer cancel()
	h, err := newHost(ctx, identityPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = h.Close() }()
	out, err := pairclient.Redeem(ctx, h, link, pairclient.Request{Name: deviceName, Platform: "android", AllowOpenRelay: allowOpenRelay})
	if err != nil {
		return nil, err
	}
	view := map[string]any{
		"device_id": out.DeviceID, "kind": out.Kind, "phrase": out.Phrase,
		"server": map[string]string{"peer_id": link.Peer.String(), "name": out.Server.Name, "version": out.Server.Version},
		"relays": link.RelayAddrStrings(), "addrs": link.AddrStrings(), "hints": link.HintAddrStrings(),
	}
	if out.ClientKey != nil {
		view["client_key"] = out.ClientKey
	}
	// The credential of a sign-in invite goes to the caller, which keeps it
	// out of the WebView and its logs. Absent for other invites and for
	// servers without sign-in invites.
	if out.DeviceCredential != "" {
		view["device_credential"] = out.DeviceCredential
	}
	if out.SignIn {
		view["sign_in"] = true
	}
	if out.OwnerName != "" {
		view["owner_name"] = out.OwnerName
	}
	return view, nil
}

// linkError marks an invite link that does not parse.
type linkError struct{ err error }

func (e *linkError) Error() string { return e.err.Error() }
func (e *linkError) Unwrap() error { return e.err }

// Pair redeems an invite link and returns the result as JSON. The identity
// file is created when it does not exist. The caller stores the result.
// PairJSON gives the same result with a stable error code.
func Pair(identityPath, inviteLink, deviceName string, allowOpenRelay bool) (string, error) {
	view, err := pair(identityPath, inviteLink, deviceName, allowOpenRelay)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(view)
	return string(b), err
}

// PairJSON redeems an invite link like Pair and never fails. It answers
// {"ok":true,"result":{...}} with the object of Pair, or
// {"ok":false,"code":"...","detail":"..."} where code is one of the Code
// constants and detail is the error text, for logs.
func PairJSON(identityPath, inviteLink, deviceName string, allowOpenRelay bool) string {
	view, err := pair(identityPath, inviteLink, deviceName, allowOpenRelay)
	var b []byte
	if err != nil {
		b, _ = json.Marshal(map[string]any{"ok": false, "code": errorCode(err), "detail": err.Error()})
	} else {
		b, _ = json.Marshal(map[string]any{"ok": true, "result": view})
	}
	return string(b)
}

// errorCode maps a pairing error to its code. The order matters: the
// specific causes are wrapped with ErrUnreachable.
func errorCode(err error) string {
	var le *linkError
	switch {
	case errors.As(err, &le):
		return CodeInvalidLink
	case errors.Is(err, pairclient.ErrExpired):
		return CodeExpired
	case errors.Is(err, pairclient.ErrOpenRelay):
		return CodeOpenRelay
	case errors.Is(err, pairclient.ErrGone):
		return CodeGone
	case errors.Is(err, pairclient.ErrTooManyTries):
		return CodeTooManyTries
	case errors.Is(err, pairclient.ErrWrongServer):
		return CodeWrongServer
	case errors.Is(err, pairclient.ErrNeedsRelay), errors.Is(err, p2pnet.ErrNoDirectPath):
		return CodeNeedsRelay
	case errors.Is(err, pairclient.ErrNoRoute):
		return CodeNoRoute
	case errors.Is(err, pairclient.ErrUnreachable):
		return CodeUnreachable
	}
	return CodeFailed
}

// PeerID returns the peer ID of an identity file, creating none.
func PeerID(identityPath string) (string, error) {
	id, err := p2pnet.IdentityPeerID(identityPath)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// Forwarder forwards a local loopback port to a peer.
type Forwarder struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	host   *p2pnet.Host
	fwd    *p2pnet.Forwarder
	routes *pairclient.RouteWatcher
	addr   string
}

// routesInterval is how often a forward that stays up asks the server for
// its routes again; a variable for the tests.
var routesInterval = pairclient.RoutesInterval

// routesLine is the state line of the routes of the server: the lists the
// app stores in place of the ones of the invite. The lists are never null.
type routesLine struct {
	State  string   `json:"state"`
	Addrs  []string `json:"addrs"`
	Relays []string `json:"relays"`
	Hints  []string `json:"hints"`
}

func newRoutesLine(r pairclient.Routes) routesLine {
	l := routesLine{State: "routes", Addrs: r.Addrs, Relays: r.Relays, Hints: r.Hints}
	for _, p := range []*[]string{&l.Addrs, &l.Relays, &l.Hints} {
		if *p == nil {
			*p = []string{}
		}
	}
	return l
}

// serialCallback passes the lines of the forwarder and of the route watcher,
// which come from different goroutines, to cb one at a time.
type serialCallback struct {
	mu sync.Mutex
	cb StateCallback
}

func (s *serialCallback) emit(v any) {
	if s.cb == nil {
		return
	}
	b, _ := json.Marshal(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cb.OnState(string(b))
}

type stateLine struct {
	State string `json:"state"`
	Addr  string `json:"addr,omitempty"`
	Path  string `json:"path,omitempty"`
	Relay string `json:"relay,omitempty"`
	Error string `json:"error,omitempty"`
	// Code is CodeNeedsRelay when the server was reached only through a
	// hint relay and no hole punch opened a direct path.
	Code string `json:"code,omitempty"`
}

// forwardLine is the state line of a forwarder status.
func forwardLine(st p2pnet.ForwardStatus) stateLine {
	l := stateLine{State: string(st.State), Path: string(st.Path.Kind)}
	if st.Path.Relay != "" {
		l.Relay = st.Path.Relay.String()
	}
	if st.Err != nil {
		l.Error = st.Err.Error()
		if errors.Is(st.Err, p2pnet.ErrNoDirectPath) {
			l.Code = CodeNeedsRelay
		}
	}
	return l
}

func parseList(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("not a JSON array of strings: %w", err)
	}
	return out, nil
}

// StartForward listens on listenAddr and forwards each connection to the
// peer. relays, addrs and hints are JSON arrays of multiaddrs, or empty.
// hints are the public relays of the invite (key h): they only help a hole
// punch and never carry data.
//
// cb gets the state lines: ready, up, down and closed, and a routes line
// {"state":"routes","addrs":[...],"relays":[...],"hints":[...]} after each
// connection that opens and every 10 minutes while the forward stays up.
// The routes line has the current routes of the server; the caller stores
// them and passes them to the next StartForward.
func StartForward(identityPath, peerID, listenAddr, relays, addrs, hints string, cb StateCallback) (*Forwarder, error) {
	pid, err := p2pnet.ParsePeerID(peerID)
	if err != nil {
		return nil, err
	}
	rl, err := parseList(relays)
	if err != nil {
		return nil, err
	}
	al, err := parseList(addrs)
	if err != nil {
		return nil, err
	}
	hl, err := parseList(hints)
	if err != nil {
		return nil, err
	}
	var dial []p2pnet.DialOption
	if len(rl) > 0 {
		o, err := p2pnet.RelaysOption(rl)
		if err != nil {
			return nil, err
		}
		dial = append(dial, o)
	}
	if len(al) > 0 {
		o, err := p2pnet.AddrsOption(al)
		if err != nil {
			return nil, err
		}
		dial = append(dial, o)
	}
	if len(hl) > 0 {
		o, err := p2pnet.HintsOption(hl)
		if err != nil {
			return nil, err
		}
		dial = append(dial, o)
	}
	if _, err := os.Stat(identityPath); err != nil {
		return nil, fmt.Errorf("identity: %w; pair first", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h, err := newHost(ctx, identityPath)
	if err != nil {
		cancel()
		return nil, err
	}
	out := &serialCallback{cb: cb}
	client := h.HTTPClient(pid, dial...)
	routes := pairclient.WatchRoutes(ctx,
		func(ctx context.Context) (pairclient.Routes, error) { return pairclient.FetchRoutes(ctx, client, pid) },
		routesInterval,
		func(r pairclient.Routes) { out.emit(newRoutesLine(r)) },
		nil)
	f, err := h.Forward(ctx, pid, p2pnet.ProtoHTTP, listenAddr,
		p2pnet.WithDialOptions(dial...),
		p2pnet.WithStatus(func(st p2pnet.ForwardStatus) {
			out.emit(forwardLine(st))
			routes.Connected(st.State == p2pnet.ForwardUp)
		}))
	if err != nil {
		routes.Stop()
		_ = h.Close()
		cancel()
		return nil, err
	}
	fw := &Forwarder{cancel: cancel, host: h, fwd: f, routes: routes, addr: f.Addr().String()}
	out.emit(stateLine{State: "ready", Addr: fw.addr})
	return fw, nil
}

// Addr is the local address the forwarder listens on.
func (f *Forwarder) Addr() string { return f.addr }

// Stop closes the listener, the connections and the host. It is safe to
// call more than once.
func (f *Forwarder) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.host == nil {
		return
	}
	f.routes.Stop()
	_ = f.fwd.Close()
	_ = f.host.Close()
	f.cancel()
	f.host = nil
}
