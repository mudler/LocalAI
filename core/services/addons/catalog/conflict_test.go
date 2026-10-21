package catalog

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// index serves one library that lists the given add-on ids.
func index(t *testing.T, ids ...string) string {
	t.Helper()
	var rs []Release
	for _, id := range ids {
		r := release()
		r.AddonID, r.Name = id, id
		rs = append(rs, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = WriteIndex(w, rs) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

type fixture struct {
	t *testing.T
	s *Sources
	c Catalog
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "addons", "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, s: s, c: Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}}
}

func (f *fixture) shipped(id string, ids ...string) {
	f.t.Helper()
	if _, err := f.s.SeedDefaults(Source{ID: id, Name: id, URL: index(f.t, ids...), Enabled: true}); err != nil {
		f.t.Fatal(err)
	}
	f.refresh(id)
}

func (f *fixture) user(id string, ids ...string) {
	f.t.Helper()
	if err := f.s.AddUser(Source{ID: id, Name: id, URL: index(f.t, ids...), Enabled: true}); err != nil {
		f.t.Fatal(err)
	}
	f.refresh(id)
}

func (f *fixture) refresh(id string) {
	f.t.Helper()
	if _, err := f.s.Refresh(ctx, id); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) listed() []string {
	f.t.Helper()
	page, err := f.c.Search(ctx, Query{})
	if err != nil {
		f.t.Fatalf("search: %v", err)
	}
	var out []string
	for _, r := range page.Releases {
		out = append(out, r.SourceID+"/"+r.AddonID)
	}
	return out
}

func TestUserSourceWinsOverShippedDefault(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables", "notes")
	f.user("mine", "tables")
	got := strings.Join(f.listed(), ",")
	if got != "mine/tables,official/notes" {
		t.Fatalf("listed %s", got)
	}
	// The default cannot be installed from for the entry the user provides.
	if _, err := f.c.Resolve(ctx, "official", "tables", "1.0.0"); !errors.Is(err, ErrShadowed) {
		t.Errorf("resolve from the default: %v", err)
	}
	if r, err := f.c.Resolve(ctx, "mine", "tables", "1.0.0"); err != nil || r.SourceID != "mine" {
		t.Errorf("resolve from the user source: %+v, %v", r, err)
	}
	// An entry only the default lists still resolves from it.
	if _, err := f.c.Resolve(ctx, "official", "notes", "1.0.0"); err != nil {
		t.Errorf("resolve an unshadowed default entry: %v", err)
	}
}

func TestDefaultIsIgnoredOnceUserSourceWins(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables")
	if got := strings.Join(f.listed(), ","); got != "official/tables" {
		t.Fatalf("before: %s", got)
	}
	f.user("mine", "tables")
	if got := strings.Join(f.listed(), ","); got != "mine/tables" {
		t.Fatalf("after: %s", got)
	}
	// Disabling the user source brings the default back for the entry.
	if err := f.s.SetEnabled("mine", false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.listed(), ","); got != "official/tables" {
		t.Fatalf("after disabling the user source: %s", got)
	}
}

func TestTwoUserSourcesListingTheSameEntryAreRefused(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables")
	f.user("alpha", "tables")
	f.user("beta", "tables", "notes")
	_, err := f.c.Search(ctx, Query{})
	var ce *ConflictError
	if !errors.As(err, &ce) || !errors.Is(err, ErrConflict) {
		t.Fatalf("search: %v", err)
	}
	for _, want := range []string{"alpha", "beta", "tables"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %q", err, want)
		}
	}
	if _, err := f.c.Resolve(ctx, "alpha", "tables", "1.0.0"); !errors.Is(err, ErrConflict) {
		t.Errorf("resolve: %v", err)
	}
	// An entry only one of them lists is not in conflict.
	if _, err := f.c.Resolve(ctx, "beta", "notes", "1.0.0"); err != nil {
		t.Errorf("resolve an entry without a tie: %v", err)
	}
	// Disabling one of the two ends the conflict.
	if err := f.s.SetEnabled("beta", false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.listed(), ","); got != "alpha/tables" {
		t.Fatalf("after disabling: %s", got)
	}
}

// Among shipped defaults nothing changes: each lists its own entries.
func TestShippedDefaultsKeepTheirOwnOrder(t *testing.T) {
	f := newFixture(t)
	f.shipped("official", "tables")
	if _, err := f.s.SeedDefaults(Source{ID: "second", Name: "second", URL: index(t, "tables"), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	f.refresh("second")
	if got := strings.Join(f.listed(), ","); got != "official/tables,second/tables" {
		t.Fatalf("listed %s", got)
	}
}
