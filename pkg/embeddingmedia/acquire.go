// SPDX-License-Identifier: MIT
package embeddingmedia

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Source kinds accepted by Acquire.
const (
	KindImage = "image"
	KindAudio = "audio"
	KindVideo = "video"
)

// Source is one parsed media source. Exactly one of Data and URL is set. Data
// is raw base64; URL is a base64 data URI or an HTTP(S) URL. Format is an
// optional hint that must agree with the detected signature.
type Source struct {
	Data   string
	URL    string
	Format string
	Kind   string
}

func invalidInput() error { return &Error{Status: 400, Code: "invalid_embedding_input"} }
func fetchFailed() error  { return &Error{Status: 502, Code: "embedding_fetch_failed"} }
func fetchTimeout() error { return &Error{Status: 504, Code: "embedding_fetch_timeout"} }
func badFormat() error    { return &Error{Status: 415, Code: "embedding_unsupported_format"} }
func overLimit() error    { return &Error{Status: 413, Code: "embedding_resource_limit"} }

// Acquirer fetches and validates compressed media bytes. It holds no
// per-request state and is safe for concurrent use; every call takes its own
// request-scoped Budget. It never decodes media: signatures are sniffed only.
type Acquirer struct {
	client    *http.Client
	transport *http.Transport
	resolve   func(ctx context.Context, host string) ([]netip.Addr, error)
	dial      func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error)
	timeout   time.Duration
}

// acquirerConfig carries test-only hooks. Production uses NewAcquirer, whose
// zero configuration selects the system resolver, a plain dialer, system
// trust roots and the protocol fetch deadline.
type acquirerConfig struct {
	resolve      func(ctx context.Context, host string) ([]netip.Addr, error)
	lookup       func(ctx context.Context, network, host string) ([]netip.Addr, error)
	dial         func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error)
	rootCAs      *x509.CertPool
	fetchTimeout time.Duration
}

// NewAcquirer returns the production acquirer with fixed limits.
func NewAcquirer() *Acquirer { return newAcquirer(acquirerConfig{}) }

func newAcquirer(cfg acquirerConfig) *Acquirer {
	a := &Acquirer{resolve: cfg.resolve, dial: cfg.dial, timeout: cfg.fetchTimeout}
	if a.resolve == nil {
		lookup := cfg.lookup
		if lookup == nil {
			lookup = net.DefaultResolver.LookupNetIP
		}
		a.resolve = resolveByFamily(lookup)
	}
	if a.dial == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		a.dial = func(ctx context.Context, network string, addr netip.AddrPort) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr.String())
		}
	}
	if a.timeout <= 0 {
		a.timeout = time.Duration(MaxFetchMilliseconds) * time.Millisecond
	}
	transport := &http.Transport{
		// No environment proxy: a proxy would see and resolve the target itself.
		Proxy:                  nil,
		DialContext:            a.dialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: cfg.rootCAs},
		TLSHandshakeTimeout:    10 * time.Second,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	// httpclient refuses redirects, so no hop is ever followed or re-dialed.
	a.transport = transport
	a.client = httpclient.New(httpclient.WithTransport(transport))
	return a
}

// dialContext runs for every new connection: it resolves, requires all
// answers to be approved and connects to a validated address, not the name.
// The TLS layer above still verifies the original hostname and sends it as SNI.
func (a *Acquirer) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fetchFailed()
	}
	port, err := net.LookupPort(network, portText)
	if err != nil {
		return nil, fetchFailed()
	}
	addrs, err := a.approve(ctx, host)
	if err != nil {
		return nil, err
	}
	var last error = fetchFailed()
	for _, ip := range addrs {
		c, err := a.dial(ctx, network, netip.AddrPortFrom(ip, uint16(port)))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

func (a *Acquirer) approve(ctx context.Context, host string) ([]netip.Addr, error) {
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		if deniedName(host) {
			return nil, fetchFailed()
		}
		addrs, err = a.resolve(ctx, host)
		if err != nil || len(addrs) == 0 {
			return nil, fetchFailed()
		}
	}
	for _, ip := range addrs {
		if !isApprovedAddress(ip) {
			return nil, fetchFailed()
		}
	}
	return addrs, nil
}

// resolveByFamily asks for IPv4 and IPv6 answers separately. The standard
// resolver returns IPv4 answers in 4-in-6 form even for "ip4", so IPv4 answers
// are unmapped by the family they were requested as. An AAAA answer keeps its
// form and is then denied by the 4-in-6 check in isApprovedAddress.
func resolveByFamily(lookup func(ctx context.Context, network, host string) ([]netip.Addr, error)) func(ctx context.Context, host string) ([]netip.Addr, error) {
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		var firstErr error
		for _, network := range []string{"ip4", "ip6"} {
			addrs, err := lookup(ctx, network, host)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, a := range addrs {
				if network == "ip4" {
					a = a.Unmap()
				}
				out = append(out, a)
			}
		}
		if len(out) == 0 {
			if firstErr == nil {
				firstErr = fetchFailed()
			}
			return nil, firstErr
		}
		return out, nil
	}
}

func deniedName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "" || h == "localhost" || h == "instance-data" ||
		strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") ||
		strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".localdomain")
}

var deniedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		// IPv4 special-purpose blocks not covered by the standard predicates.
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		// IPv6 transition, documentation, discard and deprecated blocks.
		"::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:2::/48",
		"2001:db8::/32", "2002::/16", "3fff::/20", "5f00::/16", "fec0::/10",
		// SIIT translation, the whole 2001::/23 block, and the 2620:4f:8000::/48
		// and 192.x special-purpose blocks that the first table missed.
		"::ffff:0:0:0/96", "2001::/23", "2620:4f:8000::/48",
		"192.31.196.0/24", "192.52.193.0/24", "192.175.48.0/24",
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// isApprovedAddress allows only global unicast addresses outside the denied
// special-purpose ranges. IPv4-mapped IPv6 is rejected whatever it wraps, so a
// mapped form can never bypass the IPv4 checks.
func isApprovedAddress(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" || a.Is4In6() {
		return false
	}
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsMulticast() ||
		a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return false
	}
	for _, p := range deniedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Acquire returns the validated compressed bytes of source in a buffer the
// caller owns. The media-byte limit is the budget's remaining MediaBytes: it is
// enforced before bytes are retained and charged only on success, so a failure
// never changes the budget. Errors are *Error values carrying no URL, query,
// credentials or upstream text, except that cancellation of ctx by the caller
// is returned as the context's own error.
func (a *Acquirer) Acquire(ctx context.Context, source Source, budget *Budget) ([]byte, error) {
	if budget == nil {
		return nil, &Error{Status: 500, Code: "embedding_internal"}
	}
	switch source.Kind {
	case KindImage, KindAudio, KindVideo:
	default:
		return nil, invalidInput()
	}
	if (source.Data == "") == (source.URL == "") {
		return nil, invalidInput()
	}
	limit := min(budget.MediaBytes, MaxMediaBytes)
	var data []byte
	var err error
	var mimeFormat string
	switch {
	case source.Data != "":
		data, err = decodeBase64(source.Data, limit)
	case hasDataScheme(source.URL):
		var payload string
		mimeFormat, payload, err = parseDataURI(source.URL, source.Kind)
		if err == nil {
			data, err = decodeBase64(payload, limit)
		}
	default:
		data, err = a.fetch(ctx, source.URL, limit)
	}
	if err != nil {
		return nil, err
	}
	detected := sniff(source.Kind, data)
	if detected == "" {
		return nil, badFormat()
	}
	if mimeFormat != "" && mimeFormat != detected {
		return nil, badFormat()
	}
	if source.Format != "" && normalizeFormat(source.Format) != detected {
		return nil, badFormat()
	}
	if err := budget.Consume(Consumption{MediaBytes: uint64(len(data))}); err != nil {
		return nil, err
	}
	return data, nil
}

func hasDataScheme(u string) bool {
	return len(u) >= 5 && strings.EqualFold(u[:5], "data:")
}

// parseDataURI validates "data:[<mime>];base64,<payload>" without copying the
// payload. It returns the format named by the media type, if any.
func parseDataURI(u, kind string) (format, payload string, err error) {
	header, payload, ok := strings.Cut(u[5:], ",")
	if !ok || payload == "" {
		return "", "", invalidInput()
	}
	parts := strings.Split(header, ";")
	if len(parts) < 2 || !strings.EqualFold(parts[len(parts)-1], "base64") {
		return "", "", invalidInput()
	}
	mime := strings.ToLower(strings.TrimSpace(parts[0]))
	if mime == "" {
		return "", payload, nil
	}
	table := map[string]struct{ kind, format string }{
		"image/png": {KindImage, "png"}, "image/jpeg": {KindImage, "jpeg"}, "image/jpg": {KindImage, "jpeg"},
		"audio/wav": {KindAudio, "wav"}, "audio/x-wav": {KindAudio, "wav"}, "audio/wave": {KindAudio, "wav"},
		"audio/vnd.wave": {KindAudio, "wav"}, "audio/mpeg": {KindAudio, "mp3"}, "audio/mp3": {KindAudio, "mp3"},
		"audio/flac": {KindAudio, "flac"}, "audio/x-flac": {KindAudio, "flac"},
		"video/mp4": {KindVideo, "mp4"}, "video/webm": {KindVideo, "webm"},
	}
	entry, ok := table[mime]
	if !ok || entry.kind != kind {
		return "", "", badFormat()
	}
	return entry.format, payload, nil
}

func normalizeFormat(f string) string {
	f = strings.ToLower(strings.TrimSpace(f))
	if f == "jpg" {
		return "jpeg"
	}
	return f
}

// decodeBase64 computes the exact decoded size from the text length and its
// padding and rejects it against limit before allocating the output.
func decodeBase64(s string, limit uint64) ([]byte, error) {
	n := len(s)
	if n == 0 || n%4 != 0 || strings.ContainsAny(s, "\r\n") {
		return nil, invalidInput()
	}
	pad := 0
	for pad < 2 && s[n-1-pad] == '=' {
		pad++
	}
	size := uint64(n/4)*3 - uint64(pad)
	if size > limit {
		return nil, overLimit()
	}
	out := make([]byte, size)
	r := base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(s))
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, invalidInput()
	}
	var extra [1]byte
	if k, err := r.Read(extra[:]); k != 0 || err != io.EOF {
		return nil, invalidInput()
	}
	return out, nil
}

func (a *Acquirer) fetch(ctx context.Context, raw string, limit uint64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, invalidInput()
	}
	// Reject denied literals and names before any request or resolution.
	if ip, perr := netip.ParseAddr(u.Hostname()); perr == nil {
		if !isApprovedAddress(ip) {
			return nil, fetchFailed()
		}
	} else if deniedName(u.Hostname()) {
		return nil, fetchFailed()
	}
	fctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, invalidInput()
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "LocalAI-embedding-media")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, a.classify(ctx, fctx)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fetchFailed()
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return nil, fetchFailed()
	}
	if resp.ContentLength > 0 && uint64(resp.ContentLength) > limit {
		return nil, overLimit()
	}
	data, err := readBounded(resp.Body, limit, resp.ContentLength)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return nil, err
		}
		return nil, a.classify(ctx, fctx)
	}
	return data, nil
}

// classify maps a transport or read failure without exposing its text.
func (a *Acquirer) classify(parent, fetch context.Context) error {
	switch {
	case errors.Is(parent.Err(), context.Canceled):
		return context.Canceled
	case errors.Is(parent.Err(), context.DeadlineExceeded), errors.Is(fetch.Err(), context.DeadlineExceeded):
		return fetchTimeout()
	}
	return fetchFailed()
}

// readBounded reads at most limit bytes and fails at limit+1, growing the
// buffer geometrically but never past limit+1. A known length sizes the first
// allocation exactly.
func readBounded(r io.Reader, limit uint64, hint int64) ([]byte, error) {
	maximum := limit + 1
	// The declared length is only a claim until the bytes arrive, so the first
	// allocation is capped and the buffer grows with what is actually read.
	first := uint64(64 << 10)
	if hint >= 0 {
		first = min(first, uint64(hint)+1)
	}
	first = min(first, maximum)
	buf := make([]byte, 0, first)
	for {
		if len(buf) == cap(buf) {
			if uint64(len(buf)) >= maximum {
				return nil, overLimit()
			}
			grown := make([]byte, len(buf), min(uint64(cap(buf))*2, maximum))
			copy(grown, buf)
			buf = grown
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if uint64(len(buf)) > limit {
			return nil, overLimit()
		}
		if err == io.EOF {
			return slices.Clip(buf), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// sniff identifies a supported container by its leading bytes only. It does
// not decode, and returns "" for anything unsupported for the kind.
func sniff(kind string, b []byte) string {
	switch kind {
	case KindImage:
		switch {
		case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
			return "png"
		case len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff:
			return "jpeg"
		}
	case KindAudio:
		switch {
		case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
			return "wav"
		case bytes.HasPrefix(b, []byte("fLaC")):
			return "flac"
		case bytes.HasPrefix(b, []byte("ID3")), isMP3Frame(b):
			return "mp3"
		}
	case KindVideo:
		switch {
		case len(b) >= 12 && string(b[4:8]) == "ftyp" && mp4Brands[string(b[8:12])]:
			return "mp4"
		case bytes.HasPrefix(b, []byte{0x1a, 0x45, 0xdf, 0xa3}) && bytes.Contains(b[:min(len(b), 64)], []byte("webm")):
			return "webm"
		}
	}
	return ""
}

// mp4Brands lists the ISO-BMFF major brands accepted as MP4 video. Other
// brands (AVIF, HEIC, 3GP, M4A) share the same box layout but are not MP4.
var mp4Brands = map[string]bool{
	"isom": true, "iso2": true, "iso4": true, "iso5": true, "iso6": true,
	"mp41": true, "mp42": true, "avc1": true, "M4V ": true, "dash": true,
}

// isMP3Frame checks an MPEG audio Layer III frame header.
func isMP3Frame(b []byte) bool {
	if len(b) < 4 || b[0] != 0xff || b[1]&0xe0 != 0xe0 {
		return false
	}
	version, layer := (b[1]>>3)&3, (b[1]>>1)&3
	return version != 1 && layer == 1 && b[2]>>4 != 0xf && (b[2]>>2)&3 != 3
}
