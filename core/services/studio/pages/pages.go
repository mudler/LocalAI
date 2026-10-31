// Package pages serves the pages of a user's workspace: small HTML pages, most
// often written by the assistant, that the UI shows in a sandboxed frame. A
// page is the folder pages/<name>/ of the user's home, with an index.html, an
// optional page.json (title, icon, description) and the page's data files.
//
// The service is the server side of the bridge that a page uses: it reads,
// writes and lists files inside the page's own folder and nowhere else, with
// the rights of the user who views the page. It never runs a page.
package pages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Layout and limits of a page.
const (
	// Dir is the folder of the user's home that holds one folder per page.
	Dir = "pages"
	// IndexFile is the page itself.
	IndexFile = "index.html"
	// MetaFile holds the title, icon and description of a page.
	MetaFile = "page.json"
	// MaxFileSize bounds index.html and every file the bridge reads or writes.
	MaxFileSize = 1 << 20
	// MaxPages bounds a listing.
	MaxPages = 200

	maxPathBytes = 256
	maxDepth     = 8
	maxMetaBytes = 16 << 10
	maxTitle     = 120
	maxDesc      = 500
)

// Who changed a page last, and where a page comes from.
const (
	AuthorYou       = "you"
	AuthorAssistant = "assistant"
	// SourceOwn is a page in the user's own home. Shared folders come later and
	// will be SourceShared, with the author's name.
	SourceOwn    = "own"
	SourceShared = "shared"
)

// Errors of the service. Test them with errors.Is.
var (
	ErrBadName     = errors.New("pages: a page name is lowercase letters, digits and dashes, at most 64")
	ErrBadPath     = errors.New("pages: the path is not a file of the page's folder")
	ErrNotFound    = errors.New("pages: not found")
	ErrTooLarge    = errors.New("pages: the file is larger than 1 MiB")
	ErrNotText     = errors.New("pages: the file is not UTF-8 text")
	ErrNotImage    = errors.New("pages: only a png, jpg, gif, webp, avif, bmp, ico or svg file can be read as an image")
	ErrReadOnly    = errors.New("pages: a page cannot change its own index.html or page.json")
	ErrRateLimited = errors.New("pages: too many changes; wait a moment")
	// ErrFolderReadOnly means the viewer may only read the shared folder of the
	// page, so the page cannot write there either.
	ErrFolderReadOnly = errors.New("pages: you can only read this shared folder, so the page cannot save here")
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var iconRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// ValidName says whether name can name a page.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// CleanPath checks a path that a page names, relative to its own folder, and
// returns it unchanged. It refuses an absolute path, a parent or current folder
// segment, a backslash, a colon, a control character, an empty segment and a
// name the workspace would store under another form, so the path can only name
// something inside the folder. The workspace has no symbolic links, so a clean
// path cannot leave the folder through one either.
func CleanPath(rel string) (string, error) {
	if rel == "" || len(rel) > maxPathBytes || !utf8.ValidString(rel) || strings.HasPrefix(rel, "/") {
		return "", ErrBadPath
	}
	segs := strings.Split(rel, "/")
	if len(segs) > maxDepth {
		return "", ErrBadPath
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, `\:`) {
			return "", ErrBadPath
		}
		for _, r := range s {
			if unicode.IsControl(r) {
				return "", ErrBadPath
			}
		}
		clean, err := workspace.CleanName(s)
		if err != nil || clean != s {
			return "", ErrBadPath
		}
	}
	return rel, nil
}

// Page is one page of a user.
type Page struct {
	Name        string
	Path        string // inside the home: /pages/<name>
	Title       string
	Icon        string
	Description string
	Source      string // SourceOwn or SourceShared
	// Author is who wrote the current index.html: AuthorYou or AuthorAssistant
	// for an own page. AuthorID is the principal that wrote it (a shared page's
	// author is another account).
	Author   string
	AuthorID string
	// A shared page also has its shared folder (Folder, a workspace path, and
	// FolderID, the folder's node ID), whether the viewer may write in it, the
	// node ID of the page's own folder (PageID) and the hash of its code
	// (index.html, page.json and the files of CodeFiles), against which a
	// consent is kept. CodeFiles are the scripts, style sheets and modules of
	// the folder that the UI puts into the page: the UI loads no other text
	// file into a shared page.
	CodeFiles []string
	Folder    string
	FolderID  int64
	Writable  bool
	PageID    int64
	Hash      string
	MetaSize  int64 // of page.json, 0 when it has none
	// UpdatedAt and UpdatedBy are the last change to anything in the folder.
	UpdatedAt time.Time
	UpdatedBy string
	Rev       int64
	Size      int64 // of index.html
}

// File is an entry of a page's folder, with its path relative to the folder.
type File struct {
	Path       string
	Kind       string // file or dir
	Size       int64
	Version    int64
	ConflictOf string // set when a write kept both versions
}

// Content is a text file of a page's folder.
type Content struct {
	File
	Content string
}

// Service serves the pages of each user's home.
type Service struct {
	f      *files.Service
	now    func() time.Time
	writes *limiter
	sends  *limiter
	refs   refCache
}

// Option configures a Service.
type Option func(*Service)

// WithClock sets the clock of the rate limits, for tests.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// New returns the service on the files of the chats.
func New(f *files.Service, opts ...Option) *Service {
	s := &Service{f: f, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	// A page that saves on every keystroke must debounce; 30 writes a minute
	// with a burst of 20 is plenty for a person using a page.
	s.writes = newLimiter(20, 2*time.Second, s.now)
	// A message to the assistant starts a turn, which costs far more.
	s.sends = newLimiter(3, 20*time.Second, s.now)
	return s
}

func (s *Service) actor(principal string) workspace.Actor { return s.f.Actor(principal) }

// loc is where a page lives and who looks at it: the page's folder in the
// workspace and the viewer's actor. Every call of the bridge goes through one,
// so an own page and a shared page are read and written the same way, with the
// viewer's own rights.
type loc struct {
	a      workspace.Actor
	folder string // workspace path of pages/<name>
	name   string
	key    string // the rate limit bucket
	// shared pages run only with a consent to their code, so their hash
	// covers the files of code too.
	shared bool
}

// own returns the location of a page of the user's home.
func (s *Service) own(principal, name string) (loc, error) {
	if !ValidName(name) {
		return loc{}, ErrBadName
	}
	folder, err := s.f.Abs(principal, "/"+Dir+"/"+name)
	if err != nil {
		return loc{}, err
	}
	return loc{a: s.actor(principal), folder: folder, name: name, key: principal + "/" + name}, nil
}

// path returns the workspace path of rel, a path that CleanPath accepted,
// inside the page's folder; rel "" is the folder.
func (l loc) path(rel string) string {
	if rel == "" {
		return l.folder
	}
	return l.folder + "/" + rel
}

func mapNotFound(err error) error {
	if errors.Is(err, workspace.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// List returns the pages of the user's home, sorted by name. A folder of pages/
// without an index.html, or with a name that is not a page name, is left out.
func (s *Service) List(ctx context.Context, principal string) ([]Page, error) {
	dir, err := s.f.Abs(principal, "/"+Dir)
	if err != nil {
		return nil, err
	}
	return s.listIn(ctx, s.actor(principal), dir, func(name string) (loc, error) { return s.own(principal, name) }, func(p *Page) {
		p.Path = "/" + Dir + "/" + p.Name
	})
}

// listIn lists the pages of one pages/ folder.
func (s *Service) listIn(ctx context.Context, a workspace.Actor, dir string, at func(string) (loc, error), fill func(*Page)) ([]Page, error) {
	kids, err := s.f.Store().List(ctx, a, dir)
	if errors.Is(err, workspace.ErrNotFound) {
		return []Page{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Page{}
	for _, k := range kids {
		if k.Kind != workspace.KindDir || !ValidName(k.Name) {
			continue
		}
		l, err := at(k.Name)
		if err != nil {
			continue
		}
		p, err := s.getAt(ctx, l)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		fill(&p)
		out = append(out, p)
		if len(out) == MaxPages {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// existsAt checks that the page has an index.html: the bridge works only for
// a page, so a write can never create one.
func (s *Service) existsAt(ctx context.Context, l loc) error {
	n, err := s.f.Store().Stat(ctx, l.a, l.path(IndexFile))
	if err != nil {
		return mapNotFound(err)
	}
	if n.Kind != workspace.KindFile {
		return ErrNotFound
	}
	return nil
}

// Get returns one page.
func (s *Service) Get(ctx context.Context, principal, name string) (Page, error) {
	l, err := s.own(principal, name)
	if err != nil {
		return Page{}, err
	}
	p, err := s.getAt(ctx, l)
	if err != nil {
		return Page{}, err
	}
	p.Path = "/" + Dir + "/" + name
	return p, nil
}

func (s *Service) getAt(ctx context.Context, l loc) (Page, error) {
	index, err := s.f.Store().Stat(ctx, l.a, l.path(IndexFile))
	if err != nil {
		return Page{}, mapNotFound(err)
	}
	if index.Kind != workspace.KindFile {
		return Page{}, ErrNotFound
	}
	p := Page{Name: l.name, Title: l.name, Source: SourceOwn, Author: AuthorYou, UpdatedBy: AuthorYou, Rev: index.Rev, Size: index.Size}
	meta := s.readMeta(ctx, l.a, l.folder, &p)
	p.MetaSize = meta.Size
	var code []codeFile
	if l.shared {
		var over bool
		code, over = s.codeSet(ctx, l, index)
		for _, f := range code {
			p.CodeFiles = append(p.CodeFiles, f.path)
		}
		if over {
			code = append(code, codeFile{path: "\x00more", missing: true})
		}
	}
	p.Hash = codeHash(index, meta, code)
	if dir, err := s.f.Store().Stat(ctx, l.a, l.folder); err == nil {
		p.PageID = dir.ID
	}
	if cs, ok := s.last(ctx, l.a, l.folder); ok {
		p.UpdatedAt, p.UpdatedBy, p.Rev = cs.CreatedAt, authorOf(cs), cs.Rev
	}
	if cs, ok := s.last(ctx, l.a, l.path(IndexFile)); ok {
		p.Author, p.AuthorID = authorOf(cs), cs.Actor
	}
	return p, nil
}

func (s *Service) last(ctx context.Context, a workspace.Actor, p string) (workspace.ChangeSet, bool) {
	h, err := s.f.Store().History(ctx, a, workspace.HistoryQuery{Path: p, Limit: 1})
	if err != nil || len(h) == 0 {
		return workspace.ChangeSet{}, false
	}
	return h[0], true
}

// authorOf tells who made a change set: a chat's tools are the assistant;
// everything else (the user, an upload, an undo, a page) is the user.
func authorOf(cs workspace.ChangeSet) string {
	if cs.Kind == workspace.ChangeAgent || cs.ChatID != "" {
		return AuthorAssistant
	}
	return AuthorYou
}

// readMeta fills the title, icon and description from page.json and returns
// its node (zero when there is none). A missing or broken file keeps the
// defaults: the metadata is a nicety, never a reason for a page not to show.
func (s *Service) readMeta(ctx context.Context, a workspace.Actor, folder string, p *Page) workspace.Node {
	r, n, err := s.f.Store().Open(ctx, a, folder+"/"+MetaFile)
	if err != nil {
		return workspace.Node{}
	}
	defer func() { _ = r.Close() }()
	if n.Kind != workspace.KindFile {
		return workspace.Node{}
	}
	if n.Size > maxMetaBytes {
		return n
	}
	var m struct {
		Title       string `json:"title"`
		Icon        string `json:"icon"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(r, maxMetaBytes)).Decode(&m); err != nil {
		return n
	}
	if t := clip(m.Title, maxTitle); t != "" {
		p.Title = t
	}
	if iconRE.MatchString(m.Icon) {
		p.Icon = m.Icon
	}
	p.Description = clip(m.Description, maxDesc)
	return n
}

// clip trims white space, drops control characters and cuts s to max bytes on
// a rune boundary.
func clip(s string, max int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	for len(s) > max {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// Source returns a page with the text of its index.html.
func (s *Service) Source(ctx context.Context, principal, name string) (Page, string, error) {
	p, err := s.Get(ctx, principal, name)
	if err != nil {
		return Page{}, "", err
	}
	c, err := s.Read(ctx, principal, name, IndexFile)
	if err != nil {
		return Page{}, "", err
	}
	return p, c.Content, nil
}

// Read returns a text file of the page's folder.
func (s *Service) Read(ctx context.Context, principal, name, rel string) (Content, error) {
	l, err := s.own(principal, name)
	if err != nil {
		return Content{}, err
	}
	return s.readAt(ctx, l, rel)
}

func (s *Service) readAt(ctx context.Context, l loc, rel string) (Content, error) {
	f, body, err := s.bytesAt(ctx, l, rel)
	if err != nil {
		return Content{}, err
	}
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return Content{}, ErrNotText
	}
	return Content{File: f, Content: string(body)}, nil
}

// bytesAt reads a file of the page's folder, at most MaxFileSize.
func (s *Service) bytesAt(ctx context.Context, l loc, rel string) (File, []byte, error) {
	rel, err := CleanPath(rel)
	if err != nil {
		return File{}, nil, err
	}
	if err := s.existsAt(ctx, l); err != nil {
		return File{}, nil, err
	}
	r, n, err := s.f.Store().Open(ctx, l.a, l.path(rel))
	if err != nil {
		return File{}, nil, mapNotFound(err)
	}
	defer func() { _ = r.Close() }()
	if n.Kind != workspace.KindFile {
		return File{}, nil, ErrNotFound
	}
	if n.Size > MaxFileSize {
		return File{}, nil, ErrTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		return File{}, nil, fmt.Errorf("pages: read %s: %w", rel, err)
	}
	if len(body) > MaxFileSize {
		return File{}, nil, ErrTooLarge
	}
	return File{Path: rel, Kind: string(n.Kind), Size: n.Size, Version: n.Version}, body, nil
}

// The images that the UI may put into a page as data: URLs, by extension. The
// bridge of a page reads only text; the UI reads these for an <img>, a
// <source> or a CSS url() of the page before the page runs.
var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".svg": "image/svg+xml",
}

// ImageType is the media type of an image path, by its extension.
func ImageType(rel string) (string, bool) {
	t, ok := imageTypes[strings.ToLower(path.Ext(rel))]
	return t, ok
}

// Image is an image file of a page's folder.
type Image struct {
	File
	MediaType string // from the extension, never from the content
	Data      []byte
}

// ReadImage returns an image file of the page's folder as bytes. Only the
// extensions of ImageType are read; anything else is ErrNotImage, so this is
// no way to read an arbitrary binary file.
func (s *Service) ReadImage(ctx context.Context, principal, name, rel string) (Image, error) {
	l, err := s.own(principal, name)
	if err != nil {
		return Image{}, err
	}
	return s.readImageAt(ctx, l, rel)
}

func (s *Service) readImageAt(ctx context.Context, l loc, rel string) (Image, error) {
	if _, err := CleanPath(rel); err != nil {
		return Image{}, err
	}
	t, ok := ImageType(rel)
	if !ok {
		return Image{}, ErrNotImage
	}
	f, body, err := s.bytesAt(ctx, l, rel)
	if err != nil {
		return Image{}, err
	}
	return Image{File: f, MediaType: t, Data: body}, nil
}

// Write stores a file in the page's folder and creates missing folders. With
// base, the version the page read, a file that changed since is kept and the
// new content goes to a conflict copy; without, the file is replaced. A page
// cannot write its own index.html or page.json, so it cannot rewrite itself.
func (s *Service) Write(ctx context.Context, principal, name, rel string, body []byte, base int64) (File, error) {
	if !ValidName(name) {
		return File{}, ErrBadName
	}
	if _, err := CleanPath(rel); err != nil {
		return File{}, err
	}
	l, err := s.own(principal, name)
	if err != nil {
		return File{}, ErrBadPath
	}
	return s.writeAt(ctx, l, rel, body, base)
}

func (s *Service) writeAt(ctx context.Context, l loc, rel string, body []byte, base int64) (File, error) {
	rel, err := CleanPath(rel)
	if err != nil {
		return File{}, err
	}
	if first := workspace.FoldName(strings.SplitN(rel, "/", 2)[0]); first == workspace.FoldName(IndexFile) || first == workspace.FoldName(MetaFile) {
		return File{}, ErrReadOnly
	}
	if len(body) > MaxFileSize {
		return File{}, ErrTooLarge
	}
	if err := s.existsAt(ctx, l); err != nil {
		return File{}, err
	}
	if !s.writes.allow(l.key) {
		return File{}, ErrRateLimited
	}
	opts := workspace.PutOptions{MaxSize: MaxFileSize, Title: "Page " + l.name + ": save " + rel}
	if base > 0 {
		opts.BaseVersion = base
	} else {
		opts.Overwrite = true
	}
	n, err := s.f.Store().Put(ctx, l.a, l.path(rel), bytes.NewReader(body), opts)
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		switch {
		case errors.Is(err, workspace.ErrTooLarge):
			return File{}, ErrTooLarge
		case errors.Is(err, workspace.ErrReadOnly):
			return File{}, ErrFolderReadOnly
		}
		return File{}, mapNotFound(err)
	}
	return l.file(n), nil
}

// file is a node as the page sees it: its path relative to the page's folder.
func (l loc) file(n workspace.Node) File {
	prefix := l.folder + "/"
	f := File{Path: strings.TrimPrefix(n.Path, prefix), Kind: string(n.Kind), Size: n.Size, Version: n.Version}
	if n.ConflictOf != "" {
		f.ConflictOf = strings.TrimPrefix(n.ConflictOf, prefix)
	}
	return f
}

// ListFiles lists a folder of the page; dir "" is the page's folder.
func (s *Service) ListFiles(ctx context.Context, principal, name, dir string) ([]File, error) {
	l, err := s.own(principal, name)
	if err != nil {
		return nil, err
	}
	return s.listAt(ctx, l, dir)
}

func (s *Service) listAt(ctx context.Context, l loc, dir string) ([]File, error) {
	if dir != "" {
		var err error
		if dir, err = CleanPath(dir); err != nil {
			return nil, err
		}
	}
	if err := s.existsAt(ctx, l); err != nil {
		return nil, err
	}
	kids, err := s.f.Store().List(ctx, l.a, l.path(dir))
	if err != nil {
		return nil, mapNotFound(err)
	}
	out := make([]File, 0, len(kids))
	for _, k := range kids {
		out = append(out, l.file(k))
	}
	return out, nil
}

// AllowSend says whether a page may send another message to a chat now.
func (s *Service) AllowSend(principal, name string) bool {
	return s.sends.allow(principal + "/" + name)
}
