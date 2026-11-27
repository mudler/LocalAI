package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// Bounds of a catalog page.
const (
	DefaultCatalogLimit = 50
	MaxCatalogLimit     = 200
)

// CatalogQuery selects entries of the catalog. Empty fields match
// everything.
type CatalogQuery struct {
	// Q matches the name, description, category and tags, without case.
	Q           string
	Marketplace string
	Category    string
	// RunsCode keeps the entries known to run code (true) or known to run
	// none (false), from an install, the last review or the curated hint
	// (see CatalogItem.RunsCodeFrom); an unknown entry matches neither.
	RunsCode *bool
	Offset   int
	// Limit is DefaultCatalogLimit when 0, at most MaxCatalogLimit.
	Limit int
}

// InstallRef is an install of a catalog entry that the caller can see.
type InstallRef struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
	State string `json:"state"`
}

// CatalogItem is one entry of a marketplace as browse shows it.
type CatalogItem struct {
	store.Entry
	// PluginID is name@marketplace, the ID that inspect takes.
	PluginID      string `json:"plugin"`
	MarketplaceID string `json:"marketplace"`
	// RunsCode says whether the plugin runs code, from RunsCodeFrom; nil
	// when nothing says. Inspect tells for sure for the version it fetches.
	RunsCode *bool `json:"runsCode,omitempty"`
	// RunsCodeFrom is where RunsCode comes from: an install, the last
	// review (inspect) of the plugin, or the runsCode that the curated
	// marketplace claims (a hint; the review decides).
	RunsCodeFrom string `json:"runsCodeFrom,omitempty"`
	// Reviewed is the last inspect of the plugin by anyone: the version and
	// what it contributes.
	Reviewed *ReviewedVersion `json:"reviewed,omitempty"`
	// Installs are the installs of the plugin that the caller can see.
	Installs []InstallRef `json:"installs"`
}

// Sources of CatalogItem.RunsCode.
const (
	RunsCodeFromInstall = "install"
	RunsCodeFromReview  = "review"
	RunsCodeFromCurated = "curated"
)

// ReviewedVersion is the last inspect of a plugin.
type ReviewedVersion struct {
	SHA         string    `json:"sha"`
	Digest      string    `json:"digest"`
	RunsCode    bool      `json:"runsCode"`
	CodeReasons []string  `json:"codeReasons"`
	Hosts       []string  `json:"hosts,omitempty"`
	Model       Model     `json:"model"`
	CheckedAt   time.Time `json:"checkedAt"`
}

// CatalogPage is one page of the catalog.
type CatalogPage struct {
	Items  []CatalogItem `json:"items"`
	Total  int           `json:"total"`
	Offset int           `json:"offset"`
	Limit  int           `json:"limit"`
}

// Catalog lists the entries of the active marketplaces from their snapshots
// (spec 6.2). It reads the store on each call and fetches nothing.
func (s *Service) Catalog(ctx context.Context, a Actor, q CatalogQuery) (CatalogPage, error) {
	if a.ID == "" {
		return CatalogPage{}, ErrForbidden
	}
	if q.Offset < 0 || q.Limit < 0 {
		return CatalogPage{}, fmt.Errorf("%w: offset and limit must not be negative", ErrInvalid)
	}
	if q.Limit == 0 {
		q.Limit = DefaultCatalogLimit
	}
	q.Limit = min(q.Limit, MaxCatalogLimit)
	mkts, err := s.ListMarketplaces(ctx)
	if err != nil {
		return CatalogPage{}, err
	}
	known, err := s.catalogInstalls(ctx, a)
	if err != nil {
		return CatalogPage{}, err
	}
	needle := strings.ToLower(strings.TrimSpace(q.Q))
	var all []CatalogItem
	for _, m := range mkts {
		if !m.Active || (q.Marketplace != "" && m.ID != q.Marketplace) {
			continue
		}
		snap, err := s.st.Snapshot(ctx, m.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return CatalogPage{}, err
		}
		hints := curatedHints(m.Marketplace, snap)
		for _, e := range snap.Entries {
			it := known.item(m.ID, e, hints)
			if !catalogMatch(it, needle, q) {
				continue
			}
			all = append(all, it)
		}
	}
	page := CatalogPage{Items: []CatalogItem{}, Total: len(all), Offset: q.Offset, Limit: q.Limit}
	if q.Offset < len(all) {
		page.Items = all[q.Offset:min(len(all), q.Offset+q.Limit)]
	}
	return page, nil
}

// CatalogEntry returns one entry of an active marketplace.
func (s *Service) CatalogEntry(ctx context.Context, a Actor, marketplaceID, name string) (CatalogItem, error) {
	if a.ID == "" {
		return CatalogItem{}, ErrForbidden
	}
	m, err := s.Marketplace(ctx, marketplaceID)
	if err != nil {
		return CatalogItem{}, err
	}
	if !m.Active {
		return CatalogItem{}, fmt.Errorf("%w: marketplace %q is off", ErrNotFound, marketplaceID)
	}
	snap, err := s.st.Snapshot(ctx, marketplaceID)
	if errors.Is(err, store.ErrNotFound) {
		return CatalogItem{}, fmt.Errorf("%w: marketplace %q has not been read yet", ErrNotFound, marketplaceID)
	}
	if err != nil {
		return CatalogItem{}, err
	}
	known, err := s.catalogInstalls(ctx, a)
	if err != nil {
		return CatalogItem{}, err
	}
	hints := curatedHints(m.Marketplace, snap)
	for _, e := range snap.Entries {
		if e.Name == name {
			return known.item(marketplaceID, e, hints), nil
		}
	}
	return CatalogItem{}, fmt.Errorf("%w: %s@%s", ErrNotFound, name, marketplaceID)
}

// catalogKnown is what the installs and the reviews tell about the catalog
// entries.
type catalogKnown struct {
	// runsCode is per plugin ID, from any install of it.
	runsCode map[string]bool
	// visible are the installs the caller can see, per plugin ID.
	visible map[string][]InstallRef
	// reviewed is the last inspect of each plugin ID.
	reviewed map[string]*ReviewedVersion
}

// item builds a catalog item. RunsCode comes from an install first, then
// from the last review, then from the hint of the curated marketplace.
func (k catalogKnown) item(mktID string, e store.Entry, hints map[string]bool) CatalogItem {
	id := e.Name + "@" + mktID
	it := CatalogItem{Entry: e, PluginID: id, MarketplaceID: mktID, Installs: k.visible[id], Reviewed: k.reviewed[id]}
	if it.Installs == nil {
		it.Installs = []InstallRef{}
	}
	if rc, ok := k.runsCode[id]; ok {
		it.RunsCode, it.RunsCodeFrom = &rc, RunsCodeFromInstall
	} else if it.Reviewed != nil {
		rc := it.Reviewed.RunsCode
		it.RunsCode, it.RunsCodeFrom = &rc, RunsCodeFromReview
	} else if rc, ok := hints[e.Name]; ok {
		it.RunsCode, it.RunsCodeFrom = &rc, RunsCodeFromCurated
	}
	return it
}

// curatedHints reads metadata.localai.runsCode of each entry of the curated
// marketplace, which its maintainers review. Any other marketplace's claim
// is not used: anyone can write it.
func curatedHints(m store.Marketplace, snap store.Snapshot) map[string]bool {
	if m.ID != DefaultMarketplaceID || !m.Default {
		return nil
	}
	var doc struct {
		Plugins []struct {
			Name     string `json:"name"`
			Metadata struct {
				LocalAI struct {
					RunsCode *bool `json:"runsCode"`
				} `json:"localai"`
			} `json:"metadata"`
		} `json:"plugins"`
	}
	if json.Unmarshal(snap.Body, &doc) != nil {
		return nil
	}
	out := map[string]bool{}
	for _, p := range doc.Plugins {
		if p.Metadata.LocalAI.RunsCode != nil {
			out[p.Name] = *p.Metadata.LocalAI.RunsCode
		}
	}
	return out
}

// catalogInstalls reads every install and every review once. Whether a
// plugin runs code is not private, so it comes from any install or review;
// the install rows themselves are shown only when the caller can see them.
func (s *Service) catalogInstalls(ctx context.Context, a Actor) (catalogKnown, error) {
	list, err := s.st.Installs(ctx, store.InstallFilter{})
	if err != nil {
		return catalogKnown{}, err
	}
	k := catalogKnown{runsCode: map[string]bool{}, visible: map[string][]InstallRef{}, reviewed: map[string]*ReviewedVersion{}}
	for _, in := range list {
		k.runsCode[in.PluginID] = k.runsCode[in.PluginID] || in.RunsCode
		if visibleTo(a, in) {
			k.visible[in.PluginID] = append(k.visible[in.PluginID], InstallRef{ID: in.ID, Scope: in.Scope, State: in.State})
		}
	}
	reviews, err := s.st.Inspections(ctx)
	if err != nil {
		return catalogKnown{}, err
	}
	for _, r := range reviews {
		v := &ReviewedVersion{SHA: r.SHA, Digest: r.Digest, RunsCode: r.RunsCode, CodeReasons: r.CodeReasons, Hosts: r.Hosts, CheckedAt: r.CheckedAt}
		if len(r.Model) > 0 && json.Unmarshal(r.Model, &v.Model) != nil {
			continue
		}
		if v.CodeReasons == nil {
			v.CodeReasons = []string{}
		}
		k.reviewed[r.PluginID] = v
	}
	return k, nil
}

func catalogMatch(it CatalogItem, needle string, q CatalogQuery) bool {
	if q.Category != "" && !strings.EqualFold(it.Category, q.Category) {
		return false
	}
	if q.RunsCode != nil && (it.RunsCode == nil || *it.RunsCode != *q.RunsCode) {
		return false
	}
	if needle == "" {
		return true
	}
	if strings.Contains(strings.ToLower(it.Name), needle) ||
		strings.Contains(strings.ToLower(it.Description), needle) ||
		strings.Contains(strings.ToLower(it.Category), needle) {
		return true
	}
	return slices.ContainsFunc(it.Tags, func(t string) bool { return strings.Contains(strings.ToLower(t), needle) })
}

// visibleTo reports whether a can see an install: an administrator sees
// every install, a user the instance installs and their own.
func visibleTo(a Actor, in store.Install) bool {
	return a.Admin || in.Scope == store.ScopeInstance || (a.ID != "" && in.Owner == a.ID)
}
