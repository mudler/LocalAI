package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mudler/nib/plugin/claudeplugin"
	"github.com/mudler/xlog"

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
	// SHA is the full commit the files come from; Digest names the files
	// and the effective manifest.
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
	v, err := CheckDir(f.Dir, s.limits(), claudeplugin.LoadOptions{Entry: r.entry, Marketplace: r.mkt.ID, Name: r.entry.Name})
	if err != nil {
		return fail(err)
	}
	c.tree, c.digest, c.plugin = v.Tree, v.Digest, v.Plugin
	c.reasons, c.codeFiles, c.model = v.CodeReasons, v.CodeFiles, v.Model
	return c, nil
}

// Checked is a plugin folder that passed the checks of an install: the tree
// checks, the content digest, the parse and the classification.
type Checked struct {
	Tree        content.Tree
	Digest      string
	Plugin      *claudeplugin.Plugin
	CodeReasons []string
	CodeFiles   []string
	Model       Model
}

// CheckDir runs on a plugin folder the checks an install runs after the
// fetch (spec 9.3 step 2 and 15.3). The server and `local-ai plugins
// validate` share it, so a plugin that validates is one the server accepts.
func CheckDir(dir string, lim content.Limits, opts claudeplugin.LoadOptions) (Checked, error) {
	var v Checked
	var err error
	if v.Tree, err = content.Check(dir, lim); err != nil {
		return Checked{}, err
	}
	if v.Plugin, err = claudeplugin.Load(dir, opts); err != nil {
		return Checked{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := checkPluginName(v.Plugin); err != nil {
		return Checked{}, err
	}
	v.CodeReasons = CodeReasons(v.Plugin)
	v.CodeFiles = content.CodeFiles(v.Tree, v.Plugin)
	v.Model = ModelOf(v.Plugin)
	if v.Tree.Manifest, err = manifestHash(v); err != nil {
		return Checked{}, err
	}
	v.Digest = content.Digest(v.Tree)
	return v, nil
}

// manifestHash returns the SHA-256 of what the plugin contributes: the
// model, the code reasons and the code files. The marketplace entry is part
// of the manifest (claudeplugin.Load merges it), so the package row of one
// entry never serves an install of another entry with the same files. The
// install job computes it again and refuses a digest other than the
// reviewed one, which also catches an entry that changed after the review.
func manifestHash(v Checked) (string, error) {
	b, err := json.Marshal(struct {
		Model       Model                             `json:"model"`
		CodeReasons []string                          `json:"codeReasons"`
		CodeFiles   []string                          `json:"codeFiles"`
		LSP         map[string]claudeplugin.LSPServer `json:"lsp,omitempty"`
		Styles      []string                          `json:"outputStyles,omitempty"`
	}{v.Model, v.CodeReasons, v.CodeFiles, v.Plugin.LSPServers, relPaths(v.Plugin.Root, v.Plugin.OutputStyles)})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// relPaths makes absolute paths under root relative, so that the hash does
// not depend on the scratch folder of the fetch.
func relPaths(root string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if r, err := filepath.Rel(root, p); err == nil {
			p = filepath.ToSlash(r)
		}
		out = append(out, p)
	}
	return out
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
// inspect, within the limits of ratelimit.go. The files go into the workspace store under their digest, so
// that every frontend can show them for review; a package that no install
// takes is collected after 24 hours.
func (s *Service) Inspect(ctx context.Context, a Actor, pluginID string) (Preview, error) {
	if a.ID == "" {
		return Preview{}, ErrForbidden
	}
	release, err := s.inspects.take(a.ID, a.Admin, s.now())
	if err != nil {
		return Preview{}, err
	}
	defer release()
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
	// The last review is shown on the catalog to everyone: what a version
	// contributes is a fact of public content. A failure only loses the
	// hint.
	if mb, merr := json.Marshal(c.model); merr == nil {
		if err := s.st.SaveInspection(ctx, store.Inspection{
			PluginID: r.pluginID, SHA: c.sha, Digest: c.digest, RunsCode: p.RunsCode, CodeReasons: c.reasons,
			Hosts: p.Hosts, Model: mb, CheckedAt: s.now(),
		}); err != nil {
			xlog.Warn("plugins: keep the last review", "plugin", r.pluginID, "error", err)
		}
	}
	p.Token, p.ExpiresAt, err = s.newToken(ctx, store.Token{
		Kind: store.TokenInstall, Principal: a.ID, PluginID: r.pluginID, SHA: c.sha, Digest: c.digest,
		RunsCode: p.RunsCode, CodeFiles: c.codeFiles, Name: c.plugin.Name,
	})
	return p, err
}
