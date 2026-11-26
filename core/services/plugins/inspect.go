package plugins

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// Preview is the result of an inspect (spec 9.3 steps 1 and 2): what the
// person who installs reviews.
type Preview struct {
	// Token confirms this preview once, before ExpiresAt.
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`

	PluginID      string `json:"plugin"`
	Name          string `json:"name"`
	Version       string `json:"version,omitempty"`
	Description   string `json:"description,omitempty"`
	MarketplaceID string `json:"marketplace"`
	Entry         string `json:"entry"`
	// SHA is the full commit the files come from; Digest names the files.
	SHA         string       `json:"sha"`
	Digest      string       `json:"digest"`
	SizeBytes   int64        `json:"sizeBytes"`
	FileCount   int          `json:"fileCount"`
	Files       []store.File `json:"files"`
	Model       Model        `json:"model"`
	RunsCode    bool         `json:"runsCode"`
	CodeReasons []string     `json:"codeReasons"`
	CodeFiles   []string     `json:"codeFiles"`
	// Hosts are the hosts that the remote MCP servers connect to.
	Hosts []string `json:"hosts,omitempty"`
}

// checked is a plugin fetched into a scratch folder and checked.
type checked struct {
	dir       string
	sha       string
	tree      content.Tree
	digest    string
	plugin    *claudeplugin.Plugin
	reasons   []string
	codeFiles []string
	model     Model
	done      func()
}

// resolved is a marketplace entry and where it is.
type resolved struct {
	mkt    store.Marketplace
	snap   store.Snapshot
	parsed *claudeplugin.Marketplace
	entry  *claudeplugin.Entry
	// pluginID is name@marketplace with the current name of the entry.
	pluginID string
}

// resolveEntry finds the entry of a plugin ID in the snapshot of an active
// marketplace. Browse reads the same snapshot.
func (s *Service) resolveEntry(ctx context.Context, pluginID string) (resolved, error) {
	name, mktID, ok := claudeplugin.ParsePluginID(pluginID)
	if !ok {
		return resolved{}, fmt.Errorf("%w: %q is not name@marketplace", ErrInvalid, pluginID)
	}
	m, err := s.marketplace(ctx, mktID)
	if err != nil {
		return resolved{}, err
	}
	if v := s.view(m); !v.Active {
		return resolved{}, fmt.Errorf("%w: marketplace %q is off", ErrNotFound, mktID)
	}
	snap, err := s.st.Snapshot(ctx, mktID)
	if errors.Is(err, store.ErrNotFound) {
		return resolved{}, fmt.Errorf("%w: marketplace %q has not been read yet", ErrNotFound, mktID)
	}
	if err != nil {
		return resolved{}, err
	}
	parsed, err := ParsedSnapshot(snap)
	if err != nil {
		return resolved{}, err
	}
	e, err := parsed.Find(name)
	if err != nil {
		return resolved{}, fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	ent := normalizeEntry(m.Kind, *e)
	if !ent.Supported {
		return resolved{}, fmt.Errorf("%w: %s (%s)", ErrUnsupported, e.Name, ent.Reason)
	}
	return resolved{mkt: m, snap: snap, parsed: parsed, entry: e, pluginID: e.Name + "@" + mktID}, nil
}

// ErrUnsupported means the entry has a source that LocalAI does not fetch.
var ErrUnsupported = errors.New("plugins: this source type is not supported")

// fetchChecked fetches the entry at sha (or at what the entry points at when
// sha is empty), checks the tree, computes the digest, parses the plugin and
// classifies it. The caller calls done.
func (s *Service) fetchChecked(ctx context.Context, r resolved, sha string) (*checked, error) {
	if sha == "" {
		var err error
		if sha, err = s.targetSHA(ctx, r.mkt, r.snap, r.entry); err != nil {
			return nil, fmt.Errorf("plugins: resolve %s: %w", r.pluginID, err)
		}
	}
	src := fetch.MarketplaceSource{Kind: r.mkt.Kind, Location: r.mkt.Location, SHA: r.snap.SHA}
	f, err := s.fetcher.FetchPlugin(ctx, src, r.parsed, r.entry, sha)
	if err != nil {
		return nil, err
	}
	c := &checked{dir: f.Dir, sha: f.SHA, done: f.Done}
	fail := func(err error) (*checked, error) {
		f.Done()
		return nil, err
	}
	if c.sha != sha {
		return fail(fmt.Errorf("plugins: fetched %s, want %s", c.sha, sha))
	}
	if c.tree, err = content.Check(f.Dir, s.limits()); err != nil {
		return fail(err)
	}
	c.digest = content.Digest(c.tree)
	if c.plugin, err = claudeplugin.Load(f.Dir, claudeplugin.LoadOptions{Entry: r.entry, Marketplace: r.mkt.ID, Name: r.entry.Name}); err != nil {
		return fail(fmt.Errorf("%w: %w", ErrInvalid, err))
	}
	if err := checkPluginName(c.plugin); err != nil {
		return fail(err)
	}
	c.reasons = CodeReasons(c.plugin)
	c.codeFiles = content.CodeFiles(c.tree, c.plugin)
	c.model = ModelOf(c.plugin)
	return c, nil
}

// writePackage stores the files of c in the workspace store, once per
// digest.
func (s *Service) writePackage(ctx context.Context, c *checked) error {
	if s.ws.Files == nil {
		return nil
	}
	return content.Write(ctx, s.ws, s.root, c.digest, c.dir, c.tree)
}

func (c *checked) files() []store.File {
	out := make([]store.File, 0, len(c.tree.Files))
	for _, f := range c.tree.Files {
		out = append(out, store.File{Path: f.Path, Size: f.Size, Mode: f.Mode, SHA256: f.SHA256})
	}
	return out
}

// Inspect fetches a plugin, checks and parses it, and returns what it
// contributes with a preview token (spec 9.3 steps 1 and 2). Any user may
// inspect. The files go into the workspace store under their digest, so
// that every frontend can show them for review; a package that no install
// takes is collected after 24 hours.
func (s *Service) Inspect(ctx context.Context, a Actor, pluginID string) (Preview, error) {
	if a.ID == "" {
		return Preview{}, ErrForbidden
	}
	r, err := s.resolveEntry(ctx, pluginID)
	if err != nil {
		return Preview{}, err
	}
	c, err := s.fetchChecked(ctx, r, "")
	if err != nil {
		return Preview{}, err
	}
	defer c.done()
	if err := s.writePackage(ctx, c); err != nil {
		return Preview{}, err
	}
	p := Preview{
		PluginID: r.pluginID, Name: c.plugin.Name, Version: c.plugin.Version, Description: c.plugin.Description,
		MarketplaceID: r.mkt.ID, Entry: r.entry.Name, SHA: c.sha, Digest: c.digest,
		SizeBytes: c.tree.Size, FileCount: len(c.tree.Files), Files: c.files(), Model: c.model,
		RunsCode: len(c.reasons) > 0, CodeReasons: c.reasons, CodeFiles: c.codeFiles, Hosts: c.model.Hosts(),
	}
	p.Token, p.ExpiresAt, err = s.newToken(ctx, store.Token{
		Kind: store.TokenInstall, Principal: a.ID, PluginID: r.pluginID, SHA: c.sha, Digest: c.digest,
		RunsCode: p.RunsCode, CodeFiles: c.codeFiles,
	})
	return p, err
}
