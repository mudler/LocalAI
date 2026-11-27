package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/nib/plugin/claudeplugin"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// Short codes of Marketplace.LastError. They never carry a body or a URL.
const (
	ErrCodeFetch      = "fetch-failed"
	ErrCodeRefusedURL = "refused-url"
	ErrCodeTooLarge   = "too-large"
	ErrCodeParse      = "parse-failed"
	ErrCodeTooMany    = "too-many-entries"
)

// tickPeriod is how often the refresh loop looks for marketplaces that are
// due. A marketplace is refreshed once per refresh interval; a short period
// picks up a changed ref soon.
const tickPeriod = 10 * time.Minute

// snapshotError is a failed fetch or parse, with its short code.
type snapshotError struct {
	code string
	err  error
}

func (e *snapshotError) Error() string { return e.code + ": " + e.err.Error() }
func (e *snapshotError) Unwrap() error { return e.err }

func errorCode(err error) string {
	var se *snapshotError
	if errors.As(err, &se) {
		return se.code
	}
	return ErrCodeFetch
}

// fetchSnapshot fetches and parses the marketplace.json of m. It never
// writes.
func (s *Service) fetchSnapshot(ctx context.Context, m store.Marketplace) (store.Snapshot, *claudeplugin.Marketplace, error) {
	body, sha, err := s.fetcher.FetchMarketplace(ctx, m.Kind, m.Location, m.Ref)
	if err != nil {
		code := ErrCodeFetch
		switch {
		case errors.Is(err, fetch.ErrRefusedURL):
			code = ErrCodeRefusedURL
		case errors.Is(err, claudeplugin.ErrTooLarge):
			code = ErrCodeTooLarge
		}
		return store.Snapshot{}, nil, &snapshotError{code, err}
	}
	if len(body) > store.MaxSnapshotBody {
		return store.Snapshot{}, nil, &snapshotError{ErrCodeTooLarge, fmt.Errorf("marketplace.json is over %d bytes", store.MaxSnapshotBody)}
	}
	parsed, err := claudeplugin.ParseMarketplace(body)
	if err != nil {
		return store.Snapshot{}, nil, &snapshotError{ErrCodeParse, err}
	}
	if len(parsed.Plugins) > MaxEntries {
		return store.Snapshot{}, nil, &snapshotError{ErrCodeTooMany, fmt.Errorf("%d entries, at most %d", len(parsed.Plugins), MaxEntries)}
	}
	// LocalAI never loads a marketplace from disk (LoadMarketplace runs git
	// when it finds .git); it sets the commit itself.
	parsed.SHA = sha
	sum := sha256.Sum256(body)
	snap := store.Snapshot{
		MarketplaceID: m.ID, SHA: sha, Body: body, BodySHA256: hex.EncodeToString(sum[:]),
		FetchedAt: s.now(),
	}
	for _, e := range parsed.Plugins {
		snap.Entries = append(snap.Entries, normalizeEntry(m.Kind, e))
	}
	for _, sk := range parsed.Skipped {
		snap.Skipped = append(snap.Skipped, store.Skipped{Component: sk.Component, Name: sk.Name, Reason: string(sk.Code)})
	}
	return snap, parsed, nil
}

// normalizeEntry gives the stored form of an entry, with whether LocalAI can
// fetch it.
func normalizeEntry(kind string, e claudeplugin.Entry) store.Entry {
	src, _ := json.Marshal(e.Source)
	out := store.Entry{
		Name: e.Name, Source: src, Description: e.Description, Version: e.Version,
		Category: e.Category, Tags: e.Tags, Homepage: e.Homepage, Strict: e.Strict, Supported: true,
	}
	if e.Author != nil {
		out.Author = e.Author.Name
	}
	switch {
	case e.Source.Supported() != nil:
		out.Supported, out.Reason = false, "unsupported-source:"+string(e.Source.Kind)
	case e.Source.Kind == claudeplugin.SourceRelative && kind == store.KindURL:
		out.Supported, out.Reason = false, "relative-source-in-url-marketplace"
	}
	return out
}

// ParsedSnapshot parses the body of a stored snapshot again, for the install
// path, and sets its commit. Parsing is pure; it runs no git.
func ParsedSnapshot(snap store.Snapshot) (*claudeplugin.Marketplace, error) {
	m, err := claudeplugin.ParseMarketplace(snap.Body)
	if err != nil {
		return nil, err
	}
	m.SHA = snap.SHA
	return m, nil
}

// RefreshMarketplace refreshes one marketplace now (an administrator's
// request). A failure keeps the old snapshot and records the short code in
// LastError.
func (s *Service) RefreshMarketplace(ctx context.Context, a Actor, id string) (MarketplaceView, error) {
	if !a.Admin {
		return MarketplaceView{}, ErrForbidden
	}
	m, err := s.marketplace(ctx, id)
	if err != nil {
		return MarketplaceView{}, err
	}
	m, err = s.refreshOne(ctx, m)
	if err != nil && m.ID == "" {
		return MarketplaceView{}, err
	}
	return s.view(m), err
}

// refreshOne fetches m, replaces its snapshot when the parse succeeds, and
// marks the installs that have a newer version. It returns the marketplace
// as saved.
func (s *Service) refreshOne(ctx context.Context, m store.Marketplace) (store.Marketplace, error) {
	old, err := s.st.Snapshot(ctx, m.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Marketplace{}, err
	}
	snap, parsed, ferr := s.fetchSnapshot(ctx, m)

	// Read the record again: an administrator may have changed or removed
	// it while the fetch ran, and the refresh must not bring it back.
	cur, err := s.marketplace(ctx, m.ID)
	if err != nil {
		return store.Marketplace{}, err
	}
	cur.RefreshedAt = s.now()
	if ferr != nil {
		cur.LastError = errorCode(ferr)
		xlog.Warn("plugins: marketplace refresh failed", "marketplace", m.ID, "code", cur.LastError)
		if err := s.st.SaveMarketplace(ctx, cur); err != nil {
			return cur, err
		}
		return cur, ferr
	}
	cur.LastError, cur.Name, cur.HeadSHA = "", parsed.Name, snap.SHA
	// A url marketplace has no commit: the SHA-256 of the body tells a
	// change.
	if old.BodySHA256 != snap.BodySHA256 || old.SHA != snap.SHA {
		if err := s.st.SaveSnapshot(ctx, snap); err != nil {
			return cur, err
		}
	}
	if err := s.st.SaveMarketplace(ctx, cur); err != nil {
		return cur, err
	}
	if err := s.markUpdates(ctx, cur, snap, parsed); err != nil {
		xlog.Warn("plugins: update check failed", "marketplace", m.ID, "error", err)
	}
	return cur, nil
}

// targetSHA is the commit an entry points at now: its pinned sha, the
// snapshot's commit for a relative source, or its ref resolved with
// ls-remote.
func (s *Service) targetSHA(ctx context.Context, m store.Marketplace, snap store.Snapshot, e *claudeplugin.Entry) (string, error) {
	switch {
	case e.Source.Supported() != nil:
		return "", e.Source.Supported()
	case e.Source.SHA != "":
		return e.Source.SHA, nil
	case e.Source.Kind == claudeplugin.SourceRelative:
		if snap.SHA == "" {
			return "", errors.New("a relative source needs a git marketplace")
		}
		return snap.SHA, nil
	}
	return s.fetcher.ResolveRef(ctx, e.Source.GitURL(), e.Source.Ref)
}

// markUpdates sets update_available on the installs of the marketplace whose
// entry points at another commit now, and clears it when the entry points
// back at the installed commit. It never installs anything (spec 9.4).
func (s *Service) markUpdates(ctx context.Context, m store.Marketplace, snap store.Snapshot, parsed *claudeplugin.Marketplace) error {
	installs, err := s.st.Installs(ctx, store.InstallFilter{})
	if err != nil {
		return err
	}
	suffix := "@" + m.ID
	resolved := map[string]string{}
	var errs []error
	for _, in := range installs {
		name, ok := strings.CutSuffix(in.PluginID, suffix)
		if !ok {
			continue
		}
		target, seen := resolved[name]
		if !seen {
			e, ferr := parsed.Find(name)
			if ferr != nil {
				resolved[name] = ""
				continue
			}
			if target, err = s.targetSHA(ctx, m, snap, e); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", in.PluginID, err))
				resolved[name] = ""
				continue
			}
			resolved[name] = target
		}
		if target == "" {
			continue
		}
		if err := s.st.InstallTx(ctx, func(tx store.Tx) error {
			cur, err := tx.Installs(store.InstallFilter{ID: in.ID})
			if err != nil || len(cur) == 0 {
				return err
			}
			c := cur[0]
			switch {
			case target == c.SourceSHA:
				// The entry points at the installed commit again: neither an
				// update nor a blocked update waits any more.
				if c.State != store.StateUpdateAvailable && c.State != store.StateBlocked {
					return nil
				}
				c.State, c.UpdateSHA, c.UpdateDigest = store.StateInstalled, "", ""
			case target == c.UpdateSHA:
				return nil // already known, or blocked at that version
			default:
				c.State, c.UpdateSHA, c.UpdateDigest = store.StateUpdateAvailable, target, ""
			}
			c.UpdatedAt = s.now()
			return tx.UpdateInstall(c)
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// due reports whether the tick refreshes m.
func (s *Service) due(m store.Marketplace, now time.Time) bool {
	return !m.Removed && m.Enabled && !(m.Default && !s.official()) && !now.Before(m.RefreshedAt.Add(s.interval))
}

// TickResult says what one refresh tick did.
type TickResult struct {
	// Ran is false when another frontend held the lock.
	Ran       bool
	Refreshed []string
	Failed    []string
	GC        content.GCResult
}

// RefreshTick refreshes the marketplaces that are due, deletes the expired
// preview tokens, then deletes the packages that no install uses. One frontend runs it at a time, under
// advisorylock.KeyPluginsRefresh; the due check makes a second tick in the
// same interval do nothing, so leadership need not be sticky.
func (s *Service) RefreshTick(ctx context.Context) (TickResult, error) {
	var res TickResult
	run := func() error {
		res.Ran = true
		all, err := s.st.Marketplaces(ctx)
		if err != nil {
			return err
		}
		now := s.now()
		for _, m := range all {
			if !s.due(m, now) {
				continue
			}
			if _, err := s.refreshOne(ctx, m); err != nil {
				res.Failed = append(res.Failed, m.ID)
				continue
			}
			res.Refreshed = append(res.Refreshed, m.ID)
		}
		if _, err := s.st.DeleteExpiredTokens(ctx, now); err != nil {
			xlog.Warn("plugins: delete expired preview tokens", "error", err)
		}
		if s.ws.Files == nil || s.root == "" {
			return nil
		}
		// The lock is held: Collect, not GC, which would take it again.
		res.GC, err = content.Collect(ctx, s.ws, s.root, s.st, now)
		return err
	}
	if s.db == nil {
		return res, run()
	}
	_, err := advisorylock.TryWithLockCtx(ctx, s.db, advisorylock.KeyPluginsRefresh, run)
	return res, err
}

// Run refreshes the marketplaces in the background until ctx ends: one tick
// at start and one every few minutes, each refreshing only what is due.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(tickPeriod)
	defer t.Stop()
	for {
		if _, err := s.RefreshTick(ctx); err != nil && ctx.Err() == nil {
			xlog.Warn("plugins: refresh tick failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
