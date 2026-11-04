// SPDX-License-Identifier: MIT

// Package githubtest is a fake of the part of the GitHub REST API that the
// GitHub add-on reads, like slacktest for Slack. It checks the token of each
// call against the tokens it knows, can answer with rate limit headers, and
// keeps the calls so a test can see what was asked and with which token.
package githubtest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is a fake API.
type Server struct {
	*httptest.Server

	mu      sync.Mutex
	tokens  map[string]string // token -> login
	repos   map[string]*Repo  // lower-case full name
	calls   []Call
	limit   *limit
	failing map[string]int // path prefix -> status
	// dropWrites makes the fake apply a change and then cut the connection
	// before it answers, as a network failure does.
	dropWrites bool
}

// Call is one request the fake served.
type Call struct {
	Path  string
	Query string
	Token string
	// Accept is the media type the caller asked for.
	Accept string
	// Method is the HTTP method and Body the request body of a change.
	Method string
	Body   string
}

type limit struct {
	status  int
	headers map[string]string
	body    string
	times   int // 0: always
}

// Repo is a repository of the fake.
type Repo struct {
	FullName      string
	DefaultBranch string
	Private       bool
	Files         map[string][]byte // path -> content
	Issues        []*Issue
	PRs           []*PR
	// Hits answers code search: query text -> paths.
	Hits map[string][]string
}

// Issue is an issue of the fake. A pull request is also one.
type Issue struct {
	Number   int
	Title    string
	Body     string
	State    string
	User     string
	Labels   []string
	Comments []string
	IsPR     bool
	Locked   bool
	// Reason is the state_reason set when the issue was closed.
	Reason string
}

// PR is a pull request of the fake.
type PR struct {
	Number  int
	Title   string
	Body    string
	State   string
	User    string
	Base    string
	Head    string
	Files   []PRFile
	Reviews []Review
	// HeadSHA is the commit of the head; empty means "abc123".
	HeadSHA string
	// Inline are the review comments posted on the diff.
	Inline []InlineComment
}

// InlineComment is a comment on a line of a pull request.
type InlineComment struct {
	Body, Path, Side, CommitID string
	Line                       int
}

// PRFile is a changed file.
type PRFile struct {
	Name      string
	Status    string
	Additions int
	Deletions int
	Patch     string
}

// Review is a review of a pull request.
type Review struct {
	User  string
	State string
	Body  string
}

// New starts a fake API.
func New() *Server {
	s := &Server{tokens: map[string]string{}, repos: map[string]*Repo{}, failing: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// DropWrites makes the fake apply each change and then cut the connection
// without an answer.
func (s *Server) DropWrites(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropWrites = on
}

// AddToken makes a token valid for a login.
func (s *Server) AddToken(token, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = login
}

// AddRepo adds a repository and returns it for the test to fill.
func (s *Server) AddRepo(fullName string) *Repo {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &Repo{FullName: fullName, DefaultBranch: "main", Files: map[string][]byte{}, Hits: map[string][]string{}}
	s.repos[strings.ToLower(fullName)] = r
	return r
}

// Calls returns the requests served, oldest first.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Reset forgets the calls.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

// LimitPrimary makes every call answer 403 with the primary rate limit
// headers, resetting at reset.
func (s *Server) LimitPrimary(reset time.Time) {
	s.setLimit(&limit{status: 403, headers: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(reset.Unix(), 10)}, body: `{"message":"API rate limit exceeded"}`})
}

// LimitSecondary makes every call answer 403 with a retry-after wait, or with
// the secondary limit message only when wait is zero.
func (s *Server) LimitSecondary(wait int) {
	l := &limit{status: 403, headers: map[string]string{}, body: `{"message":"You have exceeded a secondary rate limit."}`}
	if wait > 0 {
		l.headers["Retry-After"] = strconv.Itoa(wait)
	}
	s.setLimit(l)
}

// Limit429 makes every call answer 429.
func (s *Server) Limit429() {
	s.setLimit(&limit{status: 429, headers: map[string]string{}, body: `{"message":"slow down"}`})
}

// Unlimit removes a rate limit.
func (s *Server) Unlimit() { s.setLimit(nil) }

func (s *Server) setLimit(l *limit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limit = l
}

// Fail makes the paths that start with prefix answer with status.
func (s *Server) Fail(prefix string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing[prefix] = status
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	var reqBody []byte
	if r.Body != nil {
		reqBody, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	s.mu.Lock()
	s.calls = append(s.calls, Call{Path: r.URL.Path, Query: r.URL.RawQuery, Token: token, Accept: r.Header.Get("Accept"), Method: r.Method, Body: string(reqBody)})
	login, ok := s.tokens[token]
	lim := s.limit
	var fail int
	for prefix, status := range s.failing {
		if strings.HasPrefix(r.URL.Path, prefix) {
			fail = status
		}
	}
	s.mu.Unlock()
	if !ok {
		jsonError(w, http.StatusUnauthorized, "Bad credentials")
		return
	}
	if lim != nil {
		for k, v := range lim.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lim.status)
		_, _ = w.Write([]byte(lim.body))
		return
	}
	if fail != 0 {
		jsonError(w, fail, "failed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case path == "user":
		writeJSON(w, map[string]any{"login": login, "id": 4242, "type": "User"})
	case path == "search/code":
		s.searchCode(w, r)
	case strings.HasPrefix(path, "repos/"):
		s.repoRoute(w, r, strings.TrimPrefix(path, "repos/"))
	default:
		jsonError(w, http.StatusNotFound, "Not Found")
	}
}

func (s *Server) repoRoute(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	s.mu.Lock()
	repo := s.repos[strings.ToLower(parts[0]+"/"+parts[1])]
	s.mu.Unlock()
	if repo == nil {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	if len(parts) == 2 {
		writeJSON(w, map[string]any{"full_name": repo.FullName, "default_branch": repo.DefaultBranch, "private": repo.Private, "archived": false,
			"permissions": map[string]bool{"pull": true, "push": false}})
		return
	}
	sub := parts[2]
	switch {
	case strings.HasPrefix(sub, "contents"):
		s.contents(w, r, repo, strings.TrimPrefix(strings.TrimPrefix(sub, "contents"), "/"))
	case strings.HasPrefix(sub, "git/trees/"):
		s.tree(w, r, repo)
	case sub == "issues" && r.Method == http.MethodPost:
		s.createIssue(w, r, repo)
	case sub == "issues":
		s.listIssues(w, r, repo)
	case strings.HasPrefix(sub, "issues/"):
		s.issue(w, r, repo, strings.TrimPrefix(sub, "issues/"))
	case strings.HasPrefix(sub, "pulls/"):
		s.pull(w, r, repo, strings.TrimPrefix(sub, "pulls/"))
	default:
		jsonError(w, http.StatusNotFound, "Not Found")
	}
}

func (s *Server) contents(w http.ResponseWriter, r *http.Request, repo *Repo, path string) {
	if body, ok := repo.Files[path]; ok {
		if strings.Contains(r.Header.Get("Accept"), "raw") {
			w.Header().Set("Content-Type", "application/vnd.github.raw+json")
			_, _ = w.Write(body)
			return
		}
		out := map[string]any{"type": "file", "name": path[strings.LastIndex(path, "/")+1:], "path": path, "sha": "sha-" + path, "size": len(body)}
		if len(body) <= 1<<20 {
			out["encoding"], out["content"] = "base64", base64.StdEncoding.EncodeToString(body)
		} else {
			out["encoding"], out["content"] = "none", ""
		}
		writeJSON(w, out)
		return
	}
	prefix := path
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]map[string]any{}
	for name, body := range repo.Files {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if i := strings.Index(rest, "/"); i >= 0 {
			seen[rest[:i]] = map[string]any{"type": "dir", "name": rest[:i], "path": prefix + rest[:i], "size": 0}
		} else {
			seen[rest] = map[string]any{"type": "file", "name": rest, "path": name, "size": len(body)}
		}
	}
	if len(seen) == 0 {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	list := make([]map[string]any, 0, len(names))
	for _, n := range names {
		list = append(list, seen[n])
	}
	writeJSON(w, list)
}

func (s *Server) tree(w http.ResponseWriter, _ *http.Request, repo *Repo) {
	var names []string
	for n := range repo.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	var list []map[string]any
	for _, n := range names {
		list = append(list, map[string]any{"path": n, "type": "blob", "size": len(repo.Files[n])})
	}
	writeJSON(w, map[string]any{"sha": "tree", "tree": list, "truncated": false})
}

func (s *Server) searchCode(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var repoName, text string
	for _, f := range strings.Fields(q) {
		if v, ok := strings.CutPrefix(f, "repo:"); ok {
			repoName = v
		} else {
			text += f + " "
		}
	}
	text = strings.TrimSpace(text)
	s.mu.Lock()
	repo := s.repos[strings.ToLower(repoName)]
	s.mu.Unlock()
	if repo == nil {
		jsonError(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	var items []map[string]any
	for _, p := range repo.Hits[text] {
		items = append(items, map[string]any{"path": p, "name": p[strings.LastIndex(p, "/")+1:], "sha": "sha-" + p, "html_url": "https://github.com/" + repo.FullName + "/blob/main/" + p,
			"text_matches": []map[string]any{{"fragment": "…" + text + "…"}}})
	}
	page, per := pageOf(r)
	lo, hi := (page-1)*per, page*per
	total := len(items)
	if lo > total {
		lo = total
	}
	if hi > total {
		hi = total
	}
	if hi < total {
		w.Header().Set("Link", `<https://api.github.com/search/code?page=`+strconv.Itoa(page+1)+`>; rel="next"`)
	}
	writeJSON(w, map[string]any{"total_count": total, "incomplete_results": false, "items": items[lo:hi]})
}

func (s *Server) listIssues(w http.ResponseWriter, r *http.Request, repo *Repo) {
	state := r.URL.Query().Get("state")
	var list []map[string]any
	for _, i := range repo.Issues {
		if state != "" && state != "all" && i.State != state {
			continue
		}
		e := issueJSON(i, false)
		e["body"] = i.Body
		list = append(list, e)
	}
	page, per := pageOf(r)
	lo, hi := (page-1)*per, page*per
	if lo > len(list) {
		lo = len(list)
	}
	if hi > len(list) {
		hi = len(list)
	}
	if hi < len(list) {
		w.Header().Set("Link", `<https://api.github.com/x?page=`+strconv.Itoa(page+1)+`>; rel="next"`)
	}
	writeJSON(w, list[lo:hi])
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request, repo *Repo, rest string) {
	num, tail, _ := strings.Cut(rest, "/")
	n, _ := strconv.Atoi(num)
	for _, i := range repo.Issues {
		if i.Number != n {
			continue
		}
		if r.Method != http.MethodGet {
			s.changeIssue(w, r, repo, i, tail)
			return
		}
		if tail == "comments" {
			var list []map[string]any
			for k, c := range i.Comments {
				list = append(list, map[string]any{"id": 1000 + k, "user": map[string]any{"login": "commenter"}, "body": c, "created_at": "2026-01-01T00:00:00Z"})
			}
			page, per := pageOf(r)
			lo, hi := (page-1)*per, page*per
			if lo > len(list) {
				lo = len(list)
			}
			if hi > len(list) {
				hi = len(list)
			}
			if hi < len(list) {
				w.Header().Set("Link", `<https://api.github.com/x?page=`+strconv.Itoa(page+1)+`>; rel="next"`)
			}
			writeJSON(w, list[lo:hi])
			return
		}
		writeJSON(w, issueJSON(i, true))
		return
	}
	jsonError(w, http.StatusNotFound, "Not Found")
}

func (s *Server) pull(w http.ResponseWriter, r *http.Request, repo *Repo, rest string) {
	num, tail, _ := strings.Cut(rest, "/")
	n, _ := strconv.Atoi(num)
	for _, p := range repo.PRs {
		if p.Number != n {
			continue
		}
		if r.Method == http.MethodPost && tail == "comments" {
			s.inlineComment(w, r, repo, p)
			return
		}
		switch tail {
		case "":
			add, del := 0, 0
			for _, f := range p.Files {
				add += f.Additions
				del += f.Deletions
			}
			writeJSON(w, map[string]any{"number": p.Number, "title": p.Title, "body": p.Body, "state": p.State, "draft": false, "merged": false,
				"user": map[string]any{"login": p.User}, "base": map[string]any{"ref": p.Base}, "head": map[string]any{"ref": p.Head, "sha": headSHA(p)},
				"additions": add, "deletions": del, "changed_files": len(p.Files), "commits": 1, "comments": 0, "review_comments": 0,
				"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "html_url": "https://github.com/" + repo.FullName + "/pull/" + num})
		case "files":
			var list []map[string]any
			for _, f := range p.Files {
				e := map[string]any{"filename": f.Name, "status": f.Status, "additions": f.Additions, "deletions": f.Deletions, "changes": f.Additions + f.Deletions}
				if f.Patch != "" {
					e["patch"] = f.Patch
				}
				list = append(list, e)
			}
			page, per := pageOf(r)
			lo, hi := (page-1)*per, page*per
			if lo > len(list) {
				lo = len(list)
			}
			if hi > len(list) {
				hi = len(list)
			}
			if hi < len(list) {
				w.Header().Set("Link", `<https://api.github.com/x?page=`+strconv.Itoa(page+1)+`>; rel="next"`)
			}
			writeJSON(w, list[lo:hi])
		case "reviews":
			var list []map[string]any
			for k, v := range p.Reviews {
				list = append(list, map[string]any{"id": 2000 + k, "user": map[string]any{"login": v.User}, "state": v.State, "body": v.Body})
			}
			writeJSON(w, list)
		default:
			jsonError(w, http.StatusNotFound, "Not Found")
		}
		return
	}
	jsonError(w, http.StatusNotFound, "Not Found")
}

func issueJSON(i *Issue, full bool) map[string]any {
	labels := make([]map[string]any, 0, len(i.Labels))
	for _, l := range i.Labels {
		labels = append(labels, map[string]any{"name": l})
	}
	out := map[string]any{"number": i.Number, "title": i.Title, "state": i.State, "user": map[string]any{"login": i.User}, "labels": labels,
		"comments": len(i.Comments), "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "html_url": "https://github.com/x/y/issues/" + strconv.Itoa(i.Number)}
	if i.IsPR {
		out["pull_request"] = map[string]any{"url": "x"}
	}
	out["locked"] = i.Locked
	if full {
		out["body"] = i.Body
	}
	return out
}

func headSHA(p *PR) string {
	if p.HeadSHA != "" {
		return p.HeadSHA
	}
	return "abc123"
}

func (s *Server) dropIfAsked(w http.ResponseWriter) bool {
	s.mu.Lock()
	drop := s.dropWrites
	s.mu.Unlock()
	if !drop {
		return false
	}
	if h, ok := w.(http.Hijacker); ok {
		if conn, _, err := h.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
	return true
}

func readBody(r *http.Request) map[string]any {
	var in map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
	return in
}

func str(in map[string]any, k string) string { v, _ := in[k].(string); return v }

func (s *Server) createIssue(w http.ResponseWriter, r *http.Request, repo *Repo) {
	in := readBody(r)
	if str(in, "title") == "" {
		jsonError(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	s.mu.Lock()
	n := 1
	for _, i := range repo.Issues {
		n = max(n, i.Number+1)
	}
	i := &Issue{Number: n, Title: str(in, "title"), Body: str(in, "body"), State: "open", User: "octo"}
	if ls, ok := in["labels"].([]any); ok {
		for _, l := range ls {
			if name, ok := l.(string); ok {
				i.Labels = append(i.Labels, name)
			}
		}
	}
	repo.Issues = append(repo.Issues, i)
	s.mu.Unlock()
	if s.dropIfAsked(w) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, issueJSON(i, true))
}

func (s *Server) changeIssue(w http.ResponseWriter, r *http.Request, repo *Repo, i *Issue, tail string) {
	in := readBody(r)
	s.mu.Lock()
	switch {
	case tail == "comments" && r.Method == http.MethodPost:
		if i.Locked {
			s.mu.Unlock()
			jsonError(w, http.StatusForbidden, "Issue is locked")
			return
		}
		i.Comments = append(i.Comments, str(in, "body"))
		id := 1000 + len(i.Comments) - 1
		s.mu.Unlock()
		if s.dropIfAsked(w) {
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"id": id, "body": str(in, "body"), "html_url": "https://github.com/" + repo.FullName + "/issues/" + strconv.Itoa(i.Number) + "#issuecomment-" + strconv.Itoa(id)})
		return
	case tail == "labels" && r.Method == http.MethodPost:
		if ls, ok := in["labels"].([]any); ok {
			for _, l := range ls {
				name, _ := l.(string)
				have := false
				for _, x := range i.Labels {
					have = have || strings.EqualFold(x, name)
				}
				if name != "" && !have {
					i.Labels = append(i.Labels, name)
				}
			}
		}
		out := issueLabels(i)
		s.mu.Unlock()
		if s.dropIfAsked(w) {
			return
		}
		writeJSON(w, out)
		return
	case tail == "" && r.Method == http.MethodPatch:
		if v := str(in, "title"); v != "" {
			i.Title = v
		}
		if v := str(in, "state"); v != "" {
			i.State = v
			i.Reason = str(in, "state_reason")
		}
		out := issueJSON(i, true)
		s.mu.Unlock()
		if s.dropIfAsked(w) {
			return
		}
		writeJSON(w, out)
		return
	}
	s.mu.Unlock()
	jsonError(w, http.StatusNotFound, "Not Found")
}

func issueLabels(i *Issue) []map[string]any {
	out := make([]map[string]any, 0, len(i.Labels))
	for _, l := range i.Labels {
		out = append(out, map[string]any{"name": l})
	}
	return out
}

func (s *Server) inlineComment(w http.ResponseWriter, r *http.Request, repo *Repo, p *PR) {
	in := readBody(r)
	line, _ := in["line"].(float64)
	if str(in, "path") == "" || line < 1 || str(in, "commit_id") == "" {
		jsonError(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	s.mu.Lock()
	p.Inline = append(p.Inline, InlineComment{Body: str(in, "body"), Path: str(in, "path"), Side: str(in, "side"), CommitID: str(in, "commit_id"), Line: int(line)})
	id := 3000 + len(p.Inline)
	s.mu.Unlock()
	if s.dropIfAsked(w) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"id": id, "body": str(in, "body"), "html_url": "https://github.com/" + repo.FullName + "/pull/" + strconv.Itoa(p.Number) + "#discussion_r" + strconv.Itoa(id)})
}

func pageOf(r *http.Request) (page, per int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	per, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 100 {
		per = 30
	}
	return
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
