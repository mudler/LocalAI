package modelsearch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// PirateFaceName is the provider id of Pirate Face.
const PirateFaceName = "pirateface"

const defaultPirateFaceBase = "https://pirateface.co"

// PirateFaceOptions configures the Pirate Face provider.
type PirateFaceOptions struct {
	// BaseURL is the site root; default https://pirateface.co.
	BaseURL string
	Client  *http.Client
}

type pirateFace struct {
	base   string
	client *http.Client
}

// NewPirateFace returns the Pirate Face provider. It reads the plain text
// search page (GET /s/<query>) and never calls the /api paths, which the
// site's robots.txt disallows.
func NewPirateFace(o PirateFaceOptions) Provider {
	if o.BaseURL == "" {
		o.BaseURL = defaultPirateFaceBase
	}
	if o.Client == nil {
		o.Client = newHTTPClient()
	}
	return &pirateFace{base: strings.TrimRight(o.BaseURL, "/"), client: o.Client}
}

func (p *pirateFace) Name() string { return PirateFaceName }

// Pirate Face does not implement LatestLister. Its public text listing has
// no sort option and no documented order, and the pages that show recent
// models (the home page, the model index) are HTML meant for a browser. The
// only machine readable lists are under /api, which robots.txt disallows.

// A result line: <owner/repo> <size> <unit> <n> seeds <magnet link>
var pfLine = regexp.MustCompile(`^([\w.-]+/[\w.-]+)\s+([0-9]+(?:\.[0-9]+)?)\s*([KMGT]i?B)\s+([0-9]+)\s+seeds?\s+(magnet:\?xt=urn:btih:\S+)$`)

var pfUnits = map[string]float64{
	"KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12,
	"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40,
}

func (p *pirateFace) Search(ctx context.Context, q Query) ([]Result, error) {
	// The site has no format or sort option. Its results are open-weight
	// repositories of any format, so Format and Sort are ignored.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/s/"+url.PathEscape(q.Text), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/plain")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Pirate Face answered status %d", resp.StatusCode)
	}
	body, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	return parsePirateFace(string(body), q.Limit), nil
}

// parsePirateFace reads the text listing and skips lines it does not
// understand. A limit of zero or less means no limit.
func parsePirateFace(body string, limit int) []Result {
	out := []Result{}
	for line := range strings.SplitSeq(body, "\n") {
		m := pfLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		size, _ := strconv.ParseFloat(m[2], 64)
		seeders, _ := strconv.ParseInt(m[4], 10, 64)
		out = append(out, Result{
			ID:        m[1],
			Title:     m[1],
			URI:       "pirateface://" + m[1],
			Magnet:    m[5],
			URL:       "https://huggingface.co/" + m[1],
			SizeBytes: int64(size * pfUnits[strings.ToUpper(m[3])]),
			Seeders:   seeders,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
