package pages

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

// Consent states of a shared page for its viewer.
const (
	StateNone    = "none"    // never allowed, or the consent was revoked
	StateAllowed = "allowed" // the viewer allowed this version
	StateChanged = "changed" // the viewer allowed an earlier version
)

// ErrChanged means the page changed between the review and the consent: the
// viewer must look at the new version first.
var ErrChanged = errors.New("pages: the page changed while you were reviewing it; review it again")

// Trust keeps the consents of the viewers of shared pages and checks them on
// every call of a shared page's bridge, so a consent that is revoked, in this
// tab or anywhere else, stops an open page at its next call.
type Trust struct {
	pages *Service
	store ConsentStore
	now   func() time.Time
}

// NewTrust returns the trust of the shared pages of svc, kept in store.
func NewTrust(svc *Service, store ConsentStore) *Trust {
	return &Trust{pages: svc, store: store, now: time.Now}
}

// WithNow sets the clock, for tests.
func (t *Trust) WithNow(now func() time.Time) *Trust { t.now = now; return t }

// Review is what the viewer allowed of a shared page, against what it is now.
type Review struct {
	State   string
	Allowed *Consent // the lasting consent, when there is one
}

// State says whether the viewer allowed the current version of p.
func (t *Trust) State(ctx context.Context, principal string, p Page) (Review, error) {
	c, ok, err := t.store.Always(ctx, principal, p.PageID)
	if err != nil {
		return Review{}, err
	}
	switch {
	case !ok:
		return Review{State: StateNone}, nil
	case c.Hash == p.Hash:
		return Review{State: StateAllowed, Allowed: &c}, nil
	}
	return Review{State: StateChanged, Allowed: &c}, nil
}

// Allow records the viewer's consent to the version hash of a shared page,
// for good (once false) or for one visit (once true, whose token the result
// carries). The hash must be the page's current one.
func (t *Trust) Allow(ctx context.Context, principal string, folderID int64, name, hash string, once bool) (Consent, error) {
	p, html, err := t.pages.SharedSource(ctx, principal, folderID, name)
	if err != nil {
		return Consent{}, err
	}
	if hash == "" || hash != p.Hash {
		return Consent{}, ErrChanged
	}
	now := t.now().UTC()
	c := Consent{Principal: principal, PageID: p.PageID, Folder: p.Folder, Name: p.Name, Hash: p.Hash, Kind: ConsentAlways,
		Size: p.Size, MetaSize: p.MetaSize, Lines: strings.Count(html, "\n") + 1, At: now}
	action := "allow"
	if once {
		c.Kind, c.Token, c.Expires, action = ConsentOnce, NewToken(), now.Add(OnceTTL), "allow_once"
	}
	ev := ConsentEvent{Principal: principal, PageID: p.PageID, Folder: p.Folder, Name: p.Name, Hash: p.Hash, Action: action, At: now}
	if err := t.store.Put(ctx, c, ev); err != nil {
		return Consent{}, err
	}
	xlog.Info("pages: a shared page was allowed", "user", principal, "folder", p.Folder, "page", p.Name, "hash", p.Hash, "kind", c.Kind)
	return c, nil
}

// Revoke removes the viewer's consents to a shared page. The page's open
// frames stop at their next call of the bridge.
func (t *Trust) Revoke(ctx context.Context, principal string, folderID int64, name string) error {
	p, err := t.pages.GetShared(ctx, principal, folderID, name)
	if err != nil {
		return err
	}
	ev := ConsentEvent{Principal: principal, PageID: p.PageID, Folder: p.Folder, Name: p.Name, Hash: p.Hash, Action: "revoke", At: t.now().UTC()}
	if err := t.store.Revoke(ctx, principal, p.PageID, ev); err != nil {
		return err
	}
	xlog.Info("pages: a shared page is no longer allowed", "user", principal, "folder", p.Folder, "page", p.Name)
	return nil
}

// Check says whether a frame that runs the version hash of a shared page may
// call the bridge: the viewer allowed that version of that page, for good or
// for the visit token. A frame keeps the version it loaded, so a page that its
// author changed while it is open goes on until it is reloaded, and a consent
// for the code of another page, even the same code, never counts.
func (t *Trust) Check(ctx context.Context, principal string, folderID int64, name, hash, token string) error {
	p, err := t.pages.GetShared(ctx, principal, folderID, name)
	if err != nil {
		return err
	}
	if hash == "" {
		return ErrNoConsent
	}
	if token != "" {
		c, ok, err := t.store.Once(ctx, principal, token)
		if err != nil {
			return err
		}
		if ok && c.PageID == p.PageID && c.Hash == hash && t.now().Before(c.Expires) {
			return nil
		}
	}
	c, ok, err := t.store.Always(ctx, principal, p.PageID)
	if err != nil {
		return err
	}
	if ok && c.Hash == hash {
		return nil
	}
	return ErrNoConsent
}

// Events returns the audit of a viewer's consents, newest first.
func (t *Trust) Events(ctx context.Context, principal string, limit int) ([]ConsentEvent, error) {
	return t.store.Events(ctx, principal, limit)
}
