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
func Version() string { return "p2pmobile/1" }

// StateCallback receives one JSON object per state change of a forward.
type StateCallback interface {
	OnState(json string)
}

// Pair redeems an invite link and returns the result as JSON. The identity
// file is created when it does not exist. The caller stores the result.
func Pair(identityPath, inviteLink, deviceName string, allowOpenRelay bool) (string, error) {
	link, err := p2ppair.ParseLink(inviteLink)
	if err != nil {
		return "", err
	}
	if link.Expired(time.Now()) {
		return "", pairclient.ErrExpired
	}
	if len(link.Open) > 0 && !allowOpenRelay {
		return "", pairclient.ErrOpenRelay
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h, err := p2pnet.New(ctx, p2pnet.Config{IdentityPath: identityPath, ListenAddrs: []string{}})
	if err != nil {
		return "", err
	}
	defer func() { _ = h.Close() }()
	out, err := pairclient.Redeem(ctx, h, link, pairclient.Request{Name: deviceName, Platform: "android", AllowOpenRelay: allowOpenRelay})
	if err != nil {
		return "", err
	}
	view := map[string]any{
		"device_id": out.DeviceID, "kind": out.Kind, "phrase": out.Phrase,
		"server": map[string]string{"peer_id": link.Peer.String(), "name": out.Server.Name, "version": out.Server.Version},
		"relays": link.RelayAddrStrings(), "addrs": link.AddrStrings(),
	}
	if out.ClientKey != nil {
		view["client_key"] = out.ClientKey
	}
	b, err := json.Marshal(view)
	return string(b), err
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
	addr   string
}

type stateLine struct {
	State string `json:"state"`
	Addr  string `json:"addr,omitempty"`
	Path  string `json:"path,omitempty"`
	Relay string `json:"relay,omitempty"`
	Error string `json:"error,omitempty"`
}

func emit(cb StateCallback, l stateLine) {
	if cb == nil {
		return
	}
	b, _ := json.Marshal(l)
	cb.OnState(string(b))
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
// peer. relays and addrs are JSON arrays of multiaddrs, or empty.
func StartForward(identityPath, peerID, listenAddr, relays, addrs string, cb StateCallback) (*Forwarder, error) {
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
	if _, err := os.Stat(identityPath); err != nil {
		return nil, fmt.Errorf("identity: %w; pair first", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h, err := p2pnet.New(ctx, p2pnet.Config{IdentityPath: identityPath, ListenAddrs: []string{}})
	if err != nil {
		cancel()
		return nil, err
	}
	f, err := h.Forward(ctx, pid, p2pnet.ProtoHTTP, listenAddr,
		p2pnet.WithDialOptions(dial...),
		p2pnet.WithStatus(func(st p2pnet.ForwardStatus) {
			l := stateLine{State: string(st.State), Path: string(st.Path.Kind)}
			if st.Path.Relay != "" {
				l.Relay = st.Path.Relay.String()
			}
			if st.Err != nil {
				l.Error = st.Err.Error()
			}
			emit(cb, l)
		}))
	if err != nil {
		_ = h.Close()
		cancel()
		return nil, err
	}
	fw := &Forwarder{cancel: cancel, host: h, fwd: f, addr: f.Addr().String()}
	emit(cb, stateLine{State: "ready", Addr: fw.addr})
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
	_ = f.fwd.Close()
	_ = f.host.Close()
	f.cancel()
	f.host = nil
}
