package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// fetchURL downloads a marketplace.json over https: at most
// MaxMarketplaceBytes, within the client's timeout, and never through a
// redirect that leaves https.
func (f *Fetcher) fetchURL(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("%w: %s", ErrRefusedURL, redact(raw))
	}
	if f.refusesPrivate() {
		if err := f.checkPublicHost(ctx, raw, u.Hostname()); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch marketplace.json: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch marketplace.json: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxMarketplaceBytes {
		return nil, fmt.Errorf("fetch: marketplace.json is over %d bytes", MaxMarketplaceBytes)
	}
	return readCapped(resp.Body, MaxMarketplaceBytes)
}
