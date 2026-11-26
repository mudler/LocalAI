package p2ppair

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// Limits of the link format. They keep a link small enough for a QR code and
// bound the work of the parser on input it did not make.
const (
	linkScheme   = "localai"
	linkHost     = "pair"
	linkVersion  = 1
	maxLinkLen   = 2048
	maxLabelLen  = 64
	maxLinkAddrs = 4
	// maxPublicAddrs is how many of them a mint takes from the public ones;
	// it is what a link carried before it had local addresses.
	maxPublicAddrs = 2
	maxLinkRelays  = 3
	// maxLinkHints is how many public relays a link names for hole punch
	// coordination. AutoRelay keeps two reservations when it has no static
	// relay, so more would rarely exist.
	maxLinkHints = 2
	maxPassLen   = 256
	maxAddrLen   = 256
)

// ErrBadLink is wrapped by every ParseLink failure.
var ErrBadLink = errors.New("p2ppair: bad invite link")

func badLink(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadLink, fmt.Sprintf(format, a...))
}

// Link is a decoded invite link:
//
//	localai://pair?v=1&k=<kind>&p=<server peer ID>&c=<code>&e=<unix expiry>&n=<label>
//	  [&a=<direct multiaddr>]x0-4 [&r=<relay multiaddr with /p2p/>]x0-3
//	  [&o=<indexes of open relays>] [&x=<relay pass>]
//	  [&h=<relay hint multiaddr with /p2p/>]x0-2
//
// Values are URL query escaped. A parser ignores keys it does not know and
// refuses a version other than 1.
type Link struct {
	V       int
	Kind    Kind
	Peer    peer.ID
	Code    string
	Expires time.Time
	Label   string
	// Addrs are direct addresses of the server (at most 4): public ones
	// first, then addresses of the server's local network. A redeeming
	// device dials them all, so a link with local addresses redeems on the
	// same network with no relay and no multicast discovery.
	Addrs []ma.Multiaddr
	// Relays are relays that can reach the server (at most 3). Each has one
	// address; String writes only the first address of an AddrInfo.
	Relays []peer.AddrInfo
	// Open lists the indexes in Relays of relays that are open (anyone may
	// use them). The client warns before it connects through one.
	Open []int
	// Pass is the one-time pass that admits the redeeming peer at the relay.
	Pass string
	// Hints are public relays on which the server holds a reservation (at
	// most 2), written like Relays. A device uses one only to reach the
	// server and coordinate a hole punch: it never carries data, never gets
	// the pass and is never open. A separate key keeps older clients, which
	// try the pass on every relay of r, from treating a hint as a carrier.
	Hints []peer.AddrInfo
}

// Expired reports whether the link has expired at now.
func (l *Link) Expired(now time.Time) bool { return !now.Before(l.Expires) }

// AddrStrings returns the direct addresses of the server as text, or nil
// when the link has none.
func (l *Link) AddrStrings() []string {
	var out []string
	for _, a := range l.Addrs {
		out = append(out, a.String())
	}
	return out
}

// RelayAddrStrings returns one multiaddr ending in /p2p/<relay ID> for each
// address of each relay of the link, or nil when the link has no relays.
func (l *Link) RelayAddrStrings() []string { return p2pAddrStrings(l.Relays) }

// HintAddrStrings returns one multiaddr ending in /p2p/<relay ID> for each
// address of each relay hint of the link, or nil when the link has none.
func (l *Link) HintAddrStrings() []string { return p2pAddrStrings(l.Hints) }

func p2pAddrStrings(infos []peer.AddrInfo) []string {
	var out []string
	for _, r := range infos {
		for _, a := range r.Addrs {
			out = append(out, a.String()+"/p2p/"+r.ID.String())
		}
	}
	return out
}

// String returns the link text. Fields are written in a fixed order, so the
// output is stable.
func (l *Link) String() string {
	var b strings.Builder
	b.WriteString(linkScheme + "://" + linkHost + "?v=" + strconv.Itoa(linkVersion))
	add := func(k, v string) { b.WriteString("&" + k + "=" + url.QueryEscape(v)) }
	add("k", string(l.Kind))
	add("p", l.Peer.String())
	add("c", l.Code)
	add("e", strconv.FormatInt(l.Expires.Unix(), 10))
	add("n", l.Label)
	for _, a := range l.Addrs {
		add("a", a.String())
	}
	for _, r := range l.Relays {
		if s, ok := relayString(r); ok {
			add("r", s)
		}
	}
	if len(l.Open) > 0 {
		idx := make([]string, len(l.Open))
		for i, v := range l.Open {
			idx[i] = strconv.Itoa(v)
		}
		add("o", strings.Join(idx, ","))
	}
	if l.Pass != "" {
		add("x", l.Pass)
	}
	for _, h := range l.Hints {
		if s, ok := relayString(h); ok {
			add("h", s)
		}
	}
	return b.String()
}

// relayString writes the first address of r with its /p2p/ part.
func relayString(r peer.AddrInfo) (string, bool) {
	if len(r.Addrs) == 0 {
		return "", false
	}
	pa, err := ma.NewMultiaddr("/p2p/" + r.ID.String())
	if err != nil {
		return "", false
	}
	return r.Addrs[0].Encapsulate(pa).String(), true
}

// ParseLink decodes and validates an invite link. Every error wraps
// ErrBadLink. It does not check the expiry (see Link.Expired).
func ParseLink(s string) (*Link, error) {
	if len(s) > maxLinkLen {
		return nil, badLink("longer than %d bytes", maxLinkLen)
	}
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return nil, badLink("not a URL")
	}
	if u.Scheme != linkScheme || u.Host != linkHost || (u.Path != "" && u.Path != "/") || u.Opaque != "" || u.User != nil || u.Fragment != "" {
		return nil, badLink("not a pairing link")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, badLink("bad query")
	}
	single := func(k string) (string, error) {
		v := q[k]
		if len(v) != 1 {
			return "", badLink("key %q must appear once", k)
		}
		return v[0], nil
	}

	l := &Link{}
	v, err := single("v")
	if err != nil {
		return nil, err
	}
	if v != strconv.Itoa(linkVersion) {
		return nil, badLink("unsupported version")
	}
	l.V = linkVersion

	k, err := single("k")
	if err != nil {
		return nil, err
	}
	l.Kind = Kind(k)
	if !l.Kind.Valid() {
		return nil, badLink("unknown kind")
	}

	p, err := single("p")
	if err != nil {
		return nil, err
	}
	if l.Peer, err = peer.Decode(p); err != nil {
		return nil, badLink("bad peer ID")
	}

	c, err := single("c")
	if err != nil {
		return nil, err
	}
	if c != NormalizeCode(c) || !validCodeFormat(c) {
		return nil, badLink("bad code")
	}
	l.Code = c

	e, err := single("e")
	if err != nil {
		return nil, err
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || exp <= 0 || exp > 1<<40 {
		return nil, badLink("bad expiry")
	}
	l.Expires = time.Unix(exp, 0).UTC()

	n, err := single("n")
	if err != nil {
		return nil, err
	}
	if len(n) > maxLabelLen || !utf8.ValidString(n) || strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, badLink("bad label")
	}
	l.Label = n

	if len(q["a"]) > maxLinkAddrs {
		return nil, badLink("too many addresses")
	}
	for _, a := range q["a"] {
		if len(a) > maxAddrLen {
			return nil, badLink("address too long")
		}
		m, err := ma.NewMultiaddr(a)
		if err != nil {
			return nil, badLink("bad address")
		}
		l.Addrs = append(l.Addrs, m)
	}

	if len(q["r"]) > maxLinkRelays {
		return nil, badLink("too many relays")
	}
	for _, r := range q["r"] {
		ai, err := parseRelayAddr(r, "relay")
		if err != nil {
			return nil, err
		}
		l.Relays = append(l.Relays, ai)
	}

	if len(q["o"]) > 1 {
		return nil, badLink("key \"o\" must appear once")
	}
	if len(q["o"]) == 1 && q["o"][0] != "" {
		seen := map[int]bool{}
		for f := range strings.SplitSeq(q["o"][0], ",") {
			i, err := strconv.Atoi(f)
			if err != nil || i < 0 || i >= len(l.Relays) || seen[i] || strconv.Itoa(i) != f {
				return nil, badLink("bad open relay index")
			}
			seen[i] = true
			l.Open = append(l.Open, i)
		}
	}

	if len(q["x"]) > 1 {
		return nil, badLink("key \"x\" must appear once")
	}
	if len(q["x"]) == 1 {
		x := q["x"][0]
		if len(x) > maxPassLen || !utf8.ValidString(x) || strings.ContainsFunc(x, func(r rune) bool { return r <= 0x20 || r == 0x7f }) {
			return nil, badLink("bad relay pass")
		}
		l.Pass = x
	}

	if len(q["h"]) > maxLinkHints {
		return nil, badLink("too many relay hints")
	}
	for _, h := range q["h"] {
		ai, err := parseRelayAddr(h, "relay hint")
		if err != nil {
			return nil, err
		}
		// A hint is a route to the server through another peer: the server
		// itself or a carrier relay named in r is a mistake in the link.
		if ai.ID == l.Peer {
			return nil, badLink("a relay hint names the server")
		}
		for _, r := range l.Relays {
			if ai.ID == r.ID {
				return nil, badLink("a relay hint names a relay of the link")
			}
		}
		l.Hints = append(l.Hints, ai)
	}
	return l, nil
}

// parseRelayAddr reads one relay address of a link: a transport address that
// ends in /p2p/<relay ID>. what names the key in errors.
func parseRelayAddr(s, what string) (peer.AddrInfo, error) {
	if len(s) > maxAddrLen {
		return peer.AddrInfo{}, badLink("%s address too long", what)
	}
	m, err := ma.NewMultiaddr(s)
	if err != nil {
		return peer.AddrInfo{}, badLink("bad %s address", what)
	}
	ai, err := peer.AddrInfoFromP2pAddr(m)
	if err != nil || len(ai.Addrs) == 0 {
		return peer.AddrInfo{}, badLink("a %s address needs a transport and /p2p/", what)
	}
	return *ai, nil
}
