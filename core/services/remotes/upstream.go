package remotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

const (
	defaultTimeout  = 15 * time.Second
	defaultMaxBody  = 4 << 20 // 4 MiB
	anthropicOrigin = "https://api.anthropic.com"
)

// upstreamError is a failed contact with a remote. Step is one of the Step
// constants and Message can be shown to a person. It never holds the key.
type upstreamError struct {
	Step    string
	Message string
}

func (e *upstreamError) Error() string { return e.Message }

func upstreamErr(step, format string, args ...any) error {
	return &upstreamError{Step: step, Message: fmt.Sprintf(format, args...)}
}

// statusForStep maps a failed step to the status of a remote.
func statusForStep(step string) Status {
	if step == StepAuth {
		return StatusAuthFailed
	}
	return StatusUnreachable
}

// target is a remote, with its address normalised.
type target struct {
	kind Kind
	// base is the normalised address: the server root for localai and
	// anthropic, the API base (with /v1) for openai. It has no trailing
	// slash and no query.
	base *url.URL
	key  string
}

// normalizeURL turns what an admin typed into the address the kind works
// with. It rejects anything that is not a plain http or https address, and an
// address that holds credentials: the key has its own field.
func normalizeURL(kind Kind, raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if kind == KindAnthropic {
			raw = anthropicOrigin
		} else {
			return nil, upstreamErr(StepInvalidURL, "Enter the address of the server.")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, upstreamErr(StepInvalidURL, "The address must start with http:// or https:// and name a host.")
	}
	if u.User != nil {
		return nil, upstreamErr(StepInvalidURL, "The address must not hold a user name or password. Use the key field.")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, upstreamErr(StepInvalidURL, "The address must not have a query or a fragment.")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	switch kind {
	case KindLocalAI:
		u.Path = strings.TrimSuffix(u.Path, "/v1")
	case KindOpenAI:
		u.Path = strings.TrimSuffix(u.Path, "/chat/completions")
		u.Path = strings.TrimSuffix(u.Path, "/models")
	case KindAnthropic:
		for _, suffix := range []string{"/messages", "/models"} {
			u.Path = strings.TrimSuffix(u.Path, suffix)
		}
		u.Path = strings.TrimSuffix(u.Path, "/v1")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func parseTarget(kind Kind, rawURL, key string) (target, error) {
	if !kind.valid() {
		return target{}, errBadKind()
	}
	u, err := normalizeURL(kind, rawURL)
	if err != nil {
		return target{}, err
	}
	return target{kind: kind, base: u, key: key}, nil
}

// endpoint returns the address of a path below the base.
func (t target) endpoint(path string, query url.Values) string {
	u := *t.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

// entryUpstreamURL is the value of proxy.upstream_url that an entry of this
// target carries.
func (t target) entryUpstreamURL() string {
	switch t.kind {
	case KindOpenAI:
		return t.endpoint("/chat/completions", nil)
	case KindAnthropic:
		return t.endpoint("/v1/messages", nil)
	default:
		return t.endpoint("", nil)
	}
}

// fetcher makes the outbound calls to a remote. list sets hc for the
// length of one listing, so the calls of a listing share one client and its
// connections.
type fetcher struct {
	timeout time.Duration
	maxBody int64
	hc      *http.Client
}

func newFetcher() fetcher { return fetcher{timeout: defaultTimeout, maxBody: defaultMaxBody} }

// client builds the HTTP client for one target. The admin chose the address,
// so private networks are allowed; the guard still refuses the cloud
// metadata addresses, and redirects are refused by the shared client.
func (f fetcher) client() *http.Client {
	g := safehttp.NewGuard(safehttp.GuardPolicy{AllowPrivate: true}, nil)
	t := httpclient.HardenedTransport()
	t.Proxy = nil
	t.DialContext = g.DialContext
	return httpclient.NewWithTimeout(f.timeout, httpclient.WithTransport(t))
}

// get requests address and decodes a JSON answer into out. A status that is
// not 2xx is returned as an upstreamError: 401 and 403 as auth, others as
// unreachable. Error messages name the host at most, never the key.
func (f fetcher) get(ctx context.Context, address string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return upstreamErr(StepInvalidURL, "The address is not valid.")
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c := f.hc
	if c == nil {
		c = f.client()
		defer c.CloseIdleConnections()
	}
	resp, err := c.Do(req)
	if err != nil {
		return classifyTransportError(err, req.URL.Host)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return upstreamErr(StepAuth, "The server refused the key (status %d).", resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return upstreamErr(StepUnreachable, "The server answered with status %d. Check the address.", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBody+1))
	if err != nil {
		return classifyTransportError(err, req.URL.Host)
	}
	if int64(len(body)) > f.maxBody {
		return upstreamErr(StepUnreachable, "The answer of the server is too large.")
	}
	if err := json.Unmarshal(body, out); err != nil {
		return upstreamErr(StepUnreachable, "The server did not answer with the expected data. Check the address.")
	}
	return nil
}

func classifyTransportError(err error, host string) error {
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, httpclient.ErrRedirectBlocked):
		return upstreamErr(StepUnreachable, "The server redirected the request. Use the address it redirects to.")
	case errors.Is(err, safehttp.ErrBlockedAddress):
		return upstreamErr(StepInvalidURL, "That address is not allowed.")
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return upstreamErr(StepUnreachable, "%s did not answer in time.", host)
	case errors.Is(err, context.Canceled):
		return upstreamErr(StepUnreachable, "The request was cancelled.")
	case errors.As(err, &dnsErr):
		return upstreamErr(StepUnreachable, "Could not find %s.", host)
	default:
		return upstreamErr(StepUnreachable, "Could not connect to %s.", host)
	}
}

// bearer returns the headers of a bearer key, or none.
func bearer(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + key}
}

// list asks the remote which models it offers. It fails with an
// upstreamError; zero usable models is the step no_models. The listing is
// cut to MaxListedModels in sort order; total says how many usable models
// the remote listed.
func (f fetcher) list(ctx context.Context, t target) (models []UpstreamModel, total int, err error) {
	f.hc = f.client()
	defer f.hc.CloseIdleConnections()
	switch t.kind {
	case KindLocalAI:
		models, err = f.listLocalAI(ctx, t)
	case KindOpenAI:
		models, err = f.listOpenAI(ctx, t)
	case KindAnthropic:
		models, err = f.listAnthropic(ctx, t)
	default:
		return nil, 0, errBadKind()
	}
	if err != nil {
		return nil, 0, err
	}
	if len(models) == 0 {
		return nil, 0, upstreamErr(StepNoModels, "The server lists no model that can be used.")
	}
	total = len(models)
	if total > MaxListedModels {
		models = models[:MaxListedModels]
	}
	return models, total, nil
}
