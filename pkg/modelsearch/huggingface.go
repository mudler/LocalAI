package modelsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// HuggingFaceName is the provider id of Hugging Face.
const HuggingFaceName = "huggingface"

const defaultHuggingFaceAPI = "https://huggingface.co/api/models"

// HuggingFaceOptions configures the Hugging Face provider.
type HuggingFaceOptions struct {
	// BaseURL is the models API; default https://huggingface.co/api/models.
	BaseURL string
	// Token is optional. Without it the public rate limit applies
	// (500 requests per 5 minutes).
	Token  string
	Client *http.Client
}

type huggingFace struct {
	base   string
	token  string
	client *http.Client
}

// NewHuggingFace returns the Hugging Face provider.
func NewHuggingFace(o HuggingFaceOptions) Provider {
	if o.BaseURL == "" {
		o.BaseURL = defaultHuggingFaceAPI
	}
	if o.Client == nil {
		o.Client = newHTTPClient()
	}
	return &huggingFace{base: o.BaseURL, token: o.Token, client: o.Client}
}

func (h *huggingFace) Name() string { return HuggingFaceName }

// ListsLatest says the models API lists repositories by creation date with no
// search text.
func (h *huggingFace) ListsLatest() bool { return true }

type hfModel struct {
	ID           string   `json:"id"`
	ModelID      string   `json:"modelId"`
	Author       string   `json:"author"`
	Downloads    int64    `json:"downloads"`
	Likes        int64    `json:"likes"`
	Tags         []string `json:"tags"`
	PipelineTag  string   `json:"pipeline_tag"`
	LastModified string   `json:"lastModified"`
	Private      bool     `json:"private"`
	// Gated is false, or a string such as "auto" or "manual".
	Gated any `json:"gated"`
}

var hfSort = map[string]string{
	SortDownloads: "downloads",
	SortLikes:     "likes",
	SortUpdated:   "lastModified",
	SortTrending:  "trendingScore",
	// createdAt is when the repository was added, which is what "latest"
	// means; lastModified would also float up an old model that got a commit.
	SortLatest: "createdAt",
}

func (h *huggingFace) Search(ctx context.Context, q Query) ([]Result, error) {
	u, err := url.Parse(h.base)
	if err != nil {
		return nil, err
	}
	v := u.Query()
	if q.Text != "" {
		v.Set("search", q.Text)
	}
	v.Set("sort", hfSort[q.Sort])
	v.Set("direction", "-1")
	v.Set("limit", strconv.Itoa(q.Limit))
	if q.Format != "" && q.Format != FormatAny {
		v.Set("filter", q.Format)
	}
	// Ask for the few fields shown, so the list also carries the author and
	// the last change date at no extra request.
	for _, f := range []string{"author", "downloads", "likes", "tags", "pipeline_tag", "lastModified", "gated"} {
		v.Add("expand[]", f)
	}
	u.RawQuery = v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited by Hugging Face (status 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hugging Face answered status %d", resp.StatusCode)
	}
	body, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	var models []hfModel
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("parse Hugging Face answer: %w", err)
	}
	out := make([]Result, 0, len(models))
	for _, m := range models {
		id := m.ID
		if id == "" {
			id = m.ModelID
		}
		if id == "" || m.Private {
			continue
		}
		r := Result{
			ID:           id,
			Title:        id,
			URI:          "huggingface://" + id,
			URL:          "https://huggingface.co/" + id,
			Downloads:    m.Downloads,
			Likes:        m.Likes,
			Tags:         m.Tags,
			PipelineTag:  m.PipelineTag,
			LastModified: m.LastModified,
			Gated:        isGated(m.Gated),
		}
		for _, t := range m.Tags {
			if l, ok := strings.CutPrefix(t, "license:"); ok {
				r.License = l
				break
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func isGated(v any) bool {
	switch g := v.(type) {
	case bool:
		return g
	case string:
		return g != "" && g != "false"
	}
	return false
}
