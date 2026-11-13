package modelsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/LocalAI/pkg/vram"
	"golang.org/x/sync/singleflight"
)

var (
	// ErrBadRepo means the uri names no repository the Inspector understands.
	ErrBadRepo = errors.New("not a Hugging Face or Pirate Face repository uri")
	// ErrRepoNotFound means the provider has no such repository, or it is
	// private. Hugging Face answers 401 for both.
	ErrRepoNotFound = errors.New("repository not found")
	// ErrRateLimited means the provider asked us to slow down.
	ErrRateLimited = errors.New("rate limited by the provider")
	// ErrNoSuchFile means the requested file is not one of the repository's
	// weight files.
	ErrNoSuchFile = errors.New("file is not a weight file of the repository")
)

// RepoRef names a repository of one provider.
type RepoRef struct {
	Provider string
	ID       string // owner/repo
}

// ParseRepoURI reads the uris search results carry (huggingface://owner/repo,
// pirateface://owner/repo), plus hf:// and https://huggingface.co/owner/repo.
// A file path after the repository is ignored.
func ParseRepoURI(uri string) (RepoRef, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return RepoRef{}, ErrBadRepo
	}
	if downloader.URI(uri).LooksLikePirateFace() {
		ref, err := downloader.ParsePirateFaceURI(uri)
		if err != nil || ref.Owner == "" || ref.Repo == "" {
			return RepoRef{}, ErrBadRepo
		}
		return RepoRef{Provider: PirateFaceName, ID: ref.RepoID()}, nil
	}
	rest := uri
	for _, p := range []string{"huggingface://", "hf://"} {
		if r, ok := strings.CutPrefix(rest, p); ok {
			rest = r
			break
		}
	}
	if id, ok := vram.ExtractHFRepoID(rest); ok {
		return RepoRef{Provider: HuggingFaceName, ID: id}, nil
	}
	if strings.Contains(rest, "://") {
		return RepoRef{}, ErrBadRepo
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" && !strings.ContainsAny(rest, " ?#") {
		return RepoRef{Provider: HuggingFaceName, ID: parts[0] + "/" + parts[1]}, nil
	}
	return RepoRef{}, ErrBadRepo
}

// RepoFile is one weight file of a repository.
type RepoFile struct {
	Path string `json:"path"`
	// Size is in bytes; zero when the provider did not say.
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	// Kind is "gguf", "mmproj" or "weights" (safetensors, bin, pt).
	Kind string `json:"kind"`
}

// Variant is one quantization of a GGUF repository: all the shards of one
// model file.
type Variant struct {
	// Quantization is the label read from the file name, such as "Q4_K_M".
	Quantization string `json:"quantization"`
	// File is the first shard; pass it to the estimate route.
	File  string   `json:"file"`
	Files []string `json:"files"`
	// SizeBytes sums the shards; zero when sizes are unknown.
	SizeBytes int64 `json:"size_bytes"`
	Shards    int   `json:"shards"`
	// Preference is the value for preferences.quantizations on
	// POST /models/import-uri that makes the import pick exactly this
	// variant. Empty when no safe value exists.
	Preference string `json:"preference,omitempty"`
	// Recommended marks the variant LocalAI would suggest first.
	Recommended bool `json:"recommended,omitempty"`
}

// GGUFInfo is what Hugging Face reports from the GGUF header of a repository.
type GGUFInfo struct {
	Architecture  string `json:"architecture,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
	Parameters    int64  `json:"parameters,omitempty"`
}

// RepoFiles describes the installable content of a repository.
type RepoFiles struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	URI      string `json:"uri"`
	URL      string `json:"url,omitempty"`
	// Revision is the commit the file list and sizes come from.
	Revision string `json:"revision,omitempty"`
	// Gated is reported as is. For Pirate Face it is also true when the site
	// shows no magnet link, which is what a gated licence looks like there.
	Gated       bool     `json:"gated"`
	License     string   `json:"license,omitempty"`
	PipelineTag string   `json:"pipeline_tag,omitempty"`
	LibraryName string   `json:"library_name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	// Description is the first paragraph of the model card, at most 400
	// characters. Empty when the card could not be read.
	Description string     `json:"description,omitempty"`
	GGUF        *GGUFInfo  `json:"gguf,omitempty"`
	Files       []RepoFile `json:"files"`
	Variants    []Variant  `json:"variants"`
	// SizeKnown is false when no file size could be found, which happens for
	// a Pirate Face repository Hugging Face does not know.
	SizeKnown bool `json:"size_known"`
	// The fields below are filled by the route, not by the Inspector.
	// Backend and ImportFiles come from a dry run of the import (no download,
	// no job) on this very listing; ImportError says why it could not run.
	Backend       string   `json:"backend,omitempty"`
	Installed     bool     `json:"installed"`
	InstalledName string   `json:"installed_name,omitempty"`
	ImportFiles   []string `json:"import_files,omitempty"`
	ImportError   string   `json:"import_error,omitempty"`
	LastModified  string   `json:"last_modified,omitempty"`

	// hfBase is the site the file urls of the estimate point at.
	hfBase string
}

// WeightBytes sums the weight files that a plain import downloads: the
// recommended variant for a GGUF repository, every weight file otherwise.
func (r *RepoFiles) WeightBytes() int64 {
	for _, v := range r.Variants {
		if v.Recommended {
			return v.SizeBytes
		}
	}
	var n int64
	for _, f := range r.Files {
		if f.Kind != "mmproj" {
			n += f.Size
		}
	}
	return n
}

// InspectorOptions configures an Inspector.
type InspectorOptions struct {
	// HFBase is the Hugging Face site root; default https://huggingface.co.
	HFBase string
	// Token is optional and only raises the Hugging Face rate limit.
	Token  string
	Client *http.Client
	// TTL is how long a repository listing is reused. Default 10 minutes.
	TTL time.Duration
	// ErrorTTL is how long a failure (including a rate limit) is reused so a
	// busy UI does not hammer a provider that said no. Default 60 seconds.
	ErrorTTL time.Duration
	// MaxEntries bounds the cache. Default 256.
	MaxEntries int
	// MaxConcurrent bounds upstream calls running at once. Default 4.
	MaxConcurrent int
	// PirateFace looks a repository up on the Pirate Face index. Default
	// downloader.LookupPirateFace. This is the only call to the site's /api
	// the Inspector makes, and never from a search.
	PirateFace func(ctx context.Context, repoID string) (*downloader.PirateFaceEntry, error)
	// ReadMeta reads the architecture scalars of a remote GGUF file. Default
	// vram.ReadGGUFMetadata, which keeps its answers in the shared estimate
	// cache.
	ReadMeta func(ctx context.Context, uri string) (*vram.GGUFMeta, error)
	// QuantMatches decides whether a quantization preference selects a file
	// group, the rule the import applies. Default: whole-token match.
	QuantMatches func(base, preference string) bool
}

// Inspector reads what a repository of a remote provider offers: its weight
// files with sizes, grouped by quantization, and VRAM estimates for one of
// them. Listings are cached per repository with their own TTL, concurrent
// requests for one repository share one upstream call, and the number of
// upstream calls running at once is bounded, so a page of rows opened at the
// same time stays within the provider's rate limit.
type Inspector struct {
	opt InspectorOptions
	now func() time.Time

	sem chan struct{}
	sf  singleflight.Group

	mu    sync.Mutex
	cache map[string]inspectEntry
}

type inspectEntry struct {
	files   *RepoFiles
	err     error
	expires time.Time
}

// NewInspector returns an Inspector with defaults applied.
func NewInspector(o InspectorOptions) *Inspector {
	if o.HFBase == "" {
		o.HFBase = "https://huggingface.co"
	}
	o.HFBase = strings.TrimRight(o.HFBase, "/")
	if o.Client == nil {
		o.Client = newHTTPClient()
	}
	if o.TTL <= 0 {
		o.TTL = 10 * time.Minute
	}
	if o.ErrorTTL <= 0 {
		o.ErrorTTL = time.Minute
	}
	if o.MaxEntries <= 0 {
		o.MaxEntries = 256
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 4
	}
	if o.PirateFace == nil {
		o.PirateFace = func(ctx context.Context, repoID string) (*downloader.PirateFaceEntry, error) {
			return downloader.LookupPirateFace(ctx, repoID, "")
		}
	}
	if o.ReadMeta == nil {
		o.ReadMeta = vram.ReadGGUFMetadata
	}
	if o.QuantMatches == nil {
		o.QuantMatches = func(base, pref string) bool {
			return pref != "" && strings.Contains(strings.ToLower(base), strings.ToLower(pref))
		}
	}
	return &Inspector{
		opt:   o,
		now:   time.Now,
		sem:   make(chan struct{}, o.MaxConcurrent),
		cache: map[string]inspectEntry{},
	}
}

var (
	sharedInspectorOnce sync.Once
	sharedInspector     *Inspector
)

// SharedInspector is the process-wide Inspector, shared by the REST routes
// so they share one cache and one concurrency budget.
func SharedInspector() *Inspector {
	sharedInspectorOnce.Do(func() { sharedInspector = NewInspector(InspectorOptions{}) })
	return sharedInspector
}

func (i *Inspector) acquire(ctx context.Context) error {
	select {
	case i.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Inspector) release() { <-i.sem }

// Files lists the weight files of the repository a uri names. The answer is
// cached; the returned value is shared and must not be modified.
func (i *Inspector) Files(ctx context.Context, uri string) (*RepoFiles, error) {
	ref, err := ParseRepoURI(uri)
	if err != nil {
		return nil, err
	}
	key := ref.Provider + ":" + ref.ID
	if f, err, ok := i.cached(key); ok {
		return f, err
	}
	v, err, _ := i.sf.Do(key, func() (any, error) {
		// A caller that gave up must not cancel the call the others wait on.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
		defer cancel()
		f, err := i.fetch(fctx, ref)
		i.store(key, f, err)
		return f, err
	})
	if err != nil {
		return nil, err
	}
	return v.(*RepoFiles), nil
}

func (i *Inspector) cached(key string) (*RepoFiles, error, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	e, ok := i.cache[key]
	if !ok {
		return nil, nil, false
	}
	if !i.now().Before(e.expires) {
		delete(i.cache, key)
		return nil, nil, false
	}
	return e.files, e.err, true
}

func (i *Inspector) store(key string, f *RepoFiles, err error) {
	if err != nil && !errors.Is(err, ErrRateLimited) && !errors.Is(err, ErrRepoNotFound) {
		// Other failures (a timeout, a bad answer) are retried at once.
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	now := i.now()
	if len(i.cache) >= i.opt.MaxEntries {
		for k, e := range i.cache {
			if !now.Before(e.expires) {
				delete(i.cache, k)
			}
		}
		if len(i.cache) >= i.opt.MaxEntries {
			keys := make([]string, 0, len(i.cache))
			for k := range i.cache {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(a, b int) bool { return i.cache[keys[a]].expires.Before(i.cache[keys[b]].expires) })
			delete(i.cache, keys[0])
		}
	}
	ttl := i.opt.TTL
	if err != nil {
		ttl = i.opt.ErrorTTL
	}
	i.cache[key] = inspectEntry{files: f, err: err, expires: now.Add(ttl)}
}

func (i *Inspector) fetch(ctx context.Context, ref RepoRef) (*RepoFiles, error) {
	if err := i.acquire(ctx); err != nil {
		return nil, err
	}
	defer i.release()
	switch ref.Provider {
	case PirateFaceName:
		return i.fetchPirateFace(ctx, ref)
	default:
		return i.fetchHuggingFace(ctx, ref, "")
	}
}

// hfRepoAnswer is the part of GET /api/models/<repo>?blobs=true that is used.
type hfRepoAnswer struct {
	ID          string   `json:"id"`
	SHA         string   `json:"sha"`
	Gated       any      `json:"gated"`
	Tags        []string `json:"tags"`
	PipelineTag string   `json:"pipeline_tag"`
	LibraryName string   `json:"library_name"`
	LastMod     string   `json:"lastModified"`
	CardData    struct {
		License any `json:"license"`
	} `json:"cardData"`
	GGUF *struct {
		Total         int64  `json:"total"`
		Architecture  string `json:"architecture"`
		ContextLength int64  `json:"context_length"`
	} `json:"gguf"`
	Siblings []struct {
		Name string `json:"rfilename"`
		Size int64  `json:"size"`
		LFS  *struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"lfs"`
	} `json:"siblings"`
}

func (i *Inspector) hfGet(ctx context.Context, u, rng string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if rng != "" {
		req.Header.Set("Range", rng)
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if i.opt.Token != "" {
		req.Header.Set("Authorization", "Bearer "+i.opt.Token)
	}
	return i.opt.Client.Do(req)
}

func hfStatusError(status int, what string) error {
	switch {
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w (Hugging Face status 429)", ErrRateLimited)
	case status == http.StatusNotFound || status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrRepoNotFound, what)
	}
	return fmt.Errorf("Hugging Face answered status %d for %s", status, what)
}

// hfRepo reads one repository listing with file sizes. revision empty means
// the default branch.
func (i *Inspector) hfRepo(ctx context.Context, id, revision string) (*hfRepoAnswer, error) {
	u := i.opt.HFBase + "/api/models/" + id
	if revision != "" {
		u += "/revision/" + url.PathEscape(revision)
	}
	resp, err := i.hfGet(ctx, u+"?blobs=true", "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, hfStatusError(resp.StatusCode, id)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	var a hfRepoAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("parse Hugging Face answer: %w", err)
	}
	return &a, nil
}

func (a *hfRepoAnswer) license() string {
	if s, ok := a.CardData.License.(string); ok && s != "" {
		return s
	}
	for _, t := range a.Tags {
		if l, ok := strings.CutPrefix(t, "license:"); ok {
			return l
		}
	}
	return ""
}

func (i *Inspector) fetchHuggingFace(ctx context.Context, ref RepoRef, revision string) (*RepoFiles, error) {
	a, err := i.hfRepo(ctx, ref.ID, revision)
	if err != nil {
		return nil, err
	}
	out := &RepoFiles{
		Provider:     HuggingFaceName,
		ID:           ref.ID,
		URI:          "huggingface://" + ref.ID,
		URL:          i.opt.HFBase + "/" + ref.ID,
		Revision:     a.SHA,
		Gated:        isGated(a.Gated),
		License:      a.license(),
		PipelineTag:  a.PipelineTag,
		LibraryName:  a.LibraryName,
		Tags:         a.Tags,
		LastModified: a.LastMod,
		hfBase:       i.opt.HFBase,
	}
	if a.GGUF != nil {
		out.GGUF = &GGUFInfo{Architecture: a.GGUF.Architecture, ContextLength: a.GGUF.ContextLength, Parameters: a.GGUF.Total}
	}
	for _, s := range a.Siblings {
		size, sha := s.Size, ""
		if s.LFS != nil {
			sha = s.LFS.SHA256
			if s.LFS.Size > 0 {
				size = s.LFS.Size
			}
		}
		out.Files = appendWeight(out.Files, s.Name, size, sha)
	}
	i.finish(out)
	// The model card is best effort: a failure leaves the description empty.
	out.Description = i.readme(ctx, ref.ID, a.SHA)
	return out, nil
}

func (i *Inspector) fetchPirateFace(ctx context.Context, ref RepoRef) (*RepoFiles, error) {
	entry, err := i.opt.PirateFace(ctx, ref.ID)
	if err != nil {
		if errors.Is(err, downloader.ErrPirateFaceNotListed) {
			return nil, fmt.Errorf("%w: %v", ErrRepoNotFound, err)
		}
		return nil, err
	}
	out := &RepoFiles{
		Provider: PirateFaceName,
		ID:       ref.ID,
		URI:      "pirateface://" + ref.ID,
		URL:      i.opt.HFBase + "/" + ref.ID,
		Revision: entry.Revision,
		// The site shows no magnet link for a gated licence until the
		// terms are accepted there; the importer cannot get past that.
		Gated:  entry.Magnet == "",
		hfBase: i.opt.HFBase,
	}
	// The index lists paths and digests, no sizes. The torrent mirrors a
	// Hugging Face commit, so the sizes come from that commit.
	var sizes map[string]int64
	if entry.HF == nil || *entry.HF {
		if a, err := i.hfRepo(ctx, ref.ID, entry.Revision); err == nil {
			sizes = map[string]int64{}
			for _, s := range a.Siblings {
				sz := s.Size
				if s.LFS != nil && s.LFS.Size > 0 {
					sz = s.LFS.Size
				}
				sizes[s.Name] = sz
			}
			out.License = a.license()
			out.PipelineTag = a.PipelineTag
			out.LibraryName = a.LibraryName
			out.Tags = a.Tags
			out.LastModified = a.LastMod
			out.Gated = out.Gated || isGated(a.Gated)
			if a.GGUF != nil {
				out.GGUF = &GGUFInfo{Architecture: a.GGUF.Architecture, ContextLength: a.GGUF.ContextLength, Parameters: a.GGUF.Total}
			}
		} else if errors.Is(err, ErrRateLimited) {
			return nil, err
		}
	}
	seen := map[string]bool{}
	add := func(files []downloader.PirateFaceFile) {
		for _, f := range files {
			if f.Path == "" || seen[f.Path] {
				continue
			}
			seen[f.Path] = true
			out.Files = appendWeight(out.Files, f.Path, sizes[f.Path], f.SHA256)
		}
	}
	add(entry.Files)
	for _, q := range entry.Quants {
		add(q.Files)
	}
	i.finish(out)
	if out.Revision != "" && sizes != nil {
		out.Description = i.readme(ctx, ref.ID, entry.Revision)
	}
	return out, nil
}

// appendWeight adds a file when it is a weight file.
func appendWeight(files []RepoFile, name string, size int64, sha string) []RepoFile {
	lower := strings.ToLower(name)
	kind := ""
	switch {
	case strings.HasSuffix(lower, ".gguf") && strings.Contains(lower, "mmproj"):
		kind = "mmproj"
	case strings.HasSuffix(lower, ".gguf"):
		kind = "gguf"
	case strings.HasSuffix(lower, ".safetensors"), strings.HasSuffix(lower, ".bin"), strings.HasSuffix(lower, ".pt"):
		kind = "weights"
	default:
		return files
	}
	return append(files, RepoFile{Path: name, Size: size, SHA256: sha, Kind: kind})
}

// finish builds the variants and the size flag from the weight files.
func (i *Inspector) finish(r *RepoFiles) {
	if r.Files == nil {
		r.Files = []RepoFile{}
	}
	if r.Variants == nil {
		r.Variants = []Variant{}
	}
	var ggufs []hfapi.ModelFile
	byPath := map[string]RepoFile{}
	for _, f := range r.Files {
		byPath[f.Path] = f
		if f.Kind == "gguf" {
			ggufs = append(ggufs, hfapi.ModelFile{Path: f.Path, Size: f.Size})
		}
	}
	groups := hfapi.GroupShards(ggufs)
	for _, g := range groups {
		v := Variant{Quantization: quantLabel(g.Base), Shards: len(g.Files), File: g.Files[0].Path}
		for _, f := range g.Files {
			v.Files = append(v.Files, f.Path)
			v.SizeBytes += byPath[f.Path].Size
		}
		r.Variants = append(r.Variants, v)
	}
	sort.SliceStable(r.Variants, func(a, b int) bool {
		if r.Variants[a].SizeBytes != r.Variants[b].SizeBytes {
			return r.Variants[a].SizeBytes < r.Variants[b].SizeBytes
		}
		return r.Variants[a].Quantization < r.Variants[b].Quantization
	})
	for k := range r.Variants {
		r.Variants[k].Preference = i.preferenceFor(groups, groupBase(groups, r.Variants[k].File), r.Variants[k].Quantization)
	}
	markRecommended(r.Variants)
	r.SizeKnown = false
	for _, f := range r.Files {
		if f.Size > 0 {
			r.SizeKnown = true
			break
		}
	}
}

func groupBase(groups []hfapi.ShardGroup, first string) string {
	for _, g := range groups {
		if g.Files[0].Path == first {
			return g.Base
		}
	}
	return path.Base(first)
}

// preferenceFor returns the shortest preferences.quantizations value that
// selects exactly one file group: the quantization label when it does, the
// whole file name otherwise.
func (i *Inspector) preferenceFor(groups []hfapi.ShardGroup, base, label string) string {
	count := func(pref string) int {
		n := 0
		for _, g := range groups {
			if i.opt.QuantMatches(g.Base, pref) {
				n++
			}
		}
		return n
	}
	if label != "" && count(label) == 1 && i.opt.QuantMatches(base, label) {
		return strings.ToLower(label)
	}
	whole := strings.TrimSuffix(strings.ToLower(base), ".gguf")
	if i.opt.QuantMatches(base, whole) && count(whole) == 1 {
		return whole
	}
	return ""
}

var recommendedOrder = []string{"q4_k_m", "ud-q4_k_xl", "q4_k_xl", "q4_k_s", "q4_0", "q5_k_m", "q8_0"}

func markRecommended(vs []Variant) {
	for _, want := range recommendedOrder {
		for k := range vs {
			if strings.EqualFold(vs[k].Quantization, want) {
				vs[k].Recommended = true
				return
			}
		}
	}
	if len(vs) > 0 {
		vs[0].Recommended = true
	}
}

var quantRe = regexp.MustCompile(`(?i)(?:^|[-_.])((?:ud-)?(?:iq\d+(?:_[a-z0-9]+)*|q\d+(?:_[a-z0-9]+)*|tq\d_\d|bf16|f16|f32|fp16|fp32|mxfp4(?:_moe)?|nvfp4))(?:$|[-_.])`)

// quantLabel reads the quantization from a (shard-collapsed) GGUF file name:
// "Qwen3-8B-UD-Q4_K_XL.gguf" gives "UD-Q4_K_XL". A name with no known
// quantization gives its base name.
func quantLabel(base string) string {
	name := strings.TrimSuffix(path.Base(base), path.Ext(base))
	all := quantRe.FindAllStringSubmatch(name, -1)
	if len(all) == 0 {
		return name
	}
	return strings.ToUpper(all[len(all)-1][1])
}

var (
	fenceRe   = regexp.MustCompile("(?s)```.*?```")
	htmlRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	imageRe   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkRe    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	emphasisR = regexp.MustCompile("[*_`]{1,3}")
)

// readmeSummary returns the first paragraph of prose of a model card: front
// matter, headings, badges, HTML and code blocks are skipped. It is cut at a
// word boundary to at most 400 characters.
func readmeSummary(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if _, after, found := strings.Cut(rest, "\n---"); found {
			text = after
		}
	}
	text = fenceRe.ReplaceAllString(text, "")
	text = htmlRe.ReplaceAllString(text, "")
	text = imageRe.ReplaceAllString(text, "")
	text = linkRe.ReplaceAllString(text, "$1")
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" || strings.HasPrefix(para, "#") || strings.HasPrefix(para, "|") || strings.HasPrefix(para, ">") || strings.HasPrefix(para, "---") {
			continue
		}
		para = emphasisR.ReplaceAllString(para, "")
		para = strings.Join(strings.Fields(para), " ")
		if len(para) < 20 {
			continue
		}
		if len(para) > 400 {
			cut := strings.LastIndex(para[:400], " ")
			if cut < 200 {
				cut = 400
			}
			para = para[:cut] + "..."
		}
		return para
	}
	return ""
}

// readme reads the first 4 KiB of the model card at a commit.
func (i *Inspector) readme(ctx context.Context, id, rev string) string {
	if rev == "" {
		rev = "main"
	}
	resp, err := i.hfGet(ctx, i.opt.HFBase+"/"+id+"/resolve/"+url.PathEscape(rev)+"/README.md", "bytes=0-4095")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	return readmeSummary(string(b))
}

// VariantEstimate is the estimate of one quantization.
type VariantEstimate struct {
	File         string `json:"file"`
	Quantization string `json:"quantization"`
	SizeBytes    uint64 `json:"sizeBytes"`
	SizeDisplay  string `json:"sizeDisplay"`
	// Estimates is keyed by context size, like the gallery estimate.
	Estimates map[string]vram.VRAMAt `json:"estimates"`
}

// EstimateResult is the gallery estimate plus what was estimated.
type EstimateResult struct {
	vram.MultiContextEstimate
	// File is the first file of the variant that was estimated; empty when
	// the whole repository was.
	File         string `json:"file,omitempty"`
	Quantization string `json:"quantization,omitempty"`
	// Source says where the architecture came from: "gguf-header" when the
	// header of the file was read (layers, width and heads are exact),
	// "file-size" when only the file size was known (the KV cache term is a
	// rule of thumb) and "unknown" when no size was known at all.
	Source string `json:"source"`
	// Variants has the same estimate for every quantization of a GGUF
	// repository. Quantizations of one model share an architecture, so the
	// header read for the chosen file serves them all: one upstream read
	// answers the whole variant list.
	Variants []VariantEstimate `json:"variants,omitempty"`
}

// Estimate returns the VRAM estimate of one file group of a repository at
// the given context sizes. file is a variant's first file as listed by
// Files; empty selects the recommended variant (a GGUF repository) or all
// weight files (any other).
func (i *Inspector) Estimate(ctx context.Context, uri, file string, contexts []uint32) (*EstimateResult, error) {
	rf, err := i.Files(ctx, uri)
	if err != nil {
		return nil, err
	}
	var files []RepoFile
	res := &EstimateResult{File: file}
	gguf := len(rf.Variants) > 0
	if gguf {
		var v *Variant
		for k := range rf.Variants {
			if (file == "" && rf.Variants[k].Recommended) || (file != "" && containsString(rf.Variants[k].Files, file)) {
				v = &rf.Variants[k]
				break
			}
		}
		if v == nil {
			return nil, fmt.Errorf("%w: %s", ErrNoSuchFile, file)
		}
		res.File, res.Quantization = v.File, v.Quantization
	} else {
		for _, f := range rf.Files {
			if file != "" && f.Path != file {
				continue
			}
			if f.Kind != "mmproj" {
				files = append(files, f)
			}
		}
		if file != "" && len(files) == 0 {
			return nil, fmt.Errorf("%w: %s", ErrNoSuchFile, file)
		}
	}
	var size uint64
	var variantSize uint64
	if gguf {
		for _, v := range rf.Variants {
			if v.File == res.File {
				variantSize = uint64(v.SizeBytes)
			}
		}
		size = variantSize
	} else {
		for _, f := range files {
			size += uint64(f.Size)
		}
	}
	if size == 0 {
		res.MultiContextEstimate = vram.MultiContextEstimate{Estimates: map[string]vram.VRAMAt{}}
		res.Source = "unknown"
		return res, nil
	}

	var meta *vram.GGUFMeta
	if gguf {
		if err := i.acquire(ctx); err != nil {
			return nil, err
		}
		meta, _ = i.opt.ReadMeta(ctx, fileURL(rf, res.File))
		i.release()
	}
	res.MultiContextEstimate = vram.EstimateFromSize(size, gguf, meta, contexts, vram.EstimateOptions{})
	res.Source = "file-size"
	if !res.Approximate {
		res.Source = "gguf-header"
	}
	if gguf {
		for _, v := range rf.Variants {
			if v.SizeBytes <= 0 {
				continue
			}
			e := vram.EstimateFromSize(uint64(v.SizeBytes), true, meta, contexts, vram.EstimateOptions{})
			res.Variants = append(res.Variants, VariantEstimate{
				File: v.File, Quantization: v.Quantization,
				SizeBytes: e.SizeBytes, SizeDisplay: e.SizeDisplay, Estimates: e.Estimates,
			})
		}
	}
	return res, nil
}

func fileURL(rf *RepoFiles, p string) string {
	rev := rf.Revision
	if rev == "" {
		rev = "main"
	}
	segs := strings.Split(p, "/")
	for k := range segs {
		segs[k] = url.PathEscape(segs[k])
	}
	return rf.hfBase + "/" + rf.ID + "/resolve/" + url.PathEscape(rev) + "/" + strings.Join(segs, "/")
}

func containsString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
