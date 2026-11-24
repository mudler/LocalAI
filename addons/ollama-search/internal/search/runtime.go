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

// The filters the source declares. Each one is a capability of the site.
const (
	FilterTools    = "tools"
	FilterVision   = "vision"
	FilterThinking = "thinking"
)

// capabilityFilters are the filter IDs the add-on accepts. The ID is also the
// value of the c parameter of the site.
var capabilityFilters = []string{FilterTools, FilterVision, FilterThinking}

const (
	defaultBase = "https://ollama.com"
	userAgent   = "LocalAI model search (+https://github.com/mudler/LocalAI)"
	// maxBody caps what is read from the site.
	maxBody = 4 << 20
	// maxQuery is the longest search text, in bytes.
	maxQuery = 200
	// maxLimit is the most results the add-on returns.
	maxLimit = 50
	// defaultLimit applies when the request names no limit.
	defaultLimit = 20
)

// Options configure the runtime.
type Options struct {
	// BaseURL is the site root. Zero: https://ollama.com. Tests point it at a
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
	Tool     string         `json:"tool"`
	Query    string         `json:"q"`
	Limit    int            `json:"limit"`
	Sort     string         `json:"sort"`
	Variants string         `json:"variants"`
	Filters  map[string]any `json:"filters"`
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

func answerVariants(v []Variant, errText string) contract.Result {
	if errText != "" {
		return contract.Result{Payload: protocol.Payload(map[string]any{"variants": []Variant{}, "error": errText})}
	}
	return contract.Result{Payload: protocol.Payload(map[string]any{"variants": v})}
}

// sortParam maps a sort of the host to the o parameter of the site. The site
// default is popular, so downloads and every sort it cannot do, such as likes,
// send no o. Updated and latest both mean newest.
func sortParam(sort string) string {
	if sort == "updated" || sort == "latest" {
		return "newest"
	}
	return ""
}

// Read serves the search, or the tags of one model when the variants argument
// is set. An upstream failure is an answer with an error text, which the host
// shows as the failed status of this source.
func (r *runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	var a args
	if json.Unmarshal(env.Request.Arguments, &a) != nil || a.Tool != ToolSearch || !operationShape.MatchString(env.Request.OperationID) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	if a.Variants != "" {
		return r.readVariants(ctx, a.Variants)
	}
	want := map[string]bool{}
	for k, v := range a.Filters {
		b, isBool := v.(bool)
		if !isBool || !containsString(capabilityFilters, k) {
			return contract.Result{}, protocol.ErrBadRequest
		}
		want[k] = b
	}
	q := strings.TrimSpace(a.Query)
	if len(q) > maxQuery {
		q = strings.ToValidUTF8(q[:maxQuery], "")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)

	vals := url.Values{}
	if q != "" {
		vals.Set("q", q)
	}
	if o := sortParam(a.Sort); o != "" {
		vals.Set("o", o)
	}
	// local leaves out the models that run only in the cloud.
	vals.Add("c", "local")
	for _, f := range capabilityFilters {
		if want[f] {
			vals.Add("c", f)
		}
	}
	body, err := r.fetch(ctx, "/search?"+vals.Encode())
	if err != nil {
		return r.failed(err, false)
	}
	rows, err := ParseSearch(body, limit)
	if err != nil {
		return answer(nil, err.Error()), nil
	}
	return answer(rows, ""), nil
}

func (r *runtime) readVariants(ctx context.Context, name string) (contract.Result, error) {
	if !modelName.MatchString(name) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	body, err := r.fetch(ctx, "/library/"+name+"/tags")
	if err != nil {
		return r.failed(err, true)
	}
	v, err := ParseTags(body, name)
	if err != nil {
		return answerVariants(nil, err.Error()), nil
	}
	return answerVariants(v, ""), nil
}

func (r *runtime) failed(err error, variants bool) (contract.Result, error) {
	if errors.Is(err, context.Canceled) {
		return contract.Result{}, err
	}
	if variants {
		return answerVariants(nil, err.Error()), nil
	}
	return answer(nil, err.Error()), nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// fetch reads one page by its path and query. Its error text is a sentence made
// here: it never carries the URL or the body of the site.
func (r *runtime) fetch(ctx context.Context, pathAndQuery string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+pathAndQuery, nil)
	if err != nil {
		return "", errors.New("the search address is not valid")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("Ollama did not answer")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama answered status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", errors.New("Ollama answer could not be read")
	}
	if len(b) > maxBody {
		return "", fmt.Errorf("Ollama answer is larger than %d bytes", maxBody)
	}
	return string(b), nil
}

var errNoTool = errors.New("this operation is not served by the Ollama search add-on")

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
