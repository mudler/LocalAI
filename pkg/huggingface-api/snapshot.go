package hfapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

const defaultMaxSnapshotFiles = 100_000

var immutableRevisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type SnapshotRequest struct {
	Repo           string
	Revision       string
	Token          string
	AllowPatterns  []string
	IgnorePatterns []string
	MaxFiles       int
}

type Snapshot struct {
	Endpoint          string
	Repo              string
	RequestedRevision string
	ResolvedRevision  string
	Files             []SnapshotFile
}

type SnapshotFile struct {
	Path    string
	Size    int64
	BlobOID string
	LFSOID  string
	XetHash string
	URL     string
}

func FilterSnapshotFiles(files []SnapshotFile, allow, ignore []string) ([]SnapshotFile, error) {
	selected := make([]SnapshotFile, 0, len(files))
	for _, file := range files {
		allowed, err := matchesAny(file.Path, allow)
		if err != nil {
			return nil, err
		}
		if len(allow) > 0 && !allowed {
			continue
		}
		ignored, err := matchesAny(file.Path, ignore)
		if err != nil {
			return nil, err
		}
		if ignored {
			continue
		}
		selected = append(selected, file)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Path < selected[j].Path })
	return selected, nil
}

func matchesAny(filePath string, patterns []string) (bool, error) {
	for _, pattern := range patterns {
		matched, err := path.Match(pattern, filePath)
		if err != nil {
			return false, fmt.Errorf("invalid Hub file pattern %q: %w", pattern, err)
		}
		if !matched && !strings.Contains(pattern, "/") {
			matched, err = path.Match(pattern, path.Base(filePath))
			if err != nil {
				return false, fmt.Errorf("invalid Hub file pattern %q: %w", pattern, err)
			}
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

type revisionResponse struct {
	SHA string `json:"sha"`
}

type snapshotTreeItem struct {
	Type    string   `json:"type"`
	OID     string   `json:"oid"`
	Size    int64    `json:"size"`
	Path    string   `json:"path"`
	LFS     *LFSInfo `json:"lfs,omitempty"`
	XetHash string   `json:"xetHash,omitempty"`
}

func (c *Client) ResolveSnapshot(ctx context.Context, req SnapshotRequest) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if req.MaxFiles <= 0 {
		req.MaxFiles = defaultMaxSnapshotFiles
	}
	endpoint := strings.TrimSuffix(c.baseURL, "/api/models")
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Snapshot{}, fmt.Errorf("invalid Hugging Face endpoint")
	}
	endpoint = strings.TrimRight(parsed.String(), "/")
	owner, repo, err := splitRepo(req.Repo)
	if err != nil {
		return Snapshot{}, err
	}
	revision := req.Revision
	if revision == "" {
		revision = "main"
	}
	metadataURL := fmt.Sprintf("%s/api/models/%s/%s/revision/%s", endpoint, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(revision))
	metadataReq, err := c.newRequest(ctx, http.MethodGet, metadataURL, req.Token)
	if err != nil {
		return Snapshot{}, err
	}
	var metadata revisionResponse
	if err := c.decodeJSON(metadataReq, &metadata); err != nil {
		return Snapshot{}, fmt.Errorf("resolve Hugging Face revision: %w", err)
	}
	if !immutableRevisionPattern.MatchString(metadata.SHA) {
		return Snapshot{}, fmt.Errorf("Hub returned invalid immutable revision")
	}
	resolvedRevision := strings.ToLower(metadata.SHA)

	treeURL := fmt.Sprintf("%s/api/models/%s/%s/tree/%s?recursive=true&expand=true", endpoint, url.PathEscape(owner), url.PathEscape(repo), resolvedRevision)
	items := make([]snapshotTreeItem, 0)
	for treeURL != "" {
		treeReq, err := c.newRequest(ctx, http.MethodGet, treeURL, req.Token)
		if err != nil {
			return Snapshot{}, err
		}
		page, next, err := c.decodeTreePage(treeReq)
		if err != nil {
			return Snapshot{}, err
		}
		if len(items)+len(page) > req.MaxFiles {
			return Snapshot{}, fmt.Errorf("snapshot exceeds maximum file count %d", req.MaxFiles)
		}
		items = append(items, page...)
		treeURL = next
	}

	files := make([]SnapshotFile, 0, len(items))
	for _, item := range items {
		if item.Type != "file" {
			continue
		}
		file := SnapshotFile{Path: item.Path, Size: item.Size, BlobOID: item.OID, XetHash: item.XetHash}
		if item.LFS != nil {
			file.LFSOID = item.LFS.Oid
		}
		file.URL = fmt.Sprintf("%s/%s/%s/resolve/%s/%s", endpoint, url.PathEscape(owner), url.PathEscape(repo), resolvedRevision, escapeFilePath(item.Path))
		files = append(files, file)
	}
	files, err = FilterSnapshotFiles(files, req.AllowPatterns, req.IgnorePatterns)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Endpoint: endpoint, Repo: req.Repo, RequestedRevision: revision, ResolvedRevision: resolvedRevision, Files: files}, nil
}

func splitRepo(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("Hugging Face repo must be owner/repo")
	}
	return parts[0], parts[1], nil
}

func escapeFilePath(filePath string) string {
	parts := strings.Split(filePath, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func (c *Client) decodeJSON(req *http.Request, dst any) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Hub request returned status %d", resp.StatusCode)
	}
	return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(dst)
}

func (c *Client) decodeTreePage(req *http.Request) ([]snapshotTreeItem, string, error) {
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Hub tree request returned status %d", resp.StatusCode)
	}
	var page []snapshotTreeItem
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 32<<20)).Decode(&page); err != nil {
		return nil, "", err
	}
	next, err := nextPage(resp.Header.Get("Link"), req.URL)
	if err != nil {
		return nil, "", err
	}
	return page, next, nil
}

func nextPage(link string, current *url.URL) (string, error) {
	for entry := range strings.SplitSeq(link, ",") {
		parts := strings.Split(entry, ";")
		if len(parts) >= 2 && strings.TrimSpace(parts[1]) == `rel="next"` {
			raw := strings.Trim(strings.TrimSpace(parts[0]), "<>")
			next, err := url.Parse(raw)
			if err != nil {
				return "", fmt.Errorf("invalid Hub pagination link: %w", err)
			}
			if !next.IsAbs() {
				next = current.ResolveReference(next)
			}
			if next.Scheme != current.Scheme || next.Host != current.Host {
				return "", fmt.Errorf("cross-origin pagination link rejected")
			}
			return next.String(), nil
		}
	}
	return "", nil
}
