package modelsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/downloader"
)

// AddonSource is a source that an installed add-on provides, as the broker
// of the add-ons reports it.
type AddonSource struct {
	AddonID string
	// Tool is the add-on's own name of the search tool (the tool of the role
	// search of its declaration).
	Tool string
	// Running is true when the add-on is enabled and running.
	Running bool
	Search  contract.ModelSearch
}

// AddonBackend is what the host needs from the add-on gateway. It is an
// interface so the adapter is tested with a fake add-on.
type AddonBackend interface {
	// Sources lists the add-ons that provide the model-search slot and that
	// the person can see, running or not.
	Sources(ctx context.Context, user string) ([]AddonSource, error)
	// Call runs the search tool of an add-on as the person and returns the
	// JSON payload of the answer.
	Call(ctx context.Context, user, addonID, tool string, args json.RawMessage) (json.RawMessage, error)
}

// The caps of a row of an add-on (search-result-validation).
const (
	maxRowID     = 200
	maxRowTitle  = 300
	maxRowLink   = 2048
	maxRowTags   = 32
	maxRowTagLen = 64
	maxRowText   = 200
)

// ErrAddonNotRunning is the reason of a source whose add-on is not running.
var ErrAddonNotRunning = errors.New("the add-on of this source is not running")

type addonProvider struct {
	backend AddonBackend
	src     AddonSource
}

func newAddonProvider(b AddonBackend, src AddonSource) *addonProvider {
	return &addonProvider{backend: b, src: src}
}

func (p *addonProvider) Name() string { return p.src.Search.Source.ID }

// ListsLatest implements LatestLister.
func (p *addonProvider) ListsLatest() bool { return p.src.Search.Source.ListsLatest }

// Source implements Describer.
func (p *addonProvider) Source() Source {
	ms := p.src.Search
	out := Source{ID: ms.Source.ID, Title: ms.Source.Title, Icon: ms.Source.Icon, Kind: "addon",
		ListsLatest: ms.Source.ListsLatest, HasVariants: ms.Source.HasVariants, P2P: ms.Source.P2P, Sorts: ms.Sorts, Filters: ms.Filters, Running: p.src.Running}
	return out
}

func (p *addonProvider) Search(ctx context.Context, q Query) ([]Result, error) {
	rows, _, err := p.SearchCounted(ctx, q)
	return rows, err
}

// SearchCounted calls the add-on and checks every row it returns.
func (p *addonProvider) SearchCounted(ctx context.Context, q Query) ([]Result, int, error) {
	if !p.src.Running {
		return nil, 0, ErrAddonNotRunning
	}
	req := contract.ModelSearchRequest{Query: q.Text, Limit: q.Limit, Sort: q.Sort, Format: q.Format,
		Filters: typedFilters(p.Source(), q.Own)}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}
	payload, err := p.backend.Call(ctx, UserFrom(ctx), p.src.AddonID, p.src.Tool, body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, fmt.Errorf("the add-on could not search: %w", err)
	}
	if len(payload) > maxBody {
		return nil, 0, fmt.Errorf("answer larger than %d bytes", maxBody)
	}
	var out struct {
		Results []json.RawMessage `json:"results"`
		// Error is the reason a source gives when its own upstream failed. It
		// is text from outside: it is capped and cleaned before it is shown.
		Error string `json:"error"`
	}
	if err := json.Unmarshal(payload, &out); err != nil || (out.Results == nil && out.Error == "") {
		return nil, 0, errors.New("the add-on answered with something that is not a list of results")
	}
	if out.Error != "" {
		return nil, 0, errors.New("the source failed: " + cleanReason(out.Error))
	}
	rows := out.Results
	if len(rows) > MaxLimit {
		rows = rows[:MaxLimit]
	}
	res := make([]Result, 0, len(rows))
	dropped := 0
	for _, raw := range rows {
		var r Result
		if json.Unmarshal(raw, &r) != nil {
			dropped++
			continue
		}
		clean, ok := cleanRow(r, p.Name(), p.src.Search.Source.HasVariants)
		if !ok {
			dropped++
			continue
		}
		res = append(res, clean)
	}
	return res, dropped, nil
}

// cleanRow applies search-result-validation to one row. It returns false for a
// row to drop. The fields that the host owns are set here, whatever the add-on
// said.
func cleanRow(r Result, source string, hasVariants bool) (Result, bool) {
	// Text that a person reads loses control and direction-override characters
	// first, so a cap counts what is shown and a title cannot reorder the page.
	if len(r.Tags) > maxRowTags {
		return Result{}, false
	}
	r.ID, r.Title = stripHidden(r.ID), stripHidden(r.Title)
	r.License, r.PipelineTag = stripHidden(r.License), stripHidden(r.PipelineTag)
	tags := make([]string, 0, len(r.Tags))
	for _, t := range r.Tags {
		if t = stripHidden(t); t != "" {
			tags = append(tags, t)
		}
	}
	r.Tags = tags
	if r.ID == "" || len(r.ID) > maxRowID || len(r.Title) > maxRowTitle ||
		len(r.URL) > maxRowLink || len(r.Magnet) > maxRowLink || len(r.URI) > maxRowLink ||
		!utf8.ValidString(r.ID+r.Title+r.URI+r.URL+r.Magnet) {
		return Result{}, false
	}
	if !downloader.AllowedSearchURI(r.URI) {
		return Result{}, false
	}
	if r.URL != "" && !strings.HasPrefix(strings.ToLower(r.URL), "https://") {
		return Result{}, false
	}
	if r.Magnet != "" && !strings.HasPrefix(strings.ToLower(r.Magnet), downloader.MagnetPrefix) {
		return Result{}, false
	}
	for _, t := range r.Tags {
		if len(t) > maxRowTagLen {
			return Result{}, false
		}
	}
	if r.Title == "" {
		r.Title = r.ID
	}
	// These three are shown as text and are not part of an address, so a long
	// value is cut, not a reason to drop the row.
	r.License = capBytes(r.License, maxRowText)
	r.PipelineTag = capBytes(r.PipelineTag, maxRowText)
	r.LastModified = capBytes(stripHidden(r.LastModified), maxRowText)
	r.SizeBytes, r.Downloads, r.Likes, r.Seeders = nonNeg(r.SizeBytes), nonNeg(r.Downloads), nonNeg(r.Likes), nonNeg(r.Seeders)
	r.Provider = source
	// The host computes these from its own state; EnrichSearch fills them. The
	// variants hint comes from the description of the source, not from the row.
	r.Installed, r.InstalledName, r.Backend, r.HasVariants = false, "", "", hasVariants
	return r, true
}

// stripHidden removes control characters and the format characters of Unicode
// (the direction overrides among them) from text of a row.
func stripHidden(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// capBytes cuts s to at most n bytes without cutting a character.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func nonNeg(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

// cleanReason makes the failure text of a source safe to show: one line, no
// control characters, at most 160 characters.
func cleanReason(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= 160 {
			break
		}
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
