// Package safehttp fetches public web pages for a model. A model chooses the
// URL, so every request is treated as hostile: the URL, every redirect target
// and every address a name resolves to are checked, and the connection goes
// to the address that was checked, so a second DNS answer cannot move it
// inside the network (DNS rebinding).
package safehttp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Errors of a fetch. The tools turn them into text for the model, so each
// says what was wrong without echoing internal detail.
var (
	ErrURL              = errors.New("the URL is not valid")
	ErrScheme           = errors.New("only http and https URLs can be fetched")
	ErrUserinfo         = errors.New("URLs with a user name or password are not fetched")
	ErrPort             = errors.New("this port is not allowed")
	ErrBlockedAddress   = errors.New("the address is inside a private network or reserved")
	ErrTooManyRedirects = errors.New("too many redirects")
	ErrContentType      = errors.New("unsupported type")
	ErrTimeout          = errors.New("the server did not answer in time")
	ErrStatus           = errors.New("the server answered with an error")
)

// Policy is what a client may reach and how much it reads.
type Policy struct {
	// AllowPrivate lets requests reach private, loopback and link-local
	// addresses. The cloud metadata addresses and the unspecified address
	// stay blocked.
	AllowPrivate bool
	// Ports are the ports a URL may name. Empty means 80 and 443.
	Ports        []int
	MaxURLBytes  int
	MaxRedirects int
	MaxBodyBytes int64
	Timeout      time.Duration
	UserAgent    string
}

// Defaults of a Policy field left at its zero value.
const (
	DefaultMaxURLBytes  = 2048
	DefaultMaxRedirects = 5
	DefaultMaxBodyBytes = 2 << 20
	DefaultTimeout      = 15 * time.Second
	DefaultUserAgent    = "LocalAI (web_fetch; +https://localai.io)"
)

func (p Policy) withDefaults() Policy {
	if len(p.Ports) == 0 {
		p.Ports = []int{80, 443}
	}
	if p.MaxURLBytes <= 0 {
		p.MaxURLBytes = DefaultMaxURLBytes
	}
	if p.MaxRedirects <= 0 {
		p.MaxRedirects = DefaultMaxRedirects
	}
	if p.MaxBodyBytes <= 0 {
		p.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if p.Timeout <= 0 {
		p.Timeout = DefaultTimeout
	}
	if p.UserAgent == "" {
		p.UserAgent = DefaultUserAgent
	}
	return p
}

// Resolver resolves a host name. *net.Resolver implements it; tests pass a stub.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Client fetches URLs under a Policy. It keeps no cookies and sends no
// credentials. It is safe for concurrent use.
type Client struct {
	p        Policy
	resolver Resolver
	hc       *http.Client
	// allowed decides an address. Tests replace it to treat the loopback
	// address of an httptest server as public.
	allowed func(netip.Addr) bool
}

// New returns a client. A nil resolver uses net.DefaultResolver.
func New(p Policy, r Resolver) *Client {
	p = p.withDefaults()
	if r == nil {
		r = net.DefaultResolver
	}
	c := &Client{p: p, resolver: r}
	c.allowed = func(a netip.Addr) bool { return Allowed(a, p.AllowPrivate) }
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: c.control}
	tr := &http.Transport{
		// No proxy: a proxy would resolve the name again, after the check.
		Proxy:                  nil,
		DialContext:            c.dial(dialer),
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           16,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  p.Timeout,
		MaxResponseHeaderBytes: 64 << 10,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
	c.hc = &http.Client{
		Transport: tr,
		Jar:       nil,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > p.MaxRedirects {
				return ErrTooManyRedirects
			}
			if _, err := c.CheckURL(req.URL.String()); err != nil {
				return err
			}
			// Only the headers this client sets travel with a redirect.
			for k := range req.Header {
				if k != "User-Agent" && k != "Accept" {
					req.Header.Del(k)
				}
			}
			return nil
		},
	}
	return c
}

// Policy returns the policy in force, with its defaults.
func (c *Client) Policy() Policy { return c.p }

// Response is a fetched body.
type Response struct {
	FinalURL *url.URL
	// ContentType is the media type only, in lower case.
	ContentType string
	// Charset is the charset parameter of the Content-Type header, if any.
	Charset string
	Body    []byte
	// Truncated says that the body was longer than MaxBodyBytes.
	Truncated bool
}

// Get fetches raw. accept lists the media types the caller can read; an entry
// may be "type/*" or "application/*+json". Another type is ErrContentType.
func (c *Client) Get(ctx context.Context, raw string, accept []string) (*Response, error) {
	u, err := c.CheckURL(raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.p.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrURL
	}
	req.Header.Set("User-Agent", c.p.UserAgent)
	if len(accept) > 0 {
		req.Header.Set("Accept", strings.Join(accept, ", ")+", */*;q=0.1")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, classify(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%w: %s", ErrStatus, resp.Status)
	}
	ct := resp.Header.Get("Content-Type")
	media, charset := "application/octet-stream", ""
	if ct != "" {
		if m, params, err := mime.ParseMediaType(ct); err == nil {
			media, charset = strings.ToLower(m), params["charset"]
		}
	}
	if len(accept) > 0 && !Accepts(accept, media) {
		return nil, fmt.Errorf("%w: %s", ErrContentType, media)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.p.MaxBodyBytes+1))
	if err != nil {
		return nil, classify(ctx, err)
	}
	out := &Response{FinalURL: resp.Request.URL, ContentType: media, Charset: charset, Body: body}
	if int64(len(body)) > c.p.MaxBodyBytes {
		out.Body, out.Truncated = body[:c.p.MaxBodyBytes], true
	}
	return out, nil
}

// Accepts says whether media matches one entry of accept.
func Accepts(accept []string, media string) bool {
	for _, a := range accept {
		switch {
		case a == media:
			return true
		case strings.HasSuffix(a, "/*") && strings.HasPrefix(media, strings.TrimSuffix(a, "*")):
			return true
		case strings.HasPrefix(a, "application/*+"):
			if strings.HasPrefix(media, "application/") && strings.HasSuffix(media, strings.TrimPrefix(a, "application/*")) {
				return true
			}
		}
	}
	return false
}

// classify maps a transport error to the errors of this package.
func classify(ctx context.Context, err error) error {
	for _, e := range []error{ErrURL, ErrScheme, ErrUserinfo, ErrPort, ErrBlockedAddress, ErrTooManyRedirects} {
		if errors.Is(err, e) {
			return e
		}
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || (errors.As(err, &ne) && ne.Timeout()) {
		return ErrTimeout
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return fmt.Errorf("%w: the host name does not resolve", ErrURL)
	}
	return fmt.Errorf("the page could not be fetched: %w", unwrapURLError(err))
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// CheckURL parses raw and applies the URL rules: length, scheme, no
// userinfo, an allowed port and a host that is a name or a canonical IP
// literal. A literal address is checked here too; a name is checked when it
// is dialed. The fragment is dropped.
func (c *Client) CheckURL(raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > c.p.MaxURLBytes {
		return nil, fmt.Errorf("%w: it must be 1 to %d bytes long", ErrURL, c.p.MaxURLBytes)
	}
	for _, r := range raw {
		if r < 0x21 || r == 0x7f {
			return nil, fmt.Errorf("%w: it contains a space or a control character", ErrURL)
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrURL
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, ErrScheme
	}
	if u.User != nil {
		return nil, ErrUserinfo
	}
	if u.Opaque != "" {
		return nil, ErrURL
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return nil, fmt.Errorf("%w: it has no host", ErrURL)
	}
	port := 80
	if strings.EqualFold(u.Scheme, "https") {
		port = 443
	}
	if ps := u.Port(); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 65535 {
			return nil, ErrURL
		}
		port = n
	}
	if !c.portAllowed(port) {
		return nil, fmt.Errorf("%w: %d", ErrPort, port)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if !c.p.AllowPrivate {
			return nil, ErrBlockedAddress
		}
	}
	if strings.Contains(host, "%") {
		return nil, fmt.Errorf("%w: an address zone is not allowed", ErrURL)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !c.allowed(addr) {
			return nil, ErrBlockedAddress
		}
	} else if strings.Contains(host, ":") || looksNumeric(host) {
		// 2130706433, 0x7f.1, 017700000001 and 127.1 are loopback to some
		// resolvers and names to others. A URL that means an address must
		// write it as a canonical dotted quad.
		return nil, fmt.Errorf("%w: write an IP address as four decimal numbers", ErrURL)
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

func (c *Client) portAllowed(port int) bool {
	for _, p := range c.p.Ports {
		if p == port {
			return true
		}
	}
	return false
}

// looksNumeric says whether the last label of host is a number in any base,
// which makes a URL parser read the whole host as an IPv4 address.
func looksNumeric(host string) bool {
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if last == "" {
		return false
	}
	if strings.HasPrefix(last, "0x") {
		last = last[2:]
		if last == "" {
			return true
		}
		for _, r := range last {
			if !strings.ContainsRune("0123456789abcdef", r) {
				return false
			}
		}
		return true
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// metadata are the cloud instance metadata addresses. They stay blocked when
// private addresses are allowed: they hand out credentials.
var metadata = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("169.254.170.2"),
	netip.MustParseAddr("fd00:ec2::254"),
}

var (
	sixToFour = netip.MustParsePrefix("2002::/16")
	teredo    = netip.MustParsePrefix("2001::/32")
)

// Allowed says whether a fetch may connect to a. Without allowPrivate only
// public addresses pass. IPv4 addresses embedded in IPv6 (mapped, compatible,
// NAT64, 6to4) are checked as IPv4.
func Allowed(a netip.Addr, allowPrivate bool) bool {
	if !a.IsValid() || a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	if a.Is6() {
		b := a.As16()
		// IPv4-compatible (::a.b.c.d, deprecated) and the 6to4 prefix carry
		// an IPv4 address that some stacks route to.
		if isZero(b[:12]) && !a.IsLoopback() && !a.IsUnspecified() {
			return Allowed(netip.AddrFrom4([4]byte(b[12:16])), allowPrivate)
		}
		if sixToFour.Contains(a) {
			return Allowed(netip.AddrFrom4([4]byte(b[2:6])), allowPrivate)
		}
		if teredo.Contains(a) && !allowPrivate {
			return false
		}
	}
	for _, m := range metadata {
		if a == m {
			return false
		}
	}
	if a.IsUnspecified() {
		return false
	}
	if allowPrivate {
		return true
	}
	return httpclient.IsPublicIP(net.IP(a.AsSlice()))
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// dial resolves the host once, refuses the connection when any address is
// not allowed, and connects to the checked addresses only.
func (c *Client) dial(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, ErrURL
		}
		var addrs []netip.Addr
		if lit, err := netip.ParseAddr(host); err == nil {
			addrs = []netip.Addr{lit}
		} else {
			addrs, err = c.resolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("%w: the host name does not resolve", ErrURL)
		}
		for _, a := range addrs {
			if !c.allowed(a) {
				return nil, ErrBlockedAddress
			}
		}
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
}

// control checks the address of the socket right before connect, so nothing
// between the check and the connection can change it.
func (c *Client) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !c.allowed(ap.Addr()) {
		return ErrBlockedAddress
	}
	return nil
}
