package plugins

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/plugins/fetch"
	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// MarketplaceView is a marketplace as the API lists it.
type MarketplaceView struct {
	store.Marketplace
	// Suspended is set for a default marketplace while the "official
	// libraries" switch is off. It is not read until the switch is on.
	Suspended bool `json:"suspended"`
	// Active is the effective state: enabled and not suspended.
	Active bool `json:"active"`
}

var ownerRepoRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ParseSource reads the one string an administrator types to add a
// marketplace (spec 8.1): owner/repo, an https git URL, or an https URL of a
// marketplace.json file. Everything else is refused.
func ParseSource(input string) (kind, location string, err error) {
	in := strings.TrimSpace(input)
	if ownerRepoRE.MatchString(in) && !strings.Contains(in, "..") {
		return store.KindGitHub, strings.TrimSuffix(in, ".git"), nil
	}
	u, perr := url.Parse(in)
	if perr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", "", fmt.Errorf("%w: %w: use owner/repo or an https URL", ErrInvalid, fetch.ErrRefusedURL)
	}
	if strings.HasSuffix(u.Path, ".json") {
		return store.KindURL, u.String(), nil
	}
	return store.KindGit, u.String(), nil
}

var idCleanRE = regexp.MustCompile(`[^a-z0-9-]+`)

// suggestID turns a name into a marketplace ID: lowercase, other
// characters become "-", starting with a letter. It returns "" when nothing
// usable is left.
func suggestID(name string) string {
	id := idCleanRE.ReplaceAllString(strings.ToLower(name), "-")
	id = strings.Trim(id, "-")
	for id != "" && (id[0] < 'a' || id[0] > 'z') {
		id = strings.TrimLeft(id[1:], "-")
	}
	if len(id) > 64 {
		id = strings.TrimRight(id[:64], "-")
	}
	if !store.ValidMarketplaceID(id) {
		return ""
	}
	return id
}

// locationID gives the ID of a marketplace from its location alone, for the
// flag defaults that are seeded without a fetch: the repository name, or for
// a marketplace.json URL the folder that holds .claude-plugin.
func locationID(kind, location string) string {
	switch kind {
	case store.KindGitHub:
		return suggestID(path.Base(location))
	case store.KindGit:
		u, err := url.Parse(location)
		if err != nil {
			return ""
		}
		return suggestID(strings.TrimSuffix(path.Base(u.Path), ".git"))
	case store.KindURL:
		u, err := url.Parse(location)
		if err != nil {
			return ""
		}
		dir := path.Dir(u.Path)
		if path.Base(dir) == ".claude-plugin" {
			dir = path.Dir(dir)
		}
		if id := suggestID(path.Base(dir)); id != "" {
			return id
		}
		return suggestID(u.Hostname())
	}
	return ""
}

// ParseDefaults reads the --plugin-marketplaces flag: comma-separated
// sources, each optionally "id=source". An entry gets the ID of its
// location when it names none.
func ParseDefaults(flag string) ([]DefaultMarketplace, error) {
	var out []DefaultMarketplace
	seen := map[string]bool{}
	for _, item := range strings.Split(flag, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, src, named := strings.Cut(item, "=")
		if !named || strings.Contains(id, "/") || strings.Contains(id, ":") {
			id, src = "", item
		}
		kind, loc, err := ParseSource(src)
		if err != nil {
			return nil, fmt.Errorf("plugin marketplace %q: %w", item, err)
		}
		if id == "" {
			id = locationID(kind, loc)
		}
		if !store.ValidMarketplaceID(id) {
			return nil, fmt.Errorf("plugin marketplace %q: %w: give an ID as id=source", item, ErrInvalid)
		}
		if seen[id] {
			return nil, fmt.Errorf("plugin marketplace %q: %w: the ID %q is used twice", item, ErrInvalid, id)
		}
		seen[id] = true
		out = append(out, DefaultMarketplace{ID: id, Kind: kind, Location: loc})
	}
	return out, nil
}

// Seed adds the default marketplaces to a fresh store: one that has no
// marketplace at all, tombstones included. A store that has any record keeps
// its records, so a removed default stays removed and the flag does not add
// sources after the first start; only the location of the curated
// marketplace follows the version of the build (followCuratedURL). Two
// frontends that seed at once save the same records.
func (s *Service) Seed(ctx context.Context) error {
	all, err := s.st.Marketplaces(ctx)
	if err != nil {
		return err
	}
	if len(all) > 0 {
		return s.followCuratedURL(ctx, all)
	}
	now := s.now()
	for _, d := range s.defaults {
		m := store.Marketplace{
			ID: d.ID, Name: d.ID, Kind: d.Kind, Location: d.Location, Ref: d.Ref,
			Enabled: true, Default: true, CreatedBy: "system", CreatedAt: now, UpdatedAt: now,
		}
		if err := s.st.SaveMarketplace(ctx, m); err != nil {
			return fmt.Errorf("seed marketplace %s: %w", d.ID, err)
		}
	}
	return nil
}

// followCuratedURL points the curated default marketplace at the file of
// this build: a release reads its own tag (spec 8.3), so after an upgrade it
// must not keep reading the tag of the version that first started. Only the
// curated default changes, while it is not removed and still reads a curated
// URL; a store seeded from --plugin-marketplaces is left alone.
func (s *Service) followCuratedURL(ctx context.Context, all []store.Marketplace) error {
	if !s.curated {
		return nil
	}
	want := CuratedMarketplaceURL(s.version)
	for _, m := range all {
		if m.ID != DefaultMarketplaceID || !m.Default || m.Removed || m.Kind != store.KindURL ||
			!isCuratedURL(m.Location) || m.Location == want {
			continue
		}
		m.Location, m.UpdatedAt = want, s.now()
		if err := s.st.SaveMarketplace(ctx, m); err != nil {
			return fmt.Errorf("move the curated marketplace to %s: %w", want, err)
		}
	}
	return nil
}

func (s *Service) view(m store.Marketplace) MarketplaceView {
	suspended := m.Default && !s.official()
	return MarketplaceView{Marketplace: m, Suspended: suspended, Active: m.Enabled && !suspended && !m.Removed}
}

// ListMarketplaces lists the marketplaces, without tombstones.
func (s *Service) ListMarketplaces(ctx context.Context) ([]MarketplaceView, error) {
	all, err := s.st.Marketplaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MarketplaceView, 0, len(all))
	for _, m := range all {
		if !m.Removed {
			out = append(out, s.view(m))
		}
	}
	return out, nil
}

// Marketplace returns one marketplace. A tombstone is not found.
func (s *Service) Marketplace(ctx context.Context, id string) (MarketplaceView, error) {
	m, err := s.marketplace(ctx, id)
	if err != nil {
		return MarketplaceView{}, err
	}
	return s.view(m), nil
}

func (s *Service) marketplace(ctx context.Context, id string) (store.Marketplace, error) {
	all, err := s.st.Marketplaces(ctx)
	if err != nil {
		return store.Marketplace{}, err
	}
	for _, m := range all {
		if m.ID == id && !m.Removed {
			return m, nil
		}
	}
	return store.Marketplace{}, fmt.Errorf("%w: marketplace %q", ErrNotFound, id)
}

// AddMarketplaceRequest is what an administrator gives to add a
// marketplace.
type AddMarketplaceRequest struct {
	// Source is owner/repo, an https git URL or an https JSON URL.
	Source string
	Ref    string
	// ID is optional; the default is the name in marketplace.json.
	ID string
}

// AddMarketplace adds a marketplace and refreshes it at once. Nothing is
// stored when the first fetch or parse fails.
func (s *Service) AddMarketplace(ctx context.Context, a Actor, req AddMarketplaceRequest) (MarketplaceView, error) {
	if !a.Admin {
		return MarketplaceView{}, ErrForbidden
	}
	kind, loc, err := ParseSource(req.Source)
	if err != nil {
		return MarketplaceView{}, err
	}
	if kind == store.KindURL && req.Ref != "" {
		return MarketplaceView{}, fmt.Errorf("%w: a marketplace.json URL has no ref", ErrInvalid)
	}
	if strings.HasPrefix(req.Ref, "-") || strings.ContainsAny(req.Ref, " \x00\n:~^?*[\\") {
		return MarketplaceView{}, fmt.Errorf("%w: ref %q", ErrInvalid, req.Ref)
	}
	if req.ID != "" && !store.ValidMarketplaceID(req.ID) {
		return MarketplaceView{}, fmt.Errorf("%w: %q is not a marketplace ID (a-z, 0-9 and -, starting with a letter)", ErrInvalid, req.ID)
	}
	all, err := s.st.Marketplaces(ctx)
	if err != nil {
		return MarketplaceView{}, err
	}
	live := 0
	for _, m := range all {
		if !m.Removed {
			live++
		}
	}
	if live >= MaxMarketplaces {
		return MarketplaceView{}, fmt.Errorf("%w: at most %d marketplaces", ErrLimit, MaxMarketplaces)
	}

	m := store.Marketplace{Kind: kind, Location: loc, Ref: req.Ref, Enabled: true, CreatedBy: a.ID}
	snap, parsed, err := s.fetchSnapshot(ctx, m)
	if err != nil {
		return MarketplaceView{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	m.ID = req.ID
	if m.ID == "" {
		if m.ID = suggestID(parsed.Name); m.ID == "" {
			return MarketplaceView{}, fmt.Errorf("%w: the marketplace name %q gives no ID; give one", ErrInvalid, parsed.Name)
		}
	}
	for _, o := range all {
		if o.ID == m.ID {
			if o.Removed {
				return MarketplaceView{}, fmt.Errorf("%w: the ID %q belongs to a removed default marketplace; give another ID", store.ErrConflict, m.ID)
			}
			return MarketplaceView{}, fmt.Errorf("%w: a marketplace with the ID %q exists", store.ErrConflict, m.ID)
		}
	}
	now := s.now()
	m.Name, m.HeadSHA = parsed.Name, snap.SHA
	m.CreatedAt, m.UpdatedAt, m.RefreshedAt = now, now, now
	snap.MarketplaceID = m.ID
	if err := s.st.SaveMarketplace(ctx, m); err != nil {
		return MarketplaceView{}, err
	}
	if err := s.st.SaveSnapshot(ctx, snap); err != nil {
		return MarketplaceView{}, err
	}
	return s.view(m), nil
}

// UpdateMarketplaceRequest changes a marketplace. Nil fields stay.
type UpdateMarketplaceRequest struct {
	Ref     *string
	Enabled *bool
}

// UpdateMarketplace changes the ref or the enabled state of a marketplace.
// A new ref is read at the next refresh.
func (s *Service) UpdateMarketplace(ctx context.Context, a Actor, id string, req UpdateMarketplaceRequest) (MarketplaceView, error) {
	if !a.Admin {
		return MarketplaceView{}, ErrForbidden
	}
	m, err := s.marketplace(ctx, id)
	if err != nil {
		return MarketplaceView{}, err
	}
	if req.Ref != nil {
		ref := *req.Ref
		if m.Kind == store.KindURL && ref != "" {
			return MarketplaceView{}, fmt.Errorf("%w: a marketplace.json URL has no ref", ErrInvalid)
		}
		if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \x00\n:~^?*[\\") {
			return MarketplaceView{}, fmt.Errorf("%w: ref %q", ErrInvalid, ref)
		}
		if ref != m.Ref {
			// The next tick must read the new ref.
			m.Ref, m.RefreshedAt = ref, time.Time{}
		}
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
	m.UpdatedAt = s.now()
	if err := s.st.SaveMarketplace(ctx, m); err != nil {
		return MarketplaceView{}, err
	}
	return s.view(m), nil
}

// RemoveMarketplace removes a marketplace and its snapshot. A default
// marketplace becomes a tombstone, so that it is not seeded again.
// Installs from it stay.
func (s *Service) RemoveMarketplace(ctx context.Context, a Actor, id string) error {
	if !a.Admin {
		return ErrForbidden
	}
	if err := s.st.DeleteMarketplace(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("%w: marketplace %q", ErrNotFound, id)
		}
		return err
	}
	return nil
}
