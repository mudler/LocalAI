// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/github/internal/ghapi"
)

// contentsPath is the API path of a file or directory.
func contentsPath(repo, path string) string {
	p := "/repos/" + repo + "/contents"
	if path != "" {
		segs := strings.Split(strings.TrimSuffix(path, "/"), "/")
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		p += "/" + strings.Join(segs, "/")
	}
	return p
}

type searchArgs struct {
	Repo    string `json:"repo"`
	Query   string `json:"query"`
	Path    string `json:"path"`
	Page    int    `json:"page"`
	PerPage int    `json:"perPage"`
}

func (c call) searchCode(raw map[string]json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	if err := checkQuery(a.Query); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, true); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(a.Query) + " repo:" + a.Repo
	if a.Path != "" {
		q += " path:" + a.Path
	}
	page, per := pageOr(a.Page, 1), perPageOr(a.PerPage, 30)
	var out struct {
		Total int `json:"total_count"`
		Items []struct {
			Path        string `json:"path"`
			HTMLURL     string `json:"html_url"`
			TextMatches []struct {
				Fragment string `json:"fragment"`
			} `json:"text_matches"`
		} `json:"items"`
	}
	resp, err := c.r.API.Get(c.ctx, c.token, "/search/code", url.Values{"q": {q}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(per)}}, "application/vnd.github.text-match+json")
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(resp.Body, &out) != nil {
		return nil, ghapi.ErrUnavailable
	}
	type hit struct {
		Path    string   `json:"path"`
		URL     string   `json:"url"`
		Matches []string `json:"matches,omitempty"`
	}
	hits := make([]hit, 0, len(out.Items))
	for _, it := range out.Items {
		h := hit{Path: it.Path, URL: it.HTMLURL}
		for _, m := range it.TextMatches {
			if len(h.Matches) == 3 {
				break
			}
			frag, _ := cut(m.Fragment, 300)
			h.Matches = append(h.Matches, frag)
		}
		hits = append(hits, h)
	}
	res := map[string]any{"repo": a.Repo, "query": a.Query, "total": out.Total, "page": page, "results": hits}
	if resp.NextPage() {
		res["next"] = page + 1
		res["note"] = fmt.Sprintf("more results: ask for page=%d", page+1)
	}
	return res, nil
}

type readArgs struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Ref    string `json:"ref"`
	Offset int64  `json:"offset"`
}

func (c call) readFile(raw map[string]json.RawMessage) (any, error) {
	var a readArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, false); err != nil {
		return nil, err
	}
	if err := checkRef(a.Ref); err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, bad("offset is not negative")
	}
	q := url.Values{}
	if a.Ref != "" {
		q.Set("ref", a.Ref)
	}
	resp, err := c.r.API.Get(c.ctx, c.token, contentsPath(a.Repo, a.Path), q, "")
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(bytes.TrimSpace(resp.Body), []byte("[")) {
		return nil, bad("path is a directory: use list-tree")
	}
	var meta struct {
		Type     string `json:"type"`
		SHA      string `json:"sha"`
		Size     int64  `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if json.Unmarshal(resp.Body, &meta) != nil {
		return nil, ghapi.ErrUnavailable
	}
	if meta.Type != "file" {
		return nil, bad("path is not a file")
	}
	var content []byte
	if meta.Encoding == "base64" {
		content, err = base64.StdEncoding.DecodeString(strings.NewReplacer("\n", "", "\r", "").Replace(meta.Content))
		if err != nil {
			return nil, ghapi.ErrUnavailable
		}
	} else {
		// A file above 1 MiB has no inline content: ask for the raw bytes.
		full, err := c.r.API.Get(c.ctx, c.token, contentsPath(a.Repo, a.Path), q, "application/vnd.github.raw+json")
		if err != nil {
			return nil, err
		}
		content = full.Body
	}
	return fileResult(a, meta.SHA, int64(len(content)), content), nil
}

// fileResult cuts content to what one result carries and says what it cut.
func fileResult(a readArgs, sha string, size int64, content []byte) map[string]any {
	res := map[string]any{"repo": a.Repo, "path": a.Path, "sha": sha, "size": size}
	if a.Ref != "" {
		res["ref"] = a.Ref
	}
	head := content
	if len(head) > 8<<10 {
		head = head[:8<<10]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		res["binary"] = true
		res["note"] = "binary file: no content returned"
		return res
	}
	if a.Offset >= size {
		res["offset"], res["returned"], res["content"] = a.Offset, 0, ""
		if a.Offset > size {
			res["note"] = fmt.Sprintf("offset %d is past the end of the file (%d bytes)", a.Offset, size)
		}
		return res
	}
	start := int(a.Offset)
	for start < len(content) && !utf8.RuneStart(content[start]) {
		start++
	}
	budget := MaxFileBytes
	for {
		end := min(start+budget, len(content))
		for end < len(content) && end > start && !utf8.RuneStart(content[end]) {
			end--
		}
		text := string(content[start:end])
		if !utf8.ValidString(text) {
			text = strings.ToValidUTF8(text, "�")
		}
		res["offset"], res["returned"], res["content"] = start, end-start, text
		if end < len(content) {
			res["truncated"] = true
			res["nextOffset"] = end
			res["note"] = fmt.Sprintf("cut at %d of %d bytes; ask for offset=%d to continue", end, size, end)
		}
		// The JSON of the text can be larger than the text: shrink until it fits.
		if b, _ := json.Marshal(res); len(b) <= MaxPayload-1024 || budget < 1024 {
			return res
		}
		budget = budget * 3 / 4
		delete(res, "truncated")
		delete(res, "nextOffset")
		delete(res, "note")
	}
}

type treeArgs struct {
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Ref       string `json:"ref"`
	Recursive bool   `json:"recursive"`
	Cursor    int    `json:"cursor"`
	Limit     int    `json:"limit"`
}

type entry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

func (c call) listTree(raw map[string]json.RawMessage) (any, error) {
	var a treeArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.repo(a.Repo); err != nil {
		return nil, err
	}
	if err := checkPath(a.Path, true); err != nil {
		return nil, err
	}
	if err := checkRef(a.Ref); err != nil {
		return nil, err
	}
	if a.Cursor < 0 {
		return nil, bad("cursor is not negative")
	}
	var entries []entry
	truncatedByGitHub := false
	if a.Recursive {
		ref := a.Ref
		if ref == "" {
			var repo struct {
				DefaultBranch string `json:"default_branch"`
			}
			if _, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo, nil, &repo); err != nil {
				return nil, err
			}
			ref = repo.DefaultBranch
		}
		var tree struct {
			Tree []struct {
				Path string `json:"path"`
				Type string `json:"type"`
				Size int64  `json:"size"`
			} `json:"tree"`
			Truncated bool `json:"truncated"`
		}
		if _, err := c.r.API.GetJSON(c.ctx, c.token, "/repos/"+a.Repo+"/git/trees/"+url.PathEscape(ref), url.Values{"recursive": {"1"}}, &tree); err != nil {
			return nil, err
		}
		truncatedByGitHub = tree.Truncated
		prefix := strings.TrimSuffix(a.Path, "/")
		for _, t := range tree.Tree {
			if prefix != "" && t.Path != prefix && !strings.HasPrefix(t.Path, prefix+"/") {
				continue
			}
			entries = append(entries, entry{Path: t.Path, Type: kindOf(t.Type), Size: t.Size})
		}
	} else {
		q := url.Values{}
		if a.Ref != "" {
			q.Set("ref", a.Ref)
		}
		resp, err := c.r.API.Get(c.ctx, c.token, contentsPath(a.Repo, a.Path), q, "")
		if err != nil {
			return nil, err
		}
		var list []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int64  `json:"size"`
		}
		if json.Unmarshal(resp.Body, &list) != nil {
			return nil, bad("path is a file: use read-file")
		}
		for _, t := range list {
			entries = append(entries, entry{Path: t.Path, Type: kindOf(t.Type), Size: t.Size})
		}
		sort.SliceStable(entries, func(i, j int) bool {
			if (entries[i].Type == "dir") != (entries[j].Type == "dir") {
				return entries[i].Type == "dir"
			}
			return entries[i].Path < entries[j].Path
		})
	}
	total := len(entries)
	limit := perPageOr(a.Limit, MaxEntries)
	lo := min(a.Cursor, total)
	hi := min(lo+limit, total)
	res := map[string]any{"repo": a.Repo, "path": a.Path, "recursive": a.Recursive, "total": total, "entries": entries[lo:hi]}
	if hi < total {
		res["next"] = hi
		res["note"] = fmt.Sprintf("showing %d-%d of %d; ask for cursor=%d to continue", lo+1, hi, total, hi)
	}
	if truncatedByGitHub {
		res["treeTruncated"] = true
		res["warning"] = "GitHub cut this tree: it is too large to list in full; list a subdirectory without recursive"
	}
	if res["entries"] == nil || total == 0 {
		res["entries"] = []entry{}
	}
	return res, nil
}

func kindOf(t string) string {
	switch t {
	case "tree", "dir":
		return "dir"
	case "commit", "submodule":
		return "submodule"
	case "symlink":
		return "symlink"
	}
	return "file"
}
