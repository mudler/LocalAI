package gallery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"gopkg.in/yaml.v3"
)

// digestEntry is the part of a gallery entry that changes what an install
// writes, as a map for the canonical JSON (encoding/json sorts map keys).
// Description, icon, license, tags, size and urls are left out: a change to
// them is not an update. Variants are the declared variant names, sorted,
// because the list is unordered; the build a variant installs is part of the
// digest when it is the installed one (see EntryDigest).
func digestEntry(m *GalleryModel, withVariants bool) map[string]any {
	out := map[string]any{}
	if m.URL != "" {
		out["url"] = m.URL
	}
	if len(m.ConfigFile) > 0 {
		out["config_file"] = normalizeJSON(m.ConfigFile)
	}
	if len(m.AdditionalFiles) > 0 {
		files := make([]any, len(m.AdditionalFiles))
		for i, f := range m.AdditionalFiles {
			files[i] = map[string]any{"filename": f.Filename, "sha256": strings.ToLower(f.SHA256), "uri": f.URI}
		}
		out["files"] = files
	}
	if len(m.Overrides) > 0 {
		out["overrides"] = normalizeJSON(m.Overrides)
	}
	if withVariants && len(m.Variants) > 0 {
		names := make([]string, 0, len(m.Variants))
		for _, v := range m.Variants {
			names = append(names, v.Model)
		}
		slices.Sort(names)
		out["variants"] = names
	}
	return out
}

// EntryDigest identifies what installing a gallery entry writes. It is the
// SHA-256 of the canonical JSON of the install-affecting fields of the entry
// the person picked (parent), of the build it resolved to (resolved, when that
// is a variant), and of the bytes of the base config the build points at
// (baseConfigSHA256, see BaseConfigFetcher). The base config carries the
// prompt templates, so a template change is a new digest; so is a sha256 the
// checksum bot rewrote, and a config_file or overrides change. A change to
// the description or another presentation field is not.
//
// The format is "sha256:<hex>". Two LocalAI releases must compute the same
// digest for the same entry, or every model would show an update after an
// upgrade of LocalAI: digest_test.go pins golden values.
func EntryDigest(parent, resolved *GalleryModel, baseConfigSHA256 string) string {
	if parent == nil {
		return ""
	}
	in := map[string]any{"entry": digestEntry(parent, true)}
	if resolved != nil && resolved != parent {
		// The resolved copy carries the parent's name and presentation, and
		// no variant list; only its payload is part of the identity.
		in["resolved"] = digestEntry(resolved, false)
	}
	if baseConfigSHA256 != "" {
		in["base_config_sha256"] = baseConfigSHA256
	}
	data, err := json.Marshal(in)
	if err != nil {
		// The input is maps, lists and scalars decoded from YAML.
		return ""
	}
	return "sha256:" + SHA256Hex(data)
}

// fetchGalleryConfigBytes reads the bytes of a gallery config url, with the
// same SSRF guard and read root as GetGalleryConfigFromURLWithContext.
func fetchGalleryConfigBytes(ctx context.Context, url, basePath string) ([]byte, error) {
	if err := validateGalleryConfigURL(url); err != nil {
		return nil, err
	}
	var body []byte
	err := downloader.URI(url).ReadWithAuthorizationAndCallback(ctx, galleryConfigReadRoot(url, basePath), "", func(_ string, d []byte) error {
		body = d
		return nil
	})
	return body, err
}

// galleryConfigWithHash fetches a base config and returns it parsed, with the
// hash of its bytes.
func galleryConfigWithHash(ctx context.Context, url, basePath string) (ModelConfig, string, error) {
	var config ModelConfig
	body, err := fetchGalleryConfigBytes(ctx, url, basePath)
	if err != nil {
		return config, "", fmt.Errorf("fetch gallery config %q: %w", url, err)
	}
	if err := yaml.Unmarshal(body, &config); err != nil {
		return config, "", fmt.Errorf("parse gallery config %q: %w", url, err)
	}
	return config, SHA256Hex(body), nil
}

// BaseConfigFetcher fetches the base config of gallery entries for the update
// check, with a cache keyed by url. An http(s) url (a github: url resolves to
// one) is asked again with If-None-Match when the server gave an ETag, so an
// unchanged config costs a 304. Other urls (local files, OCI galleries) are
// read every time; they are local or already cached by the gallery code.
//
// Distributed mode: the cache is per frontend and only saves requests; a cold
// cache gives the same answer.
type BaseConfigFetcher struct {
	mu      sync.Mutex
	entries map[string]fetchedConfig
	client  *http.Client
	// validate guards a url against SSRF before the request. Tests replace it
	// to reach a local server.
	validate func(string) error
}

// newConfigHTTPClient builds the client of the fetcher. A gallery entry url
// comes from outside, so the client resolves the host itself and refuses a
// private target, on the first request and on every redirect hop. extra
// options let a test reach a local server.
func newConfigHTTPClient(extra ...httpclient.Option) *http.Client {
	opts := append([]httpclient.Option{httpclient.WithFollowRedirects(), httpclient.WithExternalOnly()}, extra...)
	return httpclient.NewWithTimeout(30*time.Second, opts...)
}

// maxGalleryConfigBytes is the size a fetched gallery config may have.
const maxGalleryConfigBytes = 16 << 20

type fetchedConfig struct {
	etag string
	body []byte
}

// NewBaseConfigFetcher returns a fetcher with an empty cache.
func NewBaseConfigFetcher() *BaseConfigFetcher {
	return &BaseConfigFetcher{
		entries:  map[string]fetchedConfig{},
		client:   newConfigHTTPClient(),
		validate: validateGalleryConfigURL,
	}
}

// Fetch returns the bytes of the base config at url and their hash.
func (f *BaseConfigFetcher) Fetch(ctx context.Context, url, basePath string) ([]byte, string, error) {
	resolved := downloader.URI(url).ResolveURL()
	if !strings.HasPrefix(resolved, "http://") && !strings.HasPrefix(resolved, "https://") {
		body, err := fetchGalleryConfigBytes(ctx, url, basePath)
		if err != nil {
			return nil, "", err
		}
		return body, SHA256Hex(body), nil
	}
	if err := f.validate(resolved); err != nil {
		return nil, "", err
	}
	f.mu.Lock()
	cached, ok := f.entries[resolved]
	f.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved, nil)
	if err != nil {
		return nil, "", err
	}
	if ok && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetch gallery config %q: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified && ok {
		return cached.body, SHA256Hex(cached.body), nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch gallery config %q: status %d", url, resp.StatusCode)
	}
	// Read one byte more than the limit: a config that long would be cut
	// silently, and its digest would be the digest of a part.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGalleryConfigBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("fetch gallery config %q: %w", url, err)
	}
	if int64(len(body)) > maxGalleryConfigBytes {
		return nil, "", fmt.Errorf("fetch gallery config %q: larger than %d bytes", url, int64(maxGalleryConfigBytes))
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		f.mu.Lock()
		f.entries[resolved] = fetchedConfig{etag: etag, body: body}
		f.mu.Unlock()
	}
	return body, SHA256Hex(body), nil
}

// ForgetGalleryIndexes drops the in-memory copies of the gallery indexes, so
// the next listing reads every index again. An explicit update check calls it:
// the copies live an hour, and a change the checksum bot merged a minute ago
// must be seen.
func ForgetGalleryIndexes() {
	for _, key := range galleryCache.Keys() {
		galleryCache.Delete(key)
	}
}

// InstalledEntry finds, in a gallery listing, the entry an installed model
// came from and the build it resolved to, as an install target (for an entry
// with variants, a copy named as the entry, the way the install resolved it).
// resolvedVariant is the variant the gallery file recorded; when the entry no
// longer declares it, the entry's own build is used. variant is the name of
// the build, empty for an entry without variants. parent is nil when the
// gallery no longer lists the entry.
func InstalledEntry(models []*GalleryModel, galleryName, entryName, resolvedVariant string) (parent, resolved *GalleryModel, variant string) {
	id := entryName
	if galleryName != "" {
		id = galleryName + "@" + entryName
	}
	parent = FindGalleryElement(models, id)
	if parent == nil {
		return nil, nil, ""
	}
	if !parent.HasVariants() {
		return parent, parent, ""
	}
	source, variant := parent, parent.Name
	if resolvedVariant != "" && resolvedVariant != parent.Name {
		for _, v := range parent.Variants {
			if v.Model != resolvedVariant {
				continue
			}
			if target := FindGalleryElement(models, v.Model); target != nil {
				source, variant = target, v.Model
			}
			break
		}
	}
	return parent, variantInstallTarget(parent, source), variant
}

// CurrentEntryDigest computes EntryDigest for an entry as the gallery lists it
// now, fetching the base config of its build with fetch. It returns the digest
// and the hash of the base config.
func CurrentEntryDigest(ctx context.Context, fetch *BaseConfigFetcher, basePath string, parent, resolved *GalleryModel) (string, string, error) {
	baseSHA := ""
	if resolved.URL != "" {
		_, sha, err := fetch.Fetch(ctx, resolved.URL, basePath)
		if err != nil {
			return "", "", err
		}
		baseSHA = sha
	}
	return EntryDigest(parent, resolved, baseSHA), baseSHA, nil
}

// PrepareReinstall resolves what installing the entry an installed model came
// from would write now: the same install name, the same request overrides and
// the same build (the variant the gallery file recorded, when the entry still
// declares it). It returns nil when the gallery no longer lists the entry. It
// fetches the base config but writes nothing.
func PrepareReinstall(ctx context.Context, models []*GalleryModel, basePath, installName string, sc *ModelConfig) (*GalleryInstall, error) {
	if sc == nil || sc.Origin == nil {
		return nil, fmt.Errorf("model %q has no recorded origin", installName)
	}
	parent, resolved, variant := InstalledEntry(models, sc.Origin.Gallery, sc.Origin.EntryName, sc.ResolvedVariant)
	if parent == nil {
		return nil, nil
	}
	// Work on copies: the install merges the request overrides into the
	// target in place, and the listing may be a cached one.
	parentCopy := *parent
	parentCopy.Overrides = deepCopyStringMap(parent.Overrides)
	target := &parentCopy
	var record *ModelConfig
	if parent.HasVariants() {
		target = resolved
		record = &ModelConfig{EntryName: parent.Name, ResolvedVariant: variant, PinnedVariant: sc.PinnedVariant}
	}
	req := GalleryModel{Overrides: deepCopyStringMap(sc.Origin.RequestOverrides)}
	req.Name = installName
	inst, err := buildGalleryInstall(ctx, basePath, &parentCopy, target, record, req)
	if err != nil {
		return nil, err
	}
	return inst, nil
}
