package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// ErrPrivateURL refuses a plugin or marketplace location on a private or
// internal network while the administrator has not allowed such addresses.
// errors.Is(err, ErrRefusedURL) holds for it too.
var ErrPrivateURL = errors.New("the address is on a private or internal network. An administrator can allow such addresses in the settings (mcp_connectors_allow_private)")

// resolveTimeout bounds the DNS lookup of the private-address check.
const resolveTimeout = 5 * time.Second

func (f *Fetcher) refusesPrivate() bool { return f.refusePrivate != nil && f.refusePrivate() }

// checkSource checks a git location before any git command runs: the rules
// of CheckGitURL, then, unless private addresses are allowed, the check of
// the MCP connectors (no localhost or private literal address) and a DNS
// lookup whose answers must all be public. Git resolves the name again when
// it connects, so a DNS answer that changes in between is not caught; the
// check stops the plain case of a marketplace entry that names an internal
// host.
func (f *Fetcher) checkSource(ctx context.Context, raw string) error {
	if err := f.CheckGitURL(raw); err != nil {
		return err
	}
	if !f.refusesPrivate() {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		// file:// in tests only.
		return nil
	}
	return f.checkPublicHost(ctx, raw, u.Hostname())
}

func (f *Fetcher) checkPublicHost(ctx context.Context, raw, host string) error {
	if err := mcpconns.CheckURL(raw, true); err != nil {
		return fmt.Errorf("%w: %w: %s", ErrRefusedURL, ErrPrivateURL, redact(raw))
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, err := f.lookup(ctx, host)
	if err != nil {
		return fmt.Errorf("fetch: resolve %s: %w", host, err)
	}
	for _, a := range addrs {
		if !httpclient.IsPublicIP(a.IP) {
			return fmt.Errorf("%w: %w: %s", ErrRefusedURL, ErrPrivateURL, redact(raw))
		}
	}
	return nil
}

// httpClient is the client of a marketplace.json download: one that dials
// only public addresses while private ones are refused. Its dialer checks
// the address it connects to, so a changed DNS answer is caught here.
func (f *Fetcher) httpClient() *http.Client {
	if f.refusesPrivate() {
		return f.publicHTTP
	}
	return f.http
}

// publicClient is the client of a marketplace URL while private addresses
// are refused: it dials only public addresses, checks each redirect target
// against the same rule, and follows only https redirects.
func publicClient(timeout time.Duration) *http.Client {
	c := httpclient.NewWithTimeout(timeout, httpclient.WithExternalOnly(), httpclient.WithFollowRedirects())
	c.CheckRedirect = httpsRedirects(c.CheckRedirect)
	return c
}
