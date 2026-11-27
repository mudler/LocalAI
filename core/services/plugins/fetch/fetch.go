// Package fetch downloads plugin marketplaces and plugins in the Claude Code
// plugin format, with a hardened git and HTTP client around nib's
// claudeplugin.Fetch (spec section 15.2).
//
// Mode (see .agents/distributed-state.md): per instance. The scratch folders
// live under the data path of the frontend that runs the fetch. Each fetch
// removes its folder when it ends, and CleanScratch removes what a crash
// left behind at startup.
package fetch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Defaults of the design (spec 15.2).
const (
	DefaultGitTimeout      = 120 * time.Second
	DefaultHTTPTimeout     = 30 * time.Second
	DefaultMaxScratchBytes = 200 << 20
	// MaxMarketplaceBytes caps a marketplace.json.
	MaxMarketplaceBytes = 1 << 20
)

// Kinds of marketplace source, as in the store.
const (
	KindGitHub = "github"
	KindGit    = "git"
	KindURL    = "url"
)

// ErrRefusedURL means a location uses a scheme or a form that the fetcher
// does not allow: only https is allowed.
var ErrRefusedURL = errors.New("plugin source refused: only https locations are allowed")

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Options configure a Fetcher.
type Options struct {
	// ScratchRoot is the folder that holds the scratch folders, under the
	// data path. It is created when missing.
	ScratchRoot string
	// Limits cap the copied plugin; MaxFiles and MaxTotalBytes apply while
	// the files are copied out of the checkout.
	Limits content.Limits
	// MaxScratchBytes caps the checkout of one fetch. Zero is the default.
	MaxScratchBytes int64
	// GitTimeout bounds one fetch. Zero is the default.
	GitTimeout time.Duration
	// HTTPClient fetches marketplace.json files. Nil uses a client with the
	// default timeout. Its redirect rule is replaced: a redirect must stay
	// on https.
	HTTPClient *http.Client
	// AllowFileURLs lets tests use file:// repositories. Never set it in a
	// server.
	AllowFileURLs bool
	// RefusePrivate reports whether locations on private or internal
	// networks are refused (see checkSource). Nil allows them: the CLI
	// validates plugins of the networks of its user.
	RefusePrivate func() bool
}

// Fetcher fetches marketplaces and plugins. It keeps no state between calls.
type Fetcher struct {
	root       string
	lim        content.Limits
	maxScratch int64
	timeout    time.Duration
	http       *http.Client
	allowFile  bool
	// refusePrivate and publicHTTP: see private.go.
	refusePrivate func() bool
	publicHTTP    *http.Client
	lookup        func(ctx context.Context, host string) ([]net.IPAddr, error)
	// wrapGit lets tests watch the git commands.
	wrapGit func(claudeplugin.GitFunc) claudeplugin.GitFunc
}

// New returns a Fetcher. It creates the scratch root.
func New(o Options) (*Fetcher, error) {
	if o.ScratchRoot == "" {
		return nil, errors.New("fetch: a scratch root is required")
	}
	root, err := filepath.Abs(o.ScratchRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	f := &Fetcher{root: root, lim: o.Limits, maxScratch: o.MaxScratchBytes, timeout: o.GitTimeout, allowFile: o.AllowFileURLs,
		refusePrivate: o.RefusePrivate, lookup: net.DefaultResolver.LookupIPAddr}
	if f.lim == (content.Limits{}) {
		f.lim = content.DefaultLimits()
	}
	if f.maxScratch <= 0 {
		f.maxScratch = DefaultMaxScratchBytes
	}
	if f.timeout <= 0 {
		f.timeout = DefaultGitTimeout
	}
	// The clients come from pkg/httpclient (TLS 1.2 floor). A marketplace
	// URL may redirect (a raw file host does), so they follow redirects, on
	// https only and at most 5 hops.
	var client *http.Client
	if o.HTTPClient != nil {
		c := *o.HTTPClient
		client = httpclient.Harden(&c)
		if client.Timeout <= 0 {
			client.Timeout = DefaultHTTPTimeout
		}
		client.CheckRedirect = httpsRedirects(nil)
	} else {
		client = httpclient.NewWithTimeout(DefaultHTTPTimeout, httpclient.WithFollowRedirects())
		client.CheckRedirect = httpsRedirects(client.CheckRedirect)
	}
	f.http = client
	f.publicHTTP = publicClient(client.Timeout)
	return f, nil
}

// httpsRedirects allows at most 5 redirects, each to https, then applies
// next (the credential stripping of httpclient) when it is set.
func httpsRedirects(next func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return fmt.Errorf("%w: a redirect leaves https", ErrRefusedURL)
		}
		if next != nil {
			return next(req, via)
		}
		return nil
	}
}

// CleanScratch removes every scratch folder. A server calls it at startup,
// before it fetches anything.
func (f *Fetcher) CleanScratch() error {
	entries, err := os.ReadDir(f.root)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, os.RemoveAll(filepath.Join(f.root, e.Name())))
	}
	return errors.Join(errs...)
}

// newScratch makes a fresh scratch folder.
func (f *Fetcher) newScratch(kind string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(f.root, kind+"-"+hex.EncodeToString(b[:]))
	return dir, os.Mkdir(dir, 0o700)
}

// CheckGitURL refuses a git location that is not https: ssh://, git@,
// http://, file:// (unless a test allows it), a local path, or a URL with
// credentials in it.
func (f *Fetcher) CheckGitURL(raw string) error {
	if strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, "\x00\n\r") {
		return fmt.Errorf("%w: %q", ErrRefusedURL, raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRefusedURL, err)
	}
	switch {
	case u.Scheme == "https" && u.Host != "" && u.User == nil:
		return nil
	case u.Scheme == "file" && f.allowFile:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRefusedURL, redact(raw))
}

// redact drops what follows the scheme and host, and any user part, so an
// error never carries a credential.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "a location that is not a URL (scp-like or local path)"
	}
	return u.Scheme + "://" + u.Hostname()
}

// GitURL returns the clone URL of a github or git marketplace.
func GitURL(kind, location string) (string, error) {
	switch kind {
	case KindGitHub:
		if strings.Count(location, "/") != 1 || strings.HasPrefix(location, "-") || strings.ContainsAny(location, ":@ \\") {
			return "", fmt.Errorf("%w: %q is not owner/repo", ErrRefusedURL, location)
		}
		return "https://github.com/" + location + ".git", nil
	case KindGit:
		return location, nil
	}
	return "", fmt.Errorf("fetch: %q is not a git marketplace kind", kind)
}

// MarketplaceSource is where a marketplace lives, as the store keeps it.
type MarketplaceSource struct {
	Kind     string // KindGitHub, KindGit or KindURL
	Location string
	// SHA is the commit of the marketplace snapshot. A relative plugin
	// source is fetched at it.
	SHA string
}

// Fetched is a plugin in a scratch folder. Call Done to remove the folder.
type Fetched struct {
	// Dir is the plugin folder.
	Dir string
	// SHA is the commit the files came from.
	SHA    string
	Source claudeplugin.Source
	// Done removes the scratch folder. It is safe to call more than once.
	Done func()
}

// FetchPlugin fetches the plugin of entry e of the marketplace m into a new
// scratch folder, pinned to sha when it is set (a full commit SHA), else to
// the entry's sha, ref or the default branch. A relative source is fetched
// from the marketplace repository at src.SHA as a git-subdir source, so it
// is a partial, sparse clone too. On error nothing is left in the scratch
// root.
func (f *Fetcher) FetchPlugin(ctx context.Context, src MarketplaceSource, m *claudeplugin.Marketplace, e *claudeplugin.Entry, sha string) (*Fetched, error) {
	if sha != "" && !fullSHA.MatchString(sha) {
		return nil, fmt.Errorf("fetch: pin %q is not a full commit SHA", sha)
	}
	entry, err := f.gitEntry(src, m, e)
	if err != nil {
		return nil, err
	}
	if entry.Source.Kind == claudeplugin.SourceGitSubdir && entry.Source.Path != "" {
		if err := content.CheckRelPath(strings.Trim(entry.Source.Path, "/")); err != nil {
			return nil, fmt.Errorf("fetch: source path: %w", err)
		}
	}
	if err := f.checkSource(ctx, entry.Source.GitURL()); err != nil {
		return nil, err
	}

	scratch, err := f.newScratch("plugin")
	if err != nil {
		return nil, err
	}
	done := func() { _ = os.RemoveAll(scratch) }
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	res, err := claudeplugin.Fetch(ctx, m, entry, filepath.Join(scratch, "plugin"), claudeplugin.FetchOptions{
		SHA: sha, Git: f.Git(), MaxBytes: f.lim.MaxTotalBytes, MaxFiles: f.lim.MaxFiles,
	})
	if err != nil {
		done()
		return nil, fmt.Errorf("fetch %s: %w", e.Name, err)
	}
	return &Fetched{Dir: res.Dir, SHA: res.SHA, Source: e.Source, Done: done}, nil
}

// gitEntry returns the entry to give to claudeplugin.Fetch. A relative
// source becomes a git-subdir source of the marketplace repository at the
// snapshot's commit: LocalAI keeps no checkout of a marketplace, so
// claudeplugin.Fetch has no root to copy from.
func (f *Fetcher) gitEntry(src MarketplaceSource, m *claudeplugin.Marketplace, e *claudeplugin.Entry) (*claudeplugin.Entry, error) {
	if err := e.Source.Supported(); err != nil {
		return nil, err
	}
	if e.Source.Kind != claudeplugin.SourceRelative {
		return e, nil
	}
	if src.Kind == KindURL {
		return nil, fmt.Errorf("fetch %s: a url marketplace cannot use relative sources", e.Name)
	}
	if !fullSHA.MatchString(src.SHA) {
		return nil, fmt.Errorf("fetch %s: the marketplace snapshot has no commit", e.Name)
	}
	gitURL, err := GitURL(src.Kind, src.Location)
	if err != nil {
		return nil, err
	}
	sub, err := relativePath(m, e.Source.Path)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", e.Name, err)
	}
	out := *e
	out.Source = claudeplugin.Source{Kind: claudeplugin.SourceGitSubdir, URL: gitURL, Path: sub, SHA: src.SHA}
	return &out, nil
}

// relativePath resolves a relative source as the format does: "./x" is x
// under the marketplace root, and a bare name sits under
// metadata.pluginRoot. "." is the whole repository.
func relativePath(m *claudeplugin.Marketplace, p string) (string, error) {
	if p != "." && p != "./" && !strings.HasPrefix(p, "./") {
		if m == nil || m.PluginRoot == "" || strings.Contains(p, "/") {
			return "", fmt.Errorf("relative source %q: %w", p, claudeplugin.ErrPathPrefix)
		}
		p = strings.TrimSuffix(m.PluginRoot, "/") + "/" + p
	}
	clean := path.Clean(strings.TrimPrefix(p, "./"))
	if clean == "." {
		return "", fmt.Errorf("relative source %q: the whole marketplace repository is not a plugin LocalAI fetches", p)
	}
	if err := content.CheckRelPath(clean); err != nil {
		return "", fmt.Errorf("relative source %q: %w", p, err)
	}
	return clean, nil
}

// ResolveRef returns the commit that ref (or the default branch, when ref
// is empty) of a git repository points at, without a checkout.
func (f *Fetcher) ResolveRef(ctx context.Context, gitURL, ref string) (string, error) {
	if err := f.checkSource(ctx, gitURL); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	return claudeplugin.ResolveRef(ctx, claudeplugin.Source{Kind: claudeplugin.SourceURL, URL: gitURL}, ref, claudeplugin.FetchOptions{Git: f.Git()})
}

// FetchMarketplace returns the marketplace.json of a marketplace and the
// commit it was read at (empty for a url marketplace). A git marketplace is
// fetched as a partial, sparse clone of .claude-plugin/ at ref (or the
// default branch); nothing else of the repository is downloaded.
func (f *Fetcher) FetchMarketplace(ctx context.Context, kind, location, ref string) (body []byte, sha string, err error) {
	if kind == KindURL {
		body, err := f.fetchURL(ctx, location)
		return body, "", err
	}
	gitURL, err := GitURL(kind, location)
	if err != nil {
		return nil, "", err
	}
	if err := f.checkSource(ctx, gitURL); err != nil {
		return nil, "", err
	}
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\n") {
		return nil, "", fmt.Errorf("fetch: ref %q refused", ref)
	}
	scratch, err := f.newScratch("marketplace")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(scratch)
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	git := f.Git()
	want := ref
	if want == "" {
		want = "HEAD"
	}
	steps := [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", gitURL},
		{"sparse-checkout", "set", "--no-cone", "/.claude-plugin/"},
		{"fetch", "-q", "origin", want},
		{"checkout", "-q", "--detach", "FETCH_HEAD"},
	}
	for _, s := range steps {
		if _, err := git(ctx, scratch, s...); err != nil {
			return nil, "", err
		}
	}
	if sha, err = git(ctx, scratch, "rev-parse", "HEAD"); err != nil {
		return nil, "", err
	}
	if !fullSHA.MatchString(sha) {
		return nil, "", fmt.Errorf("fetch: git gave %q as the commit", sha)
	}
	// Read through a root at the checkout, so that no part of the path (the
	// .claude-plugin folder included, not only the file) can be a link out
	// of it.
	root, err := os.OpenRoot(scratch)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	info, err := root.Lstat(claudeplugin.MarketplacePath)
	if err != nil {
		return nil, "", fmt.Errorf("fetch: no %s at %s", claudeplugin.MarketplacePath, sha)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("fetch: %s is not a regular file", claudeplugin.MarketplacePath)
	}
	if info.Size() > MaxMarketplaceBytes {
		return nil, "", fmt.Errorf("fetch: %s is over %d bytes", claudeplugin.MarketplacePath, MaxMarketplaceBytes)
	}
	fh, err := root.Open(claudeplugin.MarketplacePath)
	if err != nil {
		return nil, "", err
	}
	defer fh.Close()
	body, err = readCapped(fh, MaxMarketplaceBytes)
	return body, sha, err
}

func readCapped(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("fetch: marketplace.json is over %d bytes", limit)
	}
	return b, nil
}
