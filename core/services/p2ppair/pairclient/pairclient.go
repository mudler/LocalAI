// Package pairclient redeems an invite link from the device side: it uses
// the relay pass of the link, dials the server named in the link over P2P and
// posts the code to its redeem route. The command `local-ai p2p pair` and
// the remotes service share it, so a device and a server that adds a remote
// pair the same way.
package pairclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// RedeemPath is the route of the server that redeems an invite.
const RedeemPath = "/api/p2p/redeem"

// passConnectTimeout bounds the connection to one relay while the pass is
// presented.
const passConnectTimeout = 15 * time.Second

// Errors a caller can switch on with errors.Is.
var (
	// ErrExpired means the link expired before it was used.
	ErrExpired = errors.New("the invite has expired: ask for a new one")
	// ErrOpenRelay means the link names an open relay and the caller did
	// not allow one.
	ErrOpenRelay = errors.New("the invite names an open relay, which anyone can use")
	// ErrGone means the server refused the code: it was used, cancelled or
	// expired. The server gives one answer for all three on purpose.
	ErrGone = errors.New("the invite is not valid any more: it was used, cancelled or expired")
	// ErrTooManyTries means the server saw too many wrong codes from this
	// device or its relay.
	ErrTooManyTries = errors.New("too many wrong codes from this device or relay: wait a while and try again")
	// ErrUnreachable means the server named in the link could not be reached.
	ErrUnreachable = errors.New("cannot reach the server named in the invite")
	// ErrNoRoute is wrapped with ErrUnreachable when the link names no
	// address and no relay: the device can find the server only by local
	// network discovery, which a device in connect mode does not run.
	ErrNoRoute = errors.New("the invite names no address and no relay")
	// ErrNeedsRelay is wrapped with ErrUnreachable when the device reached
	// the server only through a public relay of the link (a hint) and no
	// hole punch opened a direct connection. The code was not sent, so the
	// invite stays pending.
	ErrNeedsRelay = errors.New("this device and the server cannot connect directly: " +
		"their networks block hole punching. The server needs a relay: its admin must add one " +
		"or run one with 'local-ai p2p relay', then send a new invite")
	// ErrWrongServer means another peer than the one of the link answered.
	ErrWrongServer = errors.New("the answering server is not the one of the invite: do not trust this pairing")
)

// Request is what the device tells the server about itself.
type Request struct {
	// Name is the device name the server shows in its device list.
	Name string
	// Platform is the operating system of the device.
	Platform string
	// AllowOpenRelay lets the device connect through an open relay that the
	// link names. Without it a link with an open relay is refused.
	AllowOpenRelay bool
}

// ClientKey is the API key a server issues with an invite of kind remote.
type ClientKey struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

// Server describes the server that answered.
type Server struct {
	PeerID  string `json:"peer_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Result is the answer of a redeemed invite.
type Result struct {
	DeviceID string `json:"device_id"`
	Kind     string `json:"kind"`
	// Phrase is the confirmation phrase. The server shows the same words.
	Phrase string `json:"phrase"`
	Server Server `json:"server"`
	// ClientKey is set for an invite of kind remote.
	ClientKey *ClientKey `json:"client_key,omitempty"`
	// DeviceCredential lets the device of a sign-in invite sign in as its
	// owner. It is a secret: never log it. A server that does not issue
	// one, or predates sign-in invites, leaves it empty.
	DeviceCredential string `json:"device_credential,omitempty"`
	// SignIn is true when the server issued a device credential.
	SignIn bool `json:"sign_in,omitempty"`
	// OwnerName names the account the device signs in as, when known.
	OwnerName string `json:"owner_name,omitempty"`
}

// punchWait is how long Redeem waits for a hole punch through a hint. Tests
// shorten it.
var punchWait = p2pnet.DefaultPunchWait

// DialOptions returns the dial options that reach the server of the link:
// its direct addresses, its relays and its hint relays. A dial to the
// server after the pairing uses the same options.
func DialOptions(l *p2ppair.Link) []p2pnet.DialOption {
	return []p2pnet.DialOption{p2pnet.WithRelays(l.Relays...), p2pnet.WithAddrs(l.Addrs...), p2pnet.WithHints(l.Hints...)}
}

// Redeem redeems the link on host h. The identity of h is the identity the
// server admits from now on, so h must use the identity the device keeps.
//
// When the server can be reached only through a hint relay, Redeem sends the
// code only after a hole punch opened a direct connection, so ctx must
// allow about 2*p2pnet.DefaultPunchWait. When the punch fails, it returns
// ErrNeedsRelay and the invite stays pending.
func Redeem(ctx context.Context, h *p2pnet.Host, l *p2ppair.Link, req Request) (*Result, error) {
	if l.Expired(time.Now()) {
		return nil, ErrExpired
	}
	if len(l.Open) > 0 && !req.AllowOpenRelay {
		return nil, ErrOpenRelay
	}
	if l.Pass != "" {
		usePass(ctx, h, l)
	}
	body, err := json.Marshal(map[string]any{"v": 1, "code": l.Code, "name": req.Name, "platform": req.Platform})
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+l.Peer.String()+RedeemPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	client := h.HTTPClient(l.Peer, append(DialOptions(l), p2pnet.WithPunchWait(punchWait))...)
	defer client.CloseIdleConnections()
	resp, err := client.Do(hr)
	if err != nil {
		if errors.Is(err, p2pnet.ErrNoDirectPath) {
			return nil, fmt.Errorf("%w: %w: %w", ErrUnreachable, ErrNeedsRelay, err)
		}
		if len(l.Addrs) == 0 && len(l.Relays) == 0 && len(l.Hints) == 0 {
			return nil, fmt.Errorf("%w: %w: %w", ErrUnreachable, ErrNoRoute, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusGone:
		return nil, ErrGone
	case http.StatusTooManyRequests:
		return nil, ErrTooManyTries
	default:
		return nil, apiError(resp.StatusCode, raw)
	}
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unexpected answer from the server: %w", err)
	}
	if out.Server.PeerID != "" && out.Server.PeerID != l.Peer.String() {
		return nil, fmt.Errorf("%w (%s answered instead of %s)", ErrWrongServer, out.Server.PeerID, l.Peer)
	}
	out.Server.PeerID = l.Peer.String()
	return &out, nil
}

// usePass presents the relay pass of the link. The pass lets this still
// unknown device through the relay. The link does not say which relay holds
// it, so every gated relay is tried until one accepts. A failure is not an
// error: a direct path may still work.
func usePass(ctx context.Context, h *p2pnet.Host, l *p2ppair.Link) {
	for i, r := range l.Relays {
		if slices.Contains(l.Open, i) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, passConnectTimeout)
		err := h.Libp2p().Connect(cctx, r)
		cancel()
		if err != nil {
			continue
		}
		if _, err := relayproto.UsePass(ctx, h.Libp2p(), r.ID, l.Pass); err == nil {
			return
		}
	}
}

// apiError turns an error answer of the server into an error. The server
// answers in the schema.ErrorResponse shape.
func apiError(code int, body []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("the server refused the invite (status %d): %s", code, e.Error.Message)
	}
	return fmt.Errorf("the server refused the invite (status %d)", code)
}
