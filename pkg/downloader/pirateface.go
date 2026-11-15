package downloader

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
)

// PirateFaceEndpoint is the Pirate Face (https://pirateface.co) site that
// pirateface:// URIs and page URLs resolve against. Pirate Face indexes
// open-weight models and publishes BitTorrent magnet links for them; it does
// not host the files. The CLI option --pirateface-url (env LOCALAI_PIRATEFACE_URL)
// points it at another instance.
var PirateFaceEndpoint = DefaultPirateFaceEndpoint

// DefaultPirateFaceEndpoint is the public Pirate Face site.
const DefaultPirateFaceEndpoint = "https://pirateface.co"

// ErrPirateFaceNotListed means Pirate Face has no torrent for the repository.
var ErrPirateFaceNotListed = errors.New("model is not listed on Pirate Face")

// ErrPirateFaceNoMagnet means Pirate Face lists the repository but shows no
// magnet link for it. That is the case for a gated-licence model until the
// terms are accepted on the site; LocalAI does not work around it.
var ErrPirateFaceNoMagnet = errors.New("Pirate Face shows no magnet link for this model (gated licences must be accepted on the site first)")

// maxAPIResponseBytes bounds every JSON or .torrent body read from the index.
const maxAPIResponseBytes = 16 << 20

// PirateFaceFile is one file of a Pirate Face listing.
type PirateFaceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// PirateFaceQuant is one torrent of a repository. A repository can have
// several, each with its own file set and Hugging Face revision.
type PirateFaceQuant struct {
	Quant    string           `json:"quant"`
	Revision string           `json:"revision"`
	Files    []PirateFaceFile `json:"files"`
}

// PirateFaceEntry is the answer of GET /api/torrents?repo=<owner>/<repo>.
type PirateFaceEntry struct {
	RepoID   string            `json:"repoId"`
	Revision string            `json:"revision"`
	Infohash string            `json:"infohash"`
	Magnet   string            `json:"magnet"`
	Torrent  string            `json:"torrent"`
	HF       *bool             `json:"hf"`
	Quant    string            `json:"quant"`
	Files    []PirateFaceFile  `json:"files"`
	Quants   []PirateFaceQuant `json:"quants"`
}

// LookupPirateFace asks the index for a repository. quant selects one of the
// repository's torrents; empty takes the default.
func LookupPirateFace(ctx context.Context, repoID, quant string) (*PirateFaceEntry, error) {
	q := url.Values{"repo": {repoID}}
	if quant != "" {
		q.Set("quant", quant)
	}
	body, status, err := pirateFaceGet(ctx, "/api/torrents?"+q.Encode())
	if err != nil {
		return nil, asTransient(err)
	}
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrPirateFaceNotListed, repoID)
	}
	if status >= 400 {
		err := fmt.Errorf("Pirate Face lookup of %s: status %d", repoID, status)
		if status >= 500 || status == http.StatusTooManyRequests {
			return nil, asTransient(err)
		}
		return nil, err
	}
	var entry PirateFaceEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("Pirate Face lookup of %s: bad answer: %w", repoID, err)
	}
	return &entry, nil
}

// PirateFaceFiles lists every file Pirate Face knows for a repository, across
// all of its torrents. The first torrent that lists a path wins.
func PirateFaceFiles(ctx context.Context, repoID string) ([]PirateFaceFile, error) {
	entry, err := LookupPirateFace(ctx, repoID, "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []PirateFaceFile
	add := func(files []PirateFaceFile) {
		for _, f := range files {
			if f.Path == "" || seen[f.Path] || checkRelativePath(f.Path) != nil {
				continue
			}
			seen[f.Path] = true
			out = append(out, f)
		}
	}
	add(entry.Files)
	for _, q := range entry.Quants {
		add(q.Files)
	}
	return out, nil
}

// resolvePirateFaceFile finds the torrent that carries one file of a
// repository and returns its entry together with the file's listing.
func resolvePirateFaceFile(ctx context.Context, ref PirateFaceRef) (*PirateFaceEntry, PirateFaceFile, error) {
	if ref.Path == "" {
		return nil, PirateFaceFile{}, fmt.Errorf("%s names a whole repository: add the file, as in %s<owner>/<repo>/<file>", ref.URI(), PirateFacePrefix)
	}
	entry, err := LookupPirateFace(ctx, ref.RepoID(), "")
	if err != nil {
		return nil, PirateFaceFile{}, err
	}
	find := func(files []PirateFaceFile) (PirateFaceFile, bool) {
		for _, f := range files {
			if f.Path == ref.Path {
				return f, true
			}
		}
		return PirateFaceFile{}, false
	}
	if f, ok := find(entry.Files); ok {
		return entry, f, nil
	}
	for _, q := range entry.Quants {
		f, ok := find(q.Files)
		if !ok || q.Quant == entry.Quant {
			continue
		}
		other, err := LookupPirateFace(ctx, ref.RepoID(), q.Quant)
		if err != nil {
			return nil, PirateFaceFile{}, err
		}
		return other, f, nil
	}
	return nil, PirateFaceFile{}, fmt.Errorf("%s is not in the Pirate Face listing of %s", ref.Path, ref.RepoID())
}

func pirateFaceGet(ctx context.Context, p string) ([]byte, int, error) {
	req, err := newDownloadRequest(ctx, http.MethodGet, PirateFaceEndpoint+p, "")
	if err != nil {
		return nil, 0, err
	}
	resp, err := downloadHTTPClient().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("Pirate Face request %s: %w", p, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("Pirate Face request %s: %w", p, err)
	}
	return body, resp.StatusCode, nil
}

var wsPathRe = regexp.MustCompile(`^/api/ws/([^/]+)/([^/]+)/([0-9a-f]{40})/?$`)

// pirateFaceWebSeed recognises a Pirate Face web seed URL
// (<endpoint>/api/ws/<owner>/<repo>/<revision>/) and returns the repository
// and the Hugging Face revision it mirrors.
func pirateFaceWebSeed(ws string) (owner, repo, revision string, ok bool) {
	u, err := url.Parse(ws)
	if err != nil {
		return "", "", "", false
	}
	endpoint, err := url.Parse(PirateFaceEndpoint)
	if err != nil || !strings.EqualFold(u.Host, endpoint.Host) {
		return "", "", "", false
	}
	m := wsPathRe.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}
