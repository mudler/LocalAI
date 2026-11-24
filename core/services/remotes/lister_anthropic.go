package remotes

import (
	"context"
	"net/url"
	"sort"
	"strconv"
)

const (
	anthropicVersion  = "2023-06-01"
	anthropicPageSize = 100
	// anthropicMaxPages caps the walk over a listing, so a remote that
	// always says has_more cannot keep a refresh busy.
	anthropicMaxPages = 20
)

type anthropicPage struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// listAnthropic reads {base}/v1/models page by page. The remote may be
// Anthropic or a gateway for it.
func (f fetcher) listAnthropic(ctx context.Context, t target) ([]UpstreamModel, error) {
	headers := map[string]string{"anthropic-version": anthropicVersion}
	if t.key != "" {
		headers["x-api-key"] = t.key
	}
	seen := map[string]bool{}
	var models []UpstreamModel
	after := ""
	for range anthropicMaxPages {
		q := url.Values{"limit": {strconv.Itoa(anthropicPageSize)}}
		if after != "" {
			q.Set("after_id", after)
		}
		var page anthropicPage
		if err := f.get(ctx, t.endpoint("/v1/models", q), headers, &page); err != nil {
			return nil, err
		}
		for _, m := range page.Data {
			if m.ID == "" || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			models = append(models, UpstreamModel{ID: m.ID, Usecases: []string{}})
		}
		if !page.HasMore || page.LastID == "" || page.LastID == after {
			break
		}
		after = page.LastID
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
