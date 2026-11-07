// SPDX-License-Identifier: MIT

// Package gdrivetest is a fake of the part of the Google Drive v3 REST API
// that the Google Drive add-on uses, like githubtest for GitHub. It checks the
// access token of each call against the tokens it knows and their scopes,
// reads the q expression of a list with a real (small) parser so a test sees
// whether a quote broke out of its literal, hides the items of shared drives
// from a call that does not ask for them, and can answer with a rate limit, a
// server failure or a 401. It keeps the calls so a test can see what was
// asked and with which token.
package gdrivetest

import (
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/addons/internal/googletest"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Media types the fake knows.
const (
	FolderType       = "application/vnd.google-apps.folder"
	DocumentType     = "application/vnd.google-apps.document"
	SpreadsheetType  = "application/vnd.google-apps.spreadsheet"
	PresentationType = "application/vnd.google-apps.presentation"
	DrawingType      = "application/vnd.google-apps.drawing"
)

// The scopes the fake checks.
const (
	ScopeReadonly = "https://www.googleapis.com/auth/drive.readonly"
	ScopeFile     = "https://www.googleapis.com/auth/drive.file"
)

// File is a file or a folder of the fake.
type File struct {
	ID       string
	Name     string
	MimeType string
	Parents  []string
	// Content is the text of a regular file, and what an export of a native
	// file returns.
	Content  []byte
	Trashed  bool
	Modified time.Time
	Version  int
	// DriveID is set for an item of a shared drive. A call that does not set
	// supportsAllDrives does not see it.
	DriveID string
	// CreatedByApp is set for a file the add-on created: the narrow scope
	// reaches only these.
	CreatedByApp  bool
	AppProperties map[string]string
}

// Server is a fake API.
// Call is one request the fake received.
type Call = googletest.Call

type Server struct {
	*httptest.Server
	googletest.Core

	// auth, when set, decides for a token that AddToken did not register. A
	// suite that runs a fake login service uses it, so the tokens that service
	// issues are the ones this one accepts.
	files map[string]*File
	order []string
	seq   int
	// NoMarkdown makes an export of a document as markdown fail, as an older
	// service does.
	noMarkdown bool
	// dropWrites makes the fake apply a change and then cut the connection
	// before it answers, as a network failure does.
	now func() time.Time
}

// New starts a fake API.
func New() *Server {
	s := &Server{files: map[string]*File{}, now: time.Now}
	s.Init(func(method string) bool { return method == http.MethodPost || method == http.MethodPatch })
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	s.files["root-id"] = &File{ID: "root-id", Name: "My Drive", MimeType: FolderType, Modified: s.now()}
	return s
}

// Reset removes every file but the root of My Drive, every token, the calls
// and the injected answers, and restores the defaults, so one server can serve
// many specs.
func (s *Server) Reset() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	root := s.files["root-id"]
	s.files = map[string]*File{"root-id": root}
	s.order, s.seq = nil, 0
	s.ResetCore()
	s.noMarkdown = false
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) { s.Serve(w, r, s.route) }

// SetNoMarkdown makes the export as markdown fail with a 400.
func (s *Server) SetNoMarkdown(on bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.noMarkdown = on
}

// AddFile adds f. A missing ID is made; a missing parent is the root of My
// Drive.
func (s *Server) AddFile(f File) *File {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.add(f)
}

func (s *Server) add(f File) *File {
	if f.ID == "" {
		s.seq++
		f.ID = fmt.Sprintf("file%04d", s.seq)
	}
	if f.MimeType == "" {
		f.MimeType = "text/plain"
	}
	if len(f.Parents) == 0 && f.DriveID == "" {
		f.Parents = []string{"root-id"}
	}
	if f.Modified.IsZero() {
		f.Modified = s.now().UTC().Truncate(time.Second)
	}
	if f.Version == 0 {
		f.Version = 1
	}
	c := f
	s.files[f.ID] = &c
	s.order = append(s.order, f.ID)
	return &c
}

// AddFolder adds a folder under parent ("" is the root of My Drive).
func (s *Server) AddFolder(id, name, parent string) *File {
	f := File{ID: id, Name: name, MimeType: FolderType}
	if parent != "" {
		f.Parents = []string{parent}
	}
	return s.AddFile(f)
}

// File returns a copy of the file with the ID, or nil.
func (s *Server) File(id string) *File {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if f, ok := s.files[id]; ok {
		c := *f
		return &c
	}
	return nil
}

// Find returns copies of the files named name.
func (s *Server) Find(name string) []File {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	var out []File
	for _, id := range s.order {
		if f := s.files[id]; f.Name == name {
			out = append(out, *f)
		}
	}
	return out
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, p *googletest.Principal) {
	path := r.URL.Path
	switch {
	case path == "/drive/v3/about" && r.Method == http.MethodGet:
		s.about(w, p)
	case path == "/drive/v3/files" && r.Method == http.MethodGet:
		s.list(w, r, p)
	case path == "/upload/drive/v3/files" && r.Method == http.MethodPost:
		s.upload(w, r, p)
	case strings.HasPrefix(path, "/upload/drive/v3/files/") && r.Method == http.MethodPatch:
		s.updateMedia(w, r, p, strings.TrimPrefix(path, "/upload/drive/v3/files/"))
	case strings.HasPrefix(path, "/drive/v3/files/"):
		rest := strings.TrimPrefix(path, "/drive/v3/files/")
		id, sub, _ := strings.Cut(rest, "/")
		switch {
		case sub == "" && r.Method == http.MethodGet:
			s.get(w, r, p, id)
		case sub == "" && r.Method == http.MethodPatch:
			s.patch(w, r, p, id)
		case sub == "export" && r.Method == http.MethodGet:
			s.export(w, r, p, id)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func canRead(p *googletest.Principal) bool { return p.Scopes[ScopeReadonly] || p.Scopes[ScopeFile] }

// canSee says whether the token reaches f at all: the narrow scope reaches
// only the files the add-on created.
func canSee(p *googletest.Principal, f *File) bool {
	if p.Scopes[ScopeReadonly] {
		return true
	}
	return p.Scopes[ScopeFile] && f.CreatedByApp
}

func allDrives(r *http.Request) bool { return r.URL.Query().Get("supportsAllDrives") == "true" }

func (s *Server) about(w http.ResponseWriter, p *googletest.Principal) {
	if !canRead(p) {
		googletest.Fail(w, http.StatusForbidden, "insufficientPermissions", "scope")
		return
	}
	googletest.Reply(w, 200, map[string]any{"user": map[string]string{"permissionId": "perm-" + p.Email, "emailAddress": p.Email, "displayName": "Person"}})
}

func (f *File) json() map[string]any {
	m := map[string]any{"id": f.ID, "name": f.Name, "mimeType": f.MimeType, "parents": f.Parents, "trashed": f.Trashed,
		"modifiedTime": f.Modified.UTC().Format(time.RFC3339), "createdTime": f.Modified.UTC().Format(time.RFC3339), "version": strconv.Itoa(f.Version),
		"webViewLink": "https://drive.google.com/file/d/" + f.ID + "/view"}
	if !strings.HasPrefix(f.MimeType, "application/vnd.google-apps.") {
		m["size"] = strconv.Itoa(len(f.Content))
	}
	if f.DriveID != "" {
		m["driveId"] = f.DriveID
	}
	return m
}

// lookup finds a file the call may see.
func (s *Server) lookup(w http.ResponseWriter, r *http.Request, p *googletest.Principal, id string) *File {
	if !canRead(p) {
		googletest.Fail(w, http.StatusForbidden, "insufficientPermissions", "scope")
		return nil
	}
	if id == "root" {
		id = "root-id"
	}
	f, ok := s.files[id]
	if !ok || (f.DriveID != "" && !allDrives(r)) || !canSee(p, f) {
		googletest.Fail(w, http.StatusNotFound, "notFound", "File not found")
		return nil
	}
	return f
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, p *googletest.Principal, id string) {
	f := s.lookup(w, r, p, id)
	if f == nil {
		return
	}
	if r.URL.Query().Get("alt") != "media" {
		googletest.Reply(w, 200, f.json())
		return
	}
	if strings.HasPrefix(f.MimeType, "application/vnd.google-apps.") {
		googletest.Fail(w, http.StatusForbidden, "fileNotDownloadable", "Only files with binary content can be downloaded")
		return
	}
	data := f.Content
	if rg := r.Header.Get("Range"); rg != "" {
		spec, ok := strings.CutPrefix(rg, "bytes=")
		a, b, found := strings.Cut(spec, "-")
		start, err1 := strconv.Atoi(a)
		end, err2 := strconv.Atoi(b)
		if !ok || !found || err1 != nil || err2 != nil || start > end {
			googletest.Fail(w, http.StatusBadRequest, "badRequest", "range")
			return
		}
		if start >= len(data) {
			googletest.Fail(w, http.StatusRequestedRangeNotSatisfiable, "requestedRangeNotSatisfiable", "range")
			return
		}
		end = min(end, len(data)-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
		return
	}
	_, _ = w.Write(data)
}

// exports maps a native type to the export formats the service offers.
var exports = map[string][]string{
	DocumentType:     {"text/plain", "text/markdown", "text/html", "application/pdf"},
	SpreadsheetType:  {"text/csv", "application/pdf"},
	PresentationType: {"text/plain", "application/pdf"},
	DrawingType:      {"image/png", "application/pdf"},
}

func (s *Server) export(w http.ResponseWriter, r *http.Request, p *googletest.Principal, id string) {
	f := s.lookup(w, r, p, id)
	if f == nil {
		return
	}
	want := r.URL.Query().Get("mimeType")
	offered := exports[f.MimeType]
	ok := false
	for _, o := range offered {
		ok = ok || o == want
	}
	if want == "text/markdown" && s.noMarkdown {
		ok = false
	}
	if !ok {
		googletest.Fail(w, http.StatusBadRequest, "badRequest", "Export format not supported")
		return
	}
	w.Header().Set("Content-Type", want)
	_, _ = w.Write(f.Content)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, p *googletest.Principal) {
	if !canRead(p) {
		googletest.Fail(w, http.StatusForbidden, "insufficientPermissions", "scope")
		return
	}
	q := r.URL.Query()
	conds, err := parseQuery(q.Get("q"))
	if err != nil {
		googletest.Fail(w, http.StatusBadRequest, "invalidQuery", "Invalid Value")
		return
	}
	showAll := allDrives(r) && q.Get("includeItemsFromAllDrives") == "true"
	var hits []*File
	for _, id := range s.order {
		f := s.files[id]
		if f.ID == "root-id" || (f.DriveID != "" && !showAll) || !canSee(p, f) {
			continue
		}
		match := true
		for _, c := range conds {
			if !c.match(f) {
				match = false
				break
			}
		}
		if match {
			hits = append(hits, f)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	size := 100
	if n, err := strconv.Atoi(q.Get("pageSize")); err == nil && n > 0 {
		size = min(n, 1000)
	}
	start := 0
	if t := q.Get("pageToken"); t != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(t, "page-"))
		if err != nil || !strings.HasPrefix(t, "page-") || n < 0 {
			googletest.Fail(w, http.StatusBadRequest, "badRequest", "Invalid Value")
			return
		}
		start = n
	}
	start = min(start, len(hits))
	end := min(start+size, len(hits))
	out := map[string]any{"files": []map[string]any{}}
	files := []map[string]any{}
	for _, f := range hits[start:end] {
		files = append(files, f.json())
	}
	out["files"] = files
	if end < len(hits) {
		out["nextPageToken"] = "page-" + strconv.Itoa(end)
	}
	googletest.Reply(w, 200, out)
}

type fileInput struct {
	Name          *string           `json:"name"`
	MimeType      string            `json:"mimeType"`
	Parents       []string          `json:"parents"`
	Trashed       *bool             `json:"trashed"`
	AppProperties map[string]string `json:"appProperties"`
}

// writable checks the scope for a change.
func writable(p *googletest.Principal, w http.ResponseWriter) bool {
	if !p.Scopes[ScopeFile] {
		googletest.Fail(w, http.StatusForbidden, "insufficientPermissions", "scope")
		return false
	}
	return true
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request, p *googletest.Principal) {
	if !writable(p, w) {
		return
	}
	typ, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/related" || r.URL.Query().Get("uploadType") != "multipart" || !allDrives(r) {
		googletest.Fail(w, http.StatusBadRequest, "badRequest", "upload")
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	var meta fileInput
	var content []byte
	for i := 0; ; i++ {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(part)
		if i == 0 {
			if json.Unmarshal(b, &meta) != nil {
				googletest.Fail(w, http.StatusBadRequest, "badRequest", "metadata")
				return
			}
		} else {
			content = b
		}
	}
	if meta.Name == nil || len(meta.Parents) != 1 {
		googletest.Fail(w, http.StatusBadRequest, "badRequest", "metadata")
		return
	}
	par, ok := s.files[meta.Parents[0]]
	if !ok || par.MimeType != FolderType || !canSee(p, par) && par.ID != "root-id" && !p.Scopes[ScopeReadonly] {
		googletest.Fail(w, http.StatusNotFound, "notFound", "Parent not found")
		return
	}
	f := s.add(File{Name: *meta.Name, MimeType: meta.MimeType, Parents: meta.Parents, Content: content, CreatedByApp: true, AppProperties: meta.AppProperties, DriveID: par.DriveID})
	googletest.Reply(w, 200, f.json())
}

func (s *Server) updateMedia(w http.ResponseWriter, r *http.Request, p *googletest.Principal, id string) {
	if !writable(p, w) {
		return
	}
	f, ok := s.files[id]
	if !ok || !f.CreatedByApp || r.URL.Query().Get("uploadType") != "media" || !allDrives(r) {
		googletest.Fail(w, http.StatusNotFound, "notFound", "File not found")
		return
	}
	f.Content, _ = io.ReadAll(r.Body)
	f.Version++
	f.Modified = s.now().UTC().Truncate(time.Second)
	googletest.Reply(w, 200, f.json())
}

func (s *Server) patch(w http.ResponseWriter, r *http.Request, p *googletest.Principal, id string) {
	if !writable(p, w) {
		return
	}
	f, ok := s.files[id]
	if !ok || !f.CreatedByApp || !allDrives(r) {
		googletest.Fail(w, http.StatusNotFound, "notFound", "File not found")
		return
	}
	var in fileInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		googletest.Fail(w, http.StatusBadRequest, "badRequest", "body")
		return
	}
	q := r.URL.Query()
	if in.Name != nil {
		f.Name = *in.Name
	}
	if in.Trashed != nil {
		f.Trashed = *in.Trashed
	}
	if rm := q.Get("removeParents"); rm != "" {
		var keep []string
		for _, par := range f.Parents {
			if !strings.Contains(","+rm+",", ","+par+",") {
				keep = append(keep, par)
			}
		}
		f.Parents = keep
	}
	if add := q.Get("addParents"); add != "" {
		for _, a := range strings.Split(add, ",") {
			par, ok := s.files[a]
			if !ok || par.MimeType != FolderType {
				googletest.Fail(w, http.StatusNotFound, "notFound", "Parent not found")
				return
			}
			f.Parents = append(f.Parents, a)
		}
	}
	f.Version++
	f.Modified = s.now().UTC().Truncate(time.Second)
	googletest.Reply(w, 200, f.json())
}
