// SPDX-License-Identifier: MIT
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// ToolSearch is the one tool of the add-on: the search role of the
// model-search slot.
const ToolSearch = "search"

// Declared is everything the manifest lists under capabilities.tools.
func Declared() []string { return []string{ToolSearch} }

// FilterMinSeeders is the one filter the source declares: the page lists the
// seeders of each model, so the add-on can drop the rows that few peers share.
const FilterMinSeeders = "min-seeders"

const (
	defaultBase = "https://pirateface.co"
	userAgent   = "LocalAI model search (+https://github.com/mudler/LocalAI)"
	// maxBody caps what is read from the site.
	maxBody = 4 << 20
	// maxQuery is the longest search text, in bytes.
	maxQuery = 200
)

// Options configure the runtime.
type Options struct {
	// BaseURL is the site root. Zero: https://pirateface.co. Tests point it at a
	// local server that serves a recorded page.
	BaseURL string
	Client  *http.Client
}

type runtime struct {
	base   string
	client *http.Client
}

// New returns the runtime of the executable.
func New(o Options) contract.Runtime {
	if o.BaseURL == "" {
		o.BaseURL = defaultBase
	}
	if o.Client == nil {
		o.Client = httpclient.NewWithTimeout(20*time.Second, httpclient.WithFollowRedirects())
	}
	return &runtime{base: strings.TrimRight(o.BaseURL, "/"), client: o.Client}
}

var operationShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type args struct {
	Tool    string         `json:"tool"`
	Query   string         `json:"q"`
	Limit   int            `json:"limit"`
	Sort    string         `json:"sort"`
	Format  string         `json:"format"`
	Filters map[string]any `json:"filters"`
}

func answer(rows []Row, errText string) contract.Result {
	if errText != "" {
		return contract.Result{Payload: protocol.Payload(map[string]any{"results": []Row{}, "error": errText})}
	}
	if rows == nil {
		rows = []Row{}
	}
	return contract.Result{Payload: protocol.Payload(map[string]any{"results": rows})}
}

// Read serves the search. The site has no format or sort option: its results
// are open-weight repositories of any format in its own order, so format and
// sort are ignored. An upstream failure is an answer with an error text, which
// the host shows as the failed status of this source.
func (r *runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	var a args
	if json.Unmarshal(env.Request.Arguments, &a) != nil || a.Tool != ToolSearch || !operationShape.MatchString(env.Request.OperationID) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	q := strings.TrimSpace(a.Query)
	if q == "" {
		// The source cannot list its newest models (see the manifest), so the
		// host never asks with no text; an empty answer is the safe reply.
		return answer(nil, ""), nil
	}
	if len(q) > maxQuery {
		q = q[:maxQuery]
	}
	var minSeeders int64
	if v, ok := a.Filters[FilterMinSeeders]; ok {
		n, isNum := v.(float64)
		if !isNum || n < 0 || n > 100 {
			return contract.Result{}, protocol.ErrBadRequest
		}
		minSeeders = int64(n)
	}
	for k := range a.Filters {
		if k != FilterMinSeeders {
			return contract.Result{}, protocol.ErrBadRequest
		}
	}
	body, err := r.fetch(ctx, q)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return contract.Result{}, err
		}
		return answer(nil, err.Error()), nil
	}
	return answer(ParseListing(body, a.Limit, minSeeders), ""), nil
}

// fetch reads GET /s/<query>. Its error text is a sentence made here: it never
// carries the URL or the body of the site.
func (r *runtime) fetch(ctx context.Context, q string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/s/"+url.PathEscape(q), nil)
	if err != nil {
		return "", errors.New("the search address is not valid")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/plain")
	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("Pirate Face did not answer")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Pirate Face answered status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", errors.New("Pirate Face answer could not be read")
	}
	if len(b) > maxBody {
		return "", fmt.Errorf("Pirate Face answer is larger than %d bytes", maxBody)
	}
	return string(b), nil
}

var errNoTool = errors.New("this operation is not served by the Pirate Face search add-on")

func (*runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errNoTool
}

func (*runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errNoTool
}
