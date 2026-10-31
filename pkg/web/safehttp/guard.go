package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/idna"
)

// Errors of an address that the guard refuses for a reason more precise than
// "private". Both wrap ErrBlockedAddress, so a caller that only asks whether
// an address was refused does not need to know them.
var (
	ErrSelfAddress     = fmt.Errorf("%w: it belongs to this server", ErrBlockedAddress)
	ErrMetadataAddress = fmt.Errorf("%w: it is a cloud metadata address", ErrBlockedAddress)
)

// GuardPolicy is what a Guard lets through.
type GuardPolicy struct {
	// AllowPrivate lets connections reach private, loopback and link-local
	// addresses. The cloud metadata addresses, the unspecified address and
	// the addresses of DenyAddrs stay blocked.
	AllowPrivate bool
	// Ports are the ports a host may name. Empty means 80 and 443.
	Ports []int
	// DenyAddrs returns addresses that are refused even when public: the
	// addresses of this server, so that a request cannot reach the server's
	// own API through its public address. Nil refuses none.
	DenyAddrs func() []netip.Prefix
	// TreatAsPublic are addresses the guard lets through as if they were
	// public. It exists for tests that run a server on loopback; production
	// code leaves it empty.
	TreatAsPublic []netip.Addr
}

// Guard decides hosts and addresses and dials only what it checked. It is the
// one guard of the server's outbound requests on behalf of a model: web_fetch
// uses it through Client, and the Sandbox network proxy uses it directly. It
// is safe for concurrent use.
type Guard struct {
	p        GuardPolicy
	resolver Resolver
	// treat holds the addresses of TreatAsPublic, unmapped.
	treat map[netip.Addr]bool
}

// NewGuard returns a guard. A nil resolver uses net.DefaultResolver.
func NewGuard(p GuardPolicy, r Resolver) *Guard {
	if len(p.Ports) == 0 {
		p.Ports = []int{80, 443}
	}
	if r == nil {
		r = net.DefaultResolver
	}
	g := &Guard{p: p, resolver: r, treat: map[netip.Addr]bool{}}
	for _, a := range p.TreatAsPublic {
		g.treat[a.Unmap()] = true
	}
	return g
}

// WithAllowPrivate returns a copy of g that lets private addresses through,
// or not, for one decision. The metadata and self rules stay.
func (g *Guard) WithAllowPrivate(allow bool) *Guard {
	cp := *g
	cp.p.AllowPrivate = allow
	return &cp
}

// Policy returns the policy in force.
func (g *Guard) Policy() GuardPolicy { return g.p }

// check is the address rule: Allowed, then the metadata and self lists.
func (g *Guard) check(a netip.Addr) error {
	if g.treat[a.Unmap()] {
		return nil
	}
	if isMetadata(a) {
		return ErrMetadataAddress
	}
	if !Allowed(a, g.p.AllowPrivate) {
		return ErrBlockedAddress
	}
	if g.p.DenyAddrs != nil {
		u := a.Unmap()
		for _, p := range g.p.DenyAddrs() {
			if p.Contains(u) {
				return ErrSelfAddress
			}
		}
	}
	return nil
}

// AllowedAddr says whether the guard lets a connection reach a, and why not.
func (g *Guard) AllowedAddr(a netip.Addr) error { return g.check(a) }

func (g *Guard) portAllowed(port int) bool {
	return slices.Contains(g.p.Ports, port)
}

// CheckHost applies the host rules of CheckURL to a bare host and port: a
// name or a canonical IP literal, no zone, no numeric tricks, no localhost, an
// allowed port and, for a literal, an allowed address. It does not resolve.
// The canonical form is in lower case, without a trailing dot and without
// brackets. An international name is returned in its ASCII (punycode) form,
// so a look-alike such as "pаypal.com" with a Cyrillic "а" reads
// "xn--pypal-4ve.com" wherever the host is shown, a consent card included; a
// name that has no valid ASCII form is refused.
func (g *Guard) CheckHost(host string, port int) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if !isASCII(host) {
		a, err := idna.Lookup.ToASCII(host)
		if err != nil || !isASCII(a) {
			return "", fmt.Errorf("%w: the host is not a valid international name", ErrURL)
		}
		host = strings.ToLower(a)
	}
	if host == "" || len(host) > 253 {
		return "", fmt.Errorf("%w: it has no host, or the host is too long", ErrURL)
	}
	for _, r := range host {
		if r < 0x21 || r == 0x7f || r == '/' || r == '@' || r == '\\' || r == '?' || r == '#' {
			return "", fmt.Errorf("%w: the host has a character a host cannot have", ErrURL)
		}
	}
	if port < 1 || port > 65535 {
		return "", ErrURL
	}
	if !g.portAllowed(port) {
		return "", fmt.Errorf("%w: %d", ErrPort, port)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if !g.p.AllowPrivate {
			return "", ErrBlockedAddress
		}
	}
	if strings.Contains(host, "%") {
		return "", fmt.Errorf("%w: an address zone is not allowed", ErrURL)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if err := g.check(addr); err != nil {
			return "", err
		}
		return addr.String(), nil
	}
	if strings.Contains(host, ":") || looksNumeric(host) {
		// 2130706433, 0x7f.1, 017700000001 and 127.1 are loopback to some
		// resolvers and names to others. A host that means an address must
		// be written as a canonical dotted quad.
		return "", fmt.Errorf("%w: write an IP address as four decimal numbers", ErrURL)
	}
	return host, nil
}

// Resolve looks a host up once and checks every answer. It refuses the host
// when any answer is not allowed, so a name with one public and one private
// address is refused as a whole.
func (g *Guard) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	var addrs []netip.Addr
	if lit, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{lit}
	} else {
		addrs, err = g.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: the host name does not resolve", ErrURL)
	}
	for _, a := range addrs {
		if err := g.check(a); err != nil {
			return nil, err
		}
	}
	return addrs, nil
}

// dialTimeout bounds one connection attempt.
const dialTimeout = 10 * time.Second

// DialContext resolves the host once, refuses the connection when any answer
// is not allowed, and dials the checked addresses with a Control hook that
// checks the socket address again right before connect.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrURL
	}
	addrs, err := g.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: dialTimeout, Control: g.control}
	var last error
	for _, a := range addrs {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

// control checks the address of the socket right before connect, so nothing
// between the check and the connection can change it.
func (g *Guard) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return ErrBlockedAddress
	}
	return g.check(ap.Addr())
}

// isMetadata says whether a is a cloud metadata address, IPv4 inside IPv6
// included.
func isMetadata(a netip.Addr) bool {
	a = a.Unmap()
	return slices.Contains(metadata, a)
}

// selfRefresh is how often SelfAddrs reads the interfaces again.
const selfRefresh = 60 * time.Second

// SelfAddrs returns a DenyAddrs function that lists the addresses of this
// server: the address of each network interface (as one address, not its
// subnet), the whole loopback range, and the addresses that the hosts given by
// extra resolve to (the host of the external base URL). The list is read again
// every 60 seconds. extra may be nil.
func SelfAddrs(extra func() []string) func() []netip.Prefix {
	var (
		mu   sync.Mutex
		at   time.Time
		list []netip.Prefix
	)
	return func() []netip.Prefix {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < selfRefresh {
			return list
		}
		list = readSelfAddrs(extra)
		at = time.Now()
		return list
	}
}

func readSelfAddrs(extra func() []string) []netip.Prefix {
	out := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	if ifs, err := net.InterfaceAddrs(); err == nil {
		for _, ia := range ifs {
			var ip net.IP
			switch v := ia.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if a, ok := netip.AddrFromSlice(ip); ok && a.Zone() == "" {
				a = a.Unmap()
				out = append(out, netip.PrefixFrom(a, a.BitLen()))
			}
		}
	}
	if extra != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for _, h := range extra() {
			h = strings.TrimSuffix(strings.TrimSpace(h), ".")
			if h == "" {
				continue
			}
			addrs := []netip.Addr{}
			if a, err := netip.ParseAddr(h); err == nil {
				addrs = append(addrs, a)
			} else if got, err := net.DefaultResolver.LookupNetIP(ctx, "ip", h); err == nil {
				addrs = got
			}
			for _, a := range addrs {
				a = a.Unmap()
				out = append(out, netip.PrefixFrom(a, a.BitLen()))
			}
		}
	}
	return out
}

// ParsePort reads a port number of a host:port pair.
func ParsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("the port is not a number from 1 to 65535")
	}
	return n, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
