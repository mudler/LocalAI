package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// MaxDiffBytes caps the unified diffs of the code files of an update
// preview, in all.
const MaxDiffBytes = 256 << 10

// ErrBlocked refuses an update of a user install whose new version runs
// code. The install keeps its version with the state blocked.
var ErrBlocked = errors.New("plugins: this version runs code. An administrator must install it")

// ErrNoUpdate means the install is at the commit its entry points at.
var ErrNoUpdate = errors.New("plugins: no update available")

// FileChange is one file that an update adds, removes or changes.
type FileChange struct {
	Path string `json:"path"`
	// OldLines and NewLines count the lines of each side; 0 for a missing
	// side.
	OldLines int  `json:"oldLines"`
	NewLines int  `json:"newLines"`
	Code     bool `json:"code"`
}

// FileDiff is the unified diff of one changed code file.
type FileDiff struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}

// UpdatePreview is the review of an update (spec 9.4).
type UpdatePreview struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expiresAt"`
	InstallID  string    `json:"install"`
	PluginID   string    `json:"plugin"`
	FromSHA    string    `json:"fromSha"`
	ToSHA      string    `json:"toSha"`
	FromDigest string    `json:"fromDigest"`
	ToDigest   string    `json:"toDigest"`

	Added   []FileChange `json:"added"`
	Removed []FileChange `json:"removed"`
	Changed []FileChange `json:"changed"`
	// Diffs are the unified diffs of the changed code files, up to
	// MaxDiffBytes in all; Truncated says that some were left out.
	Diffs     []FileDiff `json:"diffs"`
	Truncated bool       `json:"truncated"`

	OldCodeReasons []string `json:"oldCodeReasons"`
	NewCodeReasons []string `json:"newCodeReasons"`
	// NeedsCodeReview is set for an instance install whose code changed:
	// a code file, the code reasons or the code components. The approval
	// needs the administrator's browser session and a tick for each file of
	// CodeFiles.
	NeedsCodeReview bool     `json:"needsCodeReview"`
	CodeFiles       []string `json:"codeFiles"`
	// NewCodeFiles are the files that are code in the new version and were
	// not code before, although their bytes did not change: a hook of the
	// marketplace entry can start to run a file that was plain text. They
	// have no diff, so the review shows their full text. They are also in
	// CodeFiles.
	NewCodeFiles []string `json:"newCodeFiles"`
	// CodeItems are the code components that the new version adds or
	// changes (command hooks, stdio MCP servers, bin files), described for
	// the review. Some come from the marketplace entry and are in no file.
	CodeItems []string `json:"codeItems"`
}

func (s *Service) getInstall(ctx context.Context, id string) (store.Install, error) {
	list, err := s.st.Installs(ctx, store.InstallFilter{ID: id})
	if err != nil {
		return store.Install{}, err
	}
	if len(list) == 0 {
		return store.Install{}, fmt.Errorf("%w: install %q", ErrNotFound, id)
	}
	return list[0], nil
}

// canChange applies spec 9.2 and 9.4: the owner or an administrator changes
// a user install, an administrator an instance install.
func canChange(a Actor, in store.Install) error {
	switch {
	case a.ID == "":
		return ErrForbidden
	case a.Admin:
		return nil
	case in.Scope == store.ScopeUser && in.Owner == a.ID:
		return nil
	case in.Scope == store.ScopeUser:
		// Another user's install is not even visible.
		return fmt.Errorf("%w: install %q", ErrNotFound, in.ID)
	}
	return ErrForbidden
}

// PreviewUpdate fetches the new version of an install and returns the diff
// against the installed package with a preview token. A user install whose
// new version runs code is blocked: it keeps its version.
func (s *Service) PreviewUpdate(ctx context.Context, a Actor, installID string) (UpdatePreview, error) {
	in, err := s.getInstall(ctx, installID)
	if err != nil {
		return UpdatePreview{}, err
	}
	if err := canChange(a, in); err != nil {
		return UpdatePreview{}, err
	}
	r, err := s.resolveEntry(ctx, in.PluginID)
	if err != nil {
		return UpdatePreview{}, err
	}
	target := in.UpdateSHA
	if target == "" {
		if target, err = s.targetSHA(ctx, r.mkt, r.snap, r.entry); err != nil {
			return UpdatePreview{}, err
		}
	}
	if target == in.SourceSHA {
		return UpdatePreview{}, ErrNoUpdate
	}
	c, err := s.fetchChecked(ctx, r, target)
	if err != nil {
		return UpdatePreview{}, err
	}
	defer c.done()
	old, err := s.st.Package(ctx, in.Digest)
	if err != nil {
		return UpdatePreview{}, err
	}
	runsCode := len(c.reasons) > 0
	if in.Scope == store.ScopeUser && runsCode {
		if err := s.markUpdate(ctx, in.ID, store.StateBlocked, c.sha, c.digest); err != nil {
			return UpdatePreview{}, err
		}
		return UpdatePreview{}, ErrBlocked
	}
	if err := s.writePackage(ctx, c); err != nil {
		return UpdatePreview{}, err
	}
	p := UpdatePreview{
		InstallID: in.ID, PluginID: in.PluginID, FromSHA: in.SourceSHA, ToSHA: c.sha,
		FromDigest: in.Digest, ToDigest: c.digest, OldCodeReasons: old.CodeReasons, NewCodeReasons: c.reasons,
	}
	if err := s.diff(ctx, &p, old, c); err != nil {
		return UpdatePreview{}, err
	}
	oldModel, err := modelOf(old)
	if err != nil {
		return UpdatePreview{}, err
	}
	p.CodeItems = addedItems(CodeComponents(oldModel), CodeComponents(c.model))
	reasonsChanged := !slices.Equal(old.CodeReasons, c.reasons)
	p.NeedsCodeReview = in.Scope == store.ScopeInstance && (len(p.CodeFiles) > 0 || reasonsChanged || len(p.CodeItems) > 0)
	tokenFiles := []string{}
	if p.NeedsCodeReview {
		tokenFiles = p.CodeFiles
	}
	// The install points at the reviewed package, so the collection keeps
	// it until the update or the next refresh.
	if err := s.markUpdate(ctx, in.ID, store.StateUpdateAvailable, c.sha, c.digest); err != nil {
		return UpdatePreview{}, err
	}
	p.Token, p.ExpiresAt, err = s.newToken(ctx, store.Token{
		Kind: store.TokenUpdate, Principal: a.ID, PluginID: in.PluginID, InstallID: in.ID,
		SHA: c.sha, Digest: c.digest, RunsCode: runsCode, CodeFiles: tokenFiles, Review: p.NeedsCodeReview,
	})
	return p, err
}

// addedItems returns the items of next that are not in prev.
func addedItems(prev, next []string) []string {
	out := []string{}
	for _, it := range next {
		if !slices.Contains(prev, it) {
			out = append(out, it)
		}
	}
	return out
}

// markUpdate records the version a preview found on the install.
func (s *Service) markUpdate(ctx context.Context, installID, state, sha, digest string) error {
	return s.st.InstallTx(ctx, func(tx store.Tx) error {
		cur, err := tx.Installs(store.InstallFilter{ID: installID})
		if err != nil || len(cur) == 0 {
			return err
		}
		c := cur[0]
		c.State, c.UpdateSHA, c.UpdateDigest, c.UpdatedAt = state, sha, digest, s.now()
		return tx.UpdateInstall(c)
	})
}

// diff fills the file lists and the code diffs of p. The changed code
// files are those that are code in either version.
func (s *Service) diff(ctx context.Context, p *UpdatePreview, old store.Package, c *checked) error {
	oldFiles := map[string]store.File{}
	for _, f := range old.Files {
		oldFiles[f.Path] = f
	}
	newFiles := map[string]store.File{}
	for _, f := range c.files() {
		newFiles[f.Path] = f
	}
	isCode := map[string]bool{}
	for _, f := range old.CodeFiles {
		isCode[f] = true
	}
	for _, f := range c.codeFiles {
		isCode[f] = true
	}
	readOld := func(path string) ([]byte, error) {
		if s.ws.Files == nil {
			return nil, nil
		}
		rc, err := content.Open(ctx, s.ws, s.root, old.Digest, path)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 5<<20))
	}
	readNew := func(path string) ([]byte, error) {
		return os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(path)))
	}
	size := 0
	addDiff := func(path string, a, b []byte) {
		d, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A: difflib.SplitLines(string(a)), B: difflib.SplitLines(string(b)),
			FromFile: "a/" + path, ToFile: "b/" + path, Context: 3,
		})
		if size+len(d) > MaxDiffBytes {
			p.Truncated = true
			return
		}
		size += len(d)
		p.Diffs = append(p.Diffs, FileDiff{Path: path, Diff: d})
	}
	wasCode := map[string]bool{}
	for _, f := range old.CodeFiles {
		wasCode[f] = true
	}
	for _, f := range c.files() {
		o, ok := oldFiles[f.Path]
		if ok && o.SHA256 == f.SHA256 && o.Mode == f.Mode {
			if isCode[f.Path] && !wasCode[f.Path] {
				p.NewCodeFiles = append(p.NewCodeFiles, f.Path)
				p.CodeFiles = append(p.CodeFiles, f.Path)
			}
			continue
		}
		nb, err := readNew(f.Path)
		if err != nil {
			return err
		}
		ch := FileChange{Path: f.Path, NewLines: lines(nb), Code: isCode[f.Path]}
		var ob []byte
		if ok {
			if ob, err = readOld(f.Path); err != nil {
				return err
			}
			ch.OldLines = lines(ob)
			p.Changed = append(p.Changed, ch)
		} else {
			p.Added = append(p.Added, ch)
		}
		if ch.Code {
			p.CodeFiles = append(p.CodeFiles, f.Path)
			addDiff(f.Path, ob, nb)
		}
	}
	for _, f := range old.Files {
		if _, ok := newFiles[f.Path]; ok {
			continue
		}
		ch := FileChange{Path: f.Path, Code: isCode[f.Path]}
		ob, err := readOld(f.Path)
		if err != nil {
			return err
		}
		ch.OldLines = lines(ob)
		p.Removed = append(p.Removed, ch)
		if ch.Code {
			p.CodeFiles = append(p.CodeFiles, f.Path)
			addDiff(f.Path, ob, nil)
		}
	}
	slices.Sort(p.CodeFiles)
	slices.Sort(p.NewCodeFiles)
	return nil
}

func lines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// Update confirms an update preview and queues the update. An instance
// install whose code changed needs the administrator's browser session and
// approvedFiles that name every changed code file.
func (s *Service) Update(ctx context.Context, a Actor, installID, secret string, approvedFiles []string) (string, error) {
	t, err := s.PreviewToken(ctx, a, secret)
	if err != nil {
		return "", err
	}
	if t.Kind != store.TokenUpdate || t.InstallID != installID {
		return "", ErrTokenInvalid
	}
	in, err := s.getInstall(ctx, installID)
	if err != nil {
		return "", err
	}
	if err := canChange(a, in); err != nil {
		return "", err
	}
	if in.Scope == store.ScopeUser && t.RunsCode {
		return "", ErrBlocked
	}
	if t.Review || len(t.CodeFiles) > 0 {
		if !a.HumanSurface {
			return "", ErrNotHumanSurface
		}
		for _, f := range t.CodeFiles {
			if !slices.Contains(approvedFiles, f) {
				return "", fmt.Errorf("%w: approve each changed code file (missing %s)", ErrInvalid, f)
			}
		}
	}
	if t, err = s.takeToken(ctx, a, secret, store.TokenUpdate); err != nil {
		return "", err
	}
	id := newJobID()
	s.queue.Enqueue(Job{ID: id, PluginID: t.PluginID, Update: true, By: a.ID, Run: func(ctx context.Context) error {
		return s.updateJob(ctx, a, t)
	}})
	return id, nil
}

func (s *Service) updateJob(ctx context.Context, a Actor, t store.Token) error {
	release, err := s.lockPlugin(ctx, t.PluginID)
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	r, err := s.resolveEntry(ctx, t.PluginID)
	if err != nil {
		return err
	}
	c, err := s.fetchChecked(ctx, r, t.SHA)
	if err != nil {
		return err
	}
	defer c.done()
	if c.digest != t.Digest {
		return ErrDigestMismatch
	}
	if err := s.writePackage(ctx, c); err != nil {
		return err
	}
	pkg, err := s.packageOf(r, c)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runsCode := len(c.reasons) > 0
	reenable := false
	if err := s.st.InstallTx(ctx, func(tx store.Tx) error {
		reenable = false
		cur, err := tx.Installs(store.InstallFilter{ID: t.InstallID})
		if err != nil {
			return err
		}
		if len(cur) == 0 {
			return fmt.Errorf("%w: install %q", ErrNotFound, t.InstallID)
		}
		in := cur[0]
		if in.UpdateDigest != t.Digest {
			// Another preview or update moved the install since this
			// review.
			return fmt.Errorf("%w: the install changed since the review", store.ErrConflict)
		}
		if in.Scope == store.ScopeUser && runsCode {
			return ErrBlocked
		}
		old, err := tx.Package(in.Digest)
		if err != nil {
			return err
		}
		if err := tx.PutPackage(pkg); err != nil {
			return err
		}
		now := s.now()
		in.PreviousDigest, in.Digest, in.SourceSHA, in.RunsCode = in.Digest, c.digest, c.sha, runsCode
		in.ApprovedBy, in.ApprovedAt, in.ApprovedDigest = a.ID, now, c.digest
		in.UpdateSHA, in.UpdateDigest, in.State, in.UpdatedAt = "", "", store.StateInstalled, now
		if err := tx.UpdateInstall(in); err != nil {
			return err
		}
		if addsCodeReason(old.CodeReasons, c.reasons) {
			reenable = true
			return tx.MarkNeedsReenable(in.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	s.followConnectors(ctx, t.InstallID, pkg, reenable)
	return s.ensurePackageFiles(ctx, c)
}

// Rollback sets the previous package of an install again, with the rights
// of an update. The version rolled back from becomes the available update.
func (s *Service) Rollback(ctx context.Context, a Actor, installID string) error {
	in, err := s.getInstall(ctx, installID)
	if err != nil {
		return err
	}
	if err := canChange(a, in); err != nil {
		return err
	}
	if in.PreviousDigest == "" {
		return fmt.Errorf("%w: the install has no previous version", ErrInvalid)
	}
	release, err := s.lockPlugin(ctx, in.PluginID)
	if err != nil {
		return err
	}
	defer release()
	prev, err := s.st.Package(ctx, in.PreviousDigest)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: the previous version is gone", ErrNotFound)
	}
	if err != nil {
		return err
	}
	if s.ws.Files != nil {
		if ok, err := content.Exists(ctx, s.ws, s.root, prev.Digest); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: the files of the previous version are gone", ErrNotFound)
		}
	}
	prevRuns := len(prev.CodeReasons) > 0
	if in.Scope == store.ScopeUser && prevRuns {
		return ErrBlocked
	}
	if in.Scope == store.ScopeInstance && (prevRuns || in.RunsCode) && !a.HumanSurface {
		return ErrNotHumanSurface
	}
	reenable := false
	if err := s.st.InstallTx(ctx, func(tx store.Tx) error {
		reenable = false
		cur, err := tx.Installs(store.InstallFilter{ID: installID})
		if err != nil || len(cur) == 0 {
			return err
		}
		c := cur[0]
		if c.PreviousDigest != prev.Digest {
			return fmt.Errorf("%w: the install changed", store.ErrConflict)
		}
		now := s.now()
		curPkg, err := tx.Package(c.Digest)
		if err != nil {
			return err
		}
		c.UpdateSHA, c.UpdateDigest, c.State = c.SourceSHA, c.Digest, store.StateUpdateAvailable
		c.Digest, c.PreviousDigest, c.SourceSHA, c.RunsCode = prev.Digest, "", prev.SourceSHA, prevRuns
		c.ApprovedBy, c.ApprovedAt, c.ApprovedDigest, c.UpdatedAt = a.ID, now, prev.Digest, now
		if err := tx.UpdateInstall(c); err != nil {
			return err
		}
		if addsCodeReason(curPkg.CodeReasons, prev.CodeReasons) {
			reenable = true
			return tx.MarkNeedsReenable(c.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	s.followConnectors(ctx, installID, prev, reenable)
	return nil
}

// Remove deletes an install and its user states (spec 9.5). The package
// goes at the next collection when nothing else uses it.
func (s *Service) Remove(ctx context.Context, a Actor, installID string) error {
	in, err := s.getInstall(ctx, installID)
	if err != nil {
		return err
	}
	if err := canChange(a, in); err != nil {
		return err
	}
	release, err := s.lockPlugin(ctx, in.PluginID)
	if err != nil {
		return err
	}
	defer release()
	if err := s.st.InstallTx(ctx, func(tx store.Tx) error {
		if err := tx.DeleteInstall(installID); errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: install %q", ErrNotFound, installID)
		} else if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	s.dropConnectors(ctx, installID)
	return nil
}
