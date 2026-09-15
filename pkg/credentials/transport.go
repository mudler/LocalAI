package credentials

import "net/http"

type transport struct {
	base http.RoundTripper
}

// Transport authenticates every request it carries, including each redirect
// hop, with the default store's matching rule.
func Transport(base http.RoundTripper) http.RoundTripper {
	return transport{base: base}
}

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A caller that set Authorization itself (WithBearerToken, the HF
	// materializer) has chosen its credential; the store must not replace it.
	if req.Header.Get("Authorization") != "" {
		return t.base.RoundTrip(req)
	}
	c, ok := Default().Match(req.URL.String())
	if !ok {
		return t.base.RoundTrip(req)
	}
	// The credential goes on a clone. net/http builds every redirect hop from
	// the original request's headers, so nothing added here follows the
	// request to another host: each hop is matched on its own.
	authed := req.Clone(req.Context())
	if err := c.ApplyHeaders(authed.Header); err != nil {
		// RoundTripper must close the body even when it fails before sending.
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return t.base.RoundTrip(authed)
}
