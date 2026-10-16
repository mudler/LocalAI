# Add-on navigation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the long sidebar with four top-level items (Chat, Models, Add-ons, Settings) and a per-user Pinned section, and move Studio, Talk, fine-tuning, quantization, the Voice add-on and the Face add-on behind the Add-ons drawer without breaking any old URL or weakening any permission.

**Architecture:** A server-side built-in catalog lists the page add-ons (Studio, Talk, later fine-tuning and quantization) and applies their own gates. Pins live in a new per-user table (a JSON file when authentication is off) behind two user-scoped routes that filter by what the caller may see. The React shell merges the built-in catalog and the Apps list into tiles, draws the drawer and the Pinned section from them, and keeps every old route as a redirect. Voice and Face ship as ordinary add-on packages (idle executable, packaged UI module) that use SDK version 2 primitives and call core routes with the signed-in user's session through `sdk.core.fetch`.

**Tech Stack:** Go 1.2x (Echo, GORM, Ginkgo and `testing`), React 19 with React Router, node `node:test` unit tests, Playwright e2e specs, rolldown for add-on UI bundles.

**Spec:** `docs/superpowers/specs/2026-10-16-addons-navigation-design.md`. Read it before any task. This plan argues from it.

## Global Constraints

Copied verbatim from the spec. Every task includes them.

- Keep the top level small and stable: four items.
- Reuse the add-on contract fields and the Apps permission rules. Add no new permission model.
- Keep every old URL working through a redirect.
- Moving API routes. `/v1/*`, `/api/*` and their handlers stay where they are.
- This spec adds no manifest field.
- No permission check is weaker than before.
- API routes are unchanged. Their tests pass without edits.
- Existing UI tests and the coverage ratchet pass. Coverage is not lowered (`AGENTS.md`).
- The layout works at phone width.
- The docs under `docs/content/` are updated in the same change.

Repository rules that also bind every commit (from `AGENTS.md`, not from the spec): add an `Assisted-by: AGENT_NAME:MODEL_VERSION [TOOL1] [TOOL2]` trailer; never add `Co-Authored-By` for an AI; never add `Signed-off-by` for an AI. The UI is the React app in `core/http/react-ui/`. Use `github.com/mudler/xlog` for logging. Update `docs/content/` in the same change as a user-facing change.

## Review Focus

The five inputs most likely to bite a user that the spec implies but does not spell out. Each has a pinning test in the task named.

1. **A hidden or removed pinned add-on must survive a save.** A user pins four add-ons, one is hidden from them (removed, or access changed), they reorder the other three and save. Expected: the hidden pin is still stored and returns later. Pinned by Task 3 (`Put keeps pins the caller cannot see`).
2. **A user without the `addons` feature who uses Studio today.** Expected: the Add-ons item and the Studio tile are there, no packaged tile, no error banner, and `GET /api/addons/apps` still answers 403. Pinned by Task 1 (`Studio and Talk are visible without any feature`), Task 5 (`addons entry shows for a built-in tile`) and Task 6 (e2e `member without the add-ons feature`).
3. **An old bookmark with a model, a query and a hash.** Expected: `/app/image/stable%2Fdiffusion?x=1#top` lands on `/app/studio/images/stable%2Fdiffusion?x=1#top`, and a user without the `3d` feature still goes to `/app`. Pinned by Task 7 (`studioRedirect keeps the model, the query and the hash`).
4. **A stopped, starting or failed tile.** Expected: a click opens the detail view and never the main page, and a pinned one is muted. Pinned by Task 5 (`tileFromApp never targets the main page of a stopped add-on`).
5. **Authentication off.** Expected: the local owner's pins persist across a restart, Studio and Talk are seeded once, and an unpin sticks. Pinned by Task 2 (`file store keeps pins across a reopen`) and Task 3 (`the seed runs once`).

Two further failure modes have their own tests in the SDK task group: an add-on built for SDK 1 only is refused by the version 2 host with a message that names both ranges (Task 9), and `sdk.core.fetch` refuses absolute URLs, `/api/addons/` and `/api/auth/` (Task 11).

---

## File Structure

New Go packages and files:

- `core/services/addons/builtin/builtin.go`, `builtin_test.go`: the built-in (page add-on) catalog and its gates.
- `core/http/endpoints/addons/builtin.go`, `builtin_test.go`: `GET /api/addons/builtin`.
- `core/services/addons/pins/pins.go`, `store_gorm.go`, `store_file.go`, `service.go`, `sweep.go` and their tests: the pin model, two stores, the service and the retention sweep.
- `core/http/endpoints/addons/pins.go`, `pins_test.go`: `GET` and `PUT /api/addons/pins`.
- `addons/internal/protocol/protocol.go`: the add-on protocol server, moved out of `addons/tables/internal/server`.
- `addons/internal/idle/idle.go`, `idle_test.go`: a runtime with no tools, for UI add-ons.
- `addons/internal/uibuild/uibuild.mjs`: the shared UI bundle builder.
- `addons/internal/identity-ui/src/`: views shared by the Voice and Face add-ons.
- `addons/voice/` and `addons/face/`: the two add-on packages.

New React files:

- `core/http/react-ui/src/utils/addonTiles.js`: pure tile, pin and search rules.
- `core/http/react-ui/src/utils/navigationModel.js`: pure sidebar rules.
- `core/http/react-ui/src/utils/legacyRoutes.js`: the old-route table and the redirect targets.
- `core/http/react-ui/src/utils/pinsApi.js`, `pinsClient.js`: the browser client of the new routes.
- `core/http/react-ui/src/context/NavigationContext.jsx`: loads the built-in catalog and the pins.
- `core/http/react-ui/src/components/PinnedNav.jsx`: the Pinned sidebar section.
- `core/http/react-ui/tests/unit/navigation.test.mjs`, `legacyroutes.test.mjs`, `sdkv2.test.mjs`: unit tests.
- `core/http/react-ui/e2e/addon-navigation.spec.js`: the acceptance spec.

---

## Task group A: navigation core

### Task 1: Built-in catalog and `GET /api/addons/builtin`

**Files:**
- Create: `core/services/addons/builtin/builtin.go`
- Create: `core/services/addons/builtin/builtin_test.go`
- Create: `core/http/endpoints/addons/builtin.go`
- Create: `core/http/endpoints/addons/builtin_test.go`
- Modify: `core/http/routes/addons.go:13-15` (register the route in `RegisterAddonRoutes`, outside the feature group)
- Modify: `pkg/addons/contract/manifest.go:243` (reserve the new path segments)
- Modify: `pkg/addons/contract/manifest_test.go` (reserved test)
- Modify: `docs/content/features/addons.md` (document the route)

**Interfaces:**
- Consumes: `auth.GetUser(c echo.Context) *auth.User` (`core/http/auth/middleware.go:216`), `auth.HasFeatureAccess(db *gorm.DB, user *auth.User, feature string) bool` (`core/http/auth/permissions.go:148`), `auth.RoleAdmin`, `failure(c echo.Context, status int, msg string) error` (package `addons`, `core/http/endpoints/addons`).
- Produces:
  - `builtin.Kind` (`string`), `builtin.KindPage Kind = "page"`.
  - `builtin.Entry{ID string; Kind Kind; TitleKey string; Icon string; MainPath string; Feature string; AdminOnly bool}` with JSON names `id, kind, titleKey, icon, mainPath, feature, adminOnly`.
  - `builtin.Viewer{Admin bool; Has func(feature string) bool}`.
  - `builtin.Catalog() []Entry`, `builtin.IDs() []string`, `builtin.Visible(v Viewer) []Entry`.
  - `endpoints.ListBuiltin(db *gorm.DB) echo.HandlerFunc`. Response body `{"builtin":[Entry...]}`. With `db == nil` (authentication off) the viewer is an administrator.
  - Route `GET /api/addons/builtin`, authenticated, no `addons` feature required.

Decision recorded here (spec, "Decision: the gate rule for Studio and Talk"): Studio and Talk have no gate, so every authenticated viewer sees them.

- [ ] **Step 1: Write the failing test for the catalog**

`core/services/addons/builtin/builtin_test.go`:

```go
// SPDX-License-Identifier: MIT
package builtin

import (
	"slices"
	"testing"
)

func ids(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestStudioAndTalkAreVisibleWithoutAnyFeature(t *testing.T) {
	got := ids(Visible(Viewer{Admin: false, Has: func(string) bool { return false }}))
	if !slices.Equal(got, []string{"studio", "talk"}) {
		t.Fatalf("a viewer with no feature must still see studio and talk, got %v", got)
	}
}

func TestGatesHideAnEntryFromAViewerWithoutTheFeature(t *testing.T) {
	gated := Entry{ID: "x", Feature: "fine_tuning"}
	adminOnly := Entry{ID: "y", AdminOnly: true}
	none := Viewer{Has: func(string) bool { return false }}
	if none.allows(gated) || none.allows(adminOnly) {
		t.Fatal("a viewer without the feature or the role must not see a gated entry")
	}
	has := Viewer{Has: func(f string) bool { return f == "fine_tuning" }}
	if !has.allows(gated) || has.allows(adminOnly) {
		t.Fatal("the feature opens a feature gate and nothing else")
	}
	admin := Viewer{Admin: true}
	if !admin.allows(gated) || !admin.allows(adminOnly) {
		t.Fatal("an administrator passes every gate")
	}
}

func TestIDsAreUniqueAndWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalog() {
		if seen[e.ID] || e.ID == "" || e.TitleKey == "" || e.MainPath == "" || e.Kind != KindPage {
			t.Fatalf("invalid or duplicate entry %+v", e)
		}
		seen[e.ID] = true
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./core/services/addons/builtin/ -run 'Studio|Gates|IDs' -v`
Expected: FAIL with `no non-test Go files` or `undefined: Visible`.

- [ ] **Step 3: Write the catalog**

`core/services/addons/builtin/builtin.go`:

```go
// SPDX-License-Identifier: MIT

// Package builtin lists the page add-ons that ship inside the LocalAI web
// bundle: Studio and Talk now, fine-tuning and quantization later. They have
// no process and no lifecycle of their own. The catalog is the one place that
// says who may see each of them, so the pin routes and the drawer agree.
package builtin

import "slices"

// Kind says how an entry opens. Only pages exist: a core React page.
type Kind string

const KindPage Kind = "page"

// Entry is one built-in add-on as the browser draws it. TitleKey is a key of
// the `nav` i18n namespace and Icon is a host icon name.
type Entry struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	TitleKey  string `json:"titleKey"`
	Icon      string `json:"icon"`
	MainPath  string `json:"mainPath"`
	Feature   string `json:"feature,omitempty"`
	AdminOnly bool   `json:"adminOnly,omitempty"`
}

// Viewer is what the gates need to know about the caller.
type Viewer struct {
	Admin bool
	Has   func(feature string) bool
}

func (v Viewer) allows(e Entry) bool {
	if v.Admin {
		return true
	}
	if e.AdminOnly {
		return false
	}
	if e.Feature != "" && (v.Has == nil || !v.Has(e.Feature)) {
		return false
	}
	return true
}

var catalog = []Entry{
	{ID: "studio", Kind: KindPage, TitleKey: "items.studio", Icon: "palette", MainPath: "/app/studio"},
	{ID: "talk", Kind: KindPage, TitleKey: "items.talk", Icon: "phone", MainPath: "/app/talk"},
}

// Catalog returns a copy of every entry, in display order.
func Catalog() []Entry { return slices.Clone(catalog) }

// IDs returns the identifier of every entry.
func IDs() []string {
	out := make([]string, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, e.ID)
	}
	return out
}

// Visible returns the entries the viewer passes the gates of.
func Visible(v Viewer) []Entry {
	out := []Entry{}
	for _, e := range catalog {
		if v.allows(e) {
			out = append(out, e)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the catalog tests**

Run: `go test ./core/services/addons/builtin/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Write the failing endpoint test**

`core/http/endpoints/addons/builtin_test.go`:

```go
// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestListBuiltinWithoutAnAccountDatabaseListsEveryEntry(t *testing.T) {
	e := echo.New()
	e.GET("/api/addons/builtin", ListBuiltin(nil))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/addons/builtin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Builtin []struct {
			ID string `json:"id"`
		} `json:"builtin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Builtin) != 2 || body.Builtin[0].ID != "studio" || body.Builtin[1].ID != "talk" {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("the list depends on the caller and must not be cached, got %q", rec.Header().Get("Cache-Control"))
	}
}
```

- [ ] **Step 6: Run it to see it fail**

Run: `go test ./core/http/endpoints/addons/ -run ListBuiltin -v`
Expected: FAIL with `undefined: ListBuiltin`.

- [ ] **Step 7: Write the handler**

`core/http/endpoints/addons/builtin.go`:

```go
// SPDX-License-Identifier: MIT
package addons

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
	"gorm.io/gorm"
)

// builtinViewer resolves who is asking. Without an account database there is
// one local owner and every gate is open. With one, the authenticated user is
// required.
func builtinViewer(c echo.Context, db *gorm.DB) (builtin.Viewer, bool) {
	if db == nil {
		return builtin.Viewer{Admin: true, Has: func(string) bool { return true }}, true
	}
	u := auth.GetUser(c)
	if u == nil {
		return builtin.Viewer{}, false
	}
	return builtin.Viewer{
		Admin: u.Role == auth.RoleAdmin,
		Has:   func(feature string) bool { return auth.HasFeatureAccess(db, u, feature) },
	}, true
}

// ListBuiltin lists the built-in page add-ons the caller may open.
// @Summary List the built-in add-ons the caller may open
// @Description Returns the page add-ons that ship with LocalAI (Studio and Talk, later fine-tuning and quantization). Each entry carries only its own gate: the route needs an authenticated caller and no `addons` feature, so a user who reaches Studio today still reaches it from the Add-ons drawer.
// @Tags Addons
// @Produce json
// @Success 200 {object} map[string][]builtin.Entry "Entries the caller may open"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Router /api/addons/builtin [get]
func ListBuiltin(db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		privateHeaders(c)
		v, ok := builtinViewer(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		return c.JSON(200, map[string][]builtin.Entry{"builtin": builtin.Visible(v)})
	}
}
```

`privateHeaders(c)` is the helper `ListApps` already calls (`core/http/endpoints/addons/apps.go:34`). Confirm it sets `Cache-Control: private, no-store` with `grep -n "func privateHeaders" -A6 core/http/endpoints/addons/*.go`; the test above checks that header, so a different value fails the test and you fix the handler, not the test.

- [ ] **Step 8: Reserve the path segments and register the route**

In `pkg/addons/contract/manifest.go:243` append `"builtin"` and `"pins"` to `reservedAddonIDs` (the new routes `/api/addons/builtin` and `/api/addons/pins` share the first segment position with `/api/addons/<id>/assets/...`).

Append to `pkg/addons/contract/manifest_test.go` inside the `Manifest` Describe:

```go
	It("reserves the segments of the navigation routes", func() {
		Expect(ReservedAddonID("builtin")).To(BeTrue())
		Expect(ReservedAddonID("pins")).To(BeTrue())
		Expect(ValidAddonID("pins")).To(BeFalse())
	})
```

In `core/http/routes/addons.go`, change `RegisterAddonRoutes` to:

```go
func RegisterAddonRoutes(e *echo.Echo, a *application.Application) {
	RegisterAddonRoutesFor(e, a.AddonGateway(), a.AuthDB(), a.AddonOwnershipMode())
	// Outside the feature group on purpose: the built-in tiles must stay
	// reachable for a user who does not hold the addons feature.
	e.GET("/api/addons/builtin", endpoints.ListBuiltin(a.AuthDB()))
}
```

- [ ] **Step 9: Run the tests**

Run: `go test ./core/http/endpoints/addons/ ./pkg/addons/contract/ ./core/http/routes/ -count=1`
Expected: PASS. If `core/http/routes/addons_discovery_test.go` fails because it derives the registered segments from `RegisterAddonRoutesFor` only, it still passes: the new segments are reserved by hand and checked by the contract test above.

- [ ] **Step 10: Document the route and commit**

Add a short "Built-in add-ons" subsection to the Apps section of `docs/content/features/addons.md` stating: the route, that it needs no `addons` feature, that Studio and Talk have no gate, and that `builtin` and `pins` are reserved add-on IDs.

```bash
git add core/services/addons/builtin core/http/endpoints/addons/builtin.go core/http/endpoints/addons/builtin_test.go core/http/routes/addons.go pkg/addons/contract/manifest.go pkg/addons/contract/manifest_test.go docs/content/features/addons.md
git commit -m "feat(addons): list the built-in page add-ons

Studio and Talk get a server-side catalog with their own gates, so the
Add-ons drawer can show them to users who lack the addons feature.
The path segments builtin and pins become reserved add-on IDs.

Assisted-by: Claude:claude-sonnet-5-5 go"
```

---

### Task 2: Pin model and stores

**Files:**
- Create: `core/services/addons/pins/pins.go`
- Create: `core/services/addons/pins/store_gorm.go`
- Create: `core/services/addons/pins/store_file.go`
- Create: `core/services/addons/pins/store_contract_test.go` (untagged shared contract)
- Create: `core/services/addons/pins/store_file_test.go`
- Create: `core/services/addons/pins/store_gorm_test.go` (`//go:build auth`)

**Interfaces:**
- Consumes: `gorm.io/gorm`, `gorm.io/gorm/clause`, `auth.InitDB(databaseURL string) (*gorm.DB, error)` (tests only, `core/http/auth/db.go:16`).
- Produces:
  - `pins.HardLimit = 32` (the server rejects a longer list), `pins.SeedIDs = []string{"studio", "talk"}`, `pins.Retention = 30 * 24 * time.Hour`.
  - `pins.Pin{UserID, AddonID string; Position int; MissingSince *time.Time}` (table `addon_pins`), `pins.Seeded{UserID string; At time.Time}` (table `addon_pin_seeds`).
  - `pins.Store` interface:
    - `List(ctx context.Context, userID string) ([]Pin, error)` ordered by position.
    - `Replace(ctx context.Context, userID string, ids []string) error` writes the list in the given order, keeps `MissingSince` of ids that stay.
    - `Seed(ctx context.Context, userID string, ids []string) (bool, error)` runs once per user: returns true only the first time, appending ids that are not yet pinned.
    - `Distinct(ctx context.Context) ([]string, error)`.
    - `MarkMissing(ctx context.Context, ids []string, now time.Time) error` sets `MissingSince` where it is empty.
    - `ClearMissing(ctx context.Context, ids []string) error`.
    - `DeleteMissingBefore(ctx context.Context, cutoff time.Time) (int64, error)`.
  - `pins.NewGormStore(db *gorm.DB) (*GormStore, error)`, `pins.NewFileStore(path string) (*FileStore, error)` (empty path means memory only).

Distributed-state mode (documented in Task 3's docs step): with an account database the table is shared by every frontend. Without one there is one local owner and one process; the file store is per instance.

- [ ] **Step 1: Write the shared contract test**

`core/services/addons/pins/store_contract_test.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"testing"
	"time"
)

func idsOf(ps []Pin) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.AddonID)
	}
	return out
}

// runStoreContract is run once per store implementation.
func runStoreContract(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	t.Run("replace and list round-trip in order", func(t *testing.T) {
		s := open(t)
		if err := s.Replace(ctx, "u1", []string{"b", "a", "c"}); err != nil {
			t.Fatal(err)
		}
		got, _ := s.List(ctx, "u1")
		if !slices.Equal(idsOf(got), []string{"b", "a", "c"}) {
			t.Fatalf("order lost: %v", idsOf(got))
		}
		if err := s.Replace(ctx, "u1", []string{"c", "b"}); err != nil {
			t.Fatal(err)
		}
		got, _ = s.List(ctx, "u1")
		if !slices.Equal(idsOf(got), []string{"c", "b"}) {
			t.Fatalf("replace must drop what is not listed: %v", idsOf(got))
		}
	})
	t.Run("a second user does not see the first user's pins", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"a"})
		got, _ := s.List(ctx, "u2")
		if len(got) != 0 {
			t.Fatalf("leak: %v", idsOf(got))
		}
	})
	t.Run("the seed runs once per user and an unpin sticks", func(t *testing.T) {
		s := open(t)
		first, err := s.Seed(ctx, "u1", SeedIDs)
		if err != nil || !first {
			t.Fatalf("first seed: %v %v", first, err)
		}
		_ = s.Replace(ctx, "u1", []string{"talk"}) // the user unpins studio
		again, err := s.Seed(ctx, "u1", SeedIDs)
		if err != nil || again {
			t.Fatalf("the seed ran twice: %v %v", again, err)
		}
		got, _ := s.List(ctx, "u1")
		if !slices.Equal(idsOf(got), []string{"talk"}) {
			t.Fatalf("an unpin must stick: %v", idsOf(got))
		}
	})
	t.Run("missing pins are marked, cleared and deleted after the window", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"gone", "here"})
		now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		if err := s.MarkMissing(ctx, []string{"gone"}, now); err != nil {
			t.Fatal(err)
		}
		// A second mark must not move the start of the window.
		if err := s.MarkMissing(ctx, []string{"gone"}, now.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		got, _ := s.List(ctx, "u1")
		for _, p := range got {
			if p.AddonID == "gone" && (p.MissingSince == nil || !p.MissingSince.Equal(now)) {
				t.Fatalf("the window start moved: %v", p.MissingSince)
			}
		}
		n, _ := s.DeleteMissingBefore(ctx, now.Add(-time.Second))
		if n != 0 {
			t.Fatalf("deleted inside the window: %d", n)
		}
		if err := s.ClearMissing(ctx, []string{"gone"}); err != nil {
			t.Fatal(err)
		}
		_ = s.MarkMissing(ctx, []string{"gone"}, now)
		n, _ = s.DeleteMissingBefore(ctx, now.Add(time.Second))
		if n != 1 {
			t.Fatalf("expected one deleted pin, got %d", n)
		}
		got, _ = s.List(ctx, "u1")
		if !slices.Equal(idsOf(got), []string{"here"}) {
			t.Fatalf("unexpected list %v", idsOf(got))
		}
	})
	t.Run("replace keeps the missing mark of a pin that stays", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"gone"})
		now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		_ = s.MarkMissing(ctx, []string{"gone"}, now)
		_ = s.Replace(ctx, "u1", []string{"gone", "new"})
		got, _ := s.List(ctx, "u1")
		if got[0].MissingSince == nil {
			t.Fatal("a save must not reset the retention window")
		}
	})
	t.Run("distinct lists every pinned id once", func(t *testing.T) {
		s := open(t)
		_ = s.Replace(ctx, "u1", []string{"a", "b"})
		_ = s.Replace(ctx, "u2", []string{"b", "c"})
		d, _ := s.Distinct(ctx)
		slices.Sort(d)
		if !slices.Equal(d, []string{"a", "b", "c"}) {
			t.Fatalf("distinct: %v", d)
		}
	})
}
```

`core/services/addons/pins/store_file_test.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

func TestFileStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		s, err := NewFileStore(filepath.Join(t.TempDir(), "pins.json"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestMemoryOnlyFileStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		s, err := NewFileStore("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestFileStoreKeepsPinsAcrossAReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pins.json")
	a, _ := NewFileStore(path)
	if first, _ := a.Seed(ctx, "local-owner", SeedIDs); !first {
		t.Fatal("first seed must run")
	}
	_ = a.Replace(ctx, "local-owner", []string{"talk"})
	b, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := b.List(ctx, "local-owner")
	if !slices.Equal(idsOf(got), []string{"talk"}) {
		t.Fatalf("pins lost across a restart: %v", idsOf(got))
	}
	if again, _ := b.Seed(ctx, "local-owner", SeedIDs); again {
		t.Fatal("the seed marker must survive a restart")
	}
}
```

`core/services/addons/pins/store_gorm_test.go`:

```go
//go:build auth

// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
)

func TestTwoServicesOverOneDatabaseAgree(t *testing.T) {
	db, err := auth.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := NewGormStore(db)
	b, _ := NewGormStore(db)
	ctx := context.Background()
	sa, sb := &Service{Store: a}, &Service{Store: b}
	vis := map[string]bool{"studio": true, "talk": true, "x": true}
	if _, err := sa.Put(ctx, "u1", []string{"x", "talk"}, vis); err != nil {
		t.Fatal(err)
	}
	got, _ := sb.List(ctx, "u1", vis)
	if !slices.Equal(got, []string{"x", "talk"}) {
		t.Fatalf("the second frontend sees %v", got)
	}
}

func TestGormStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		db, err := auth.InitDB(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewGormStore(db)
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./core/services/addons/pins/ -count=1`
Expected: FAIL with `undefined: Store` and `undefined: NewFileStore`.

- [ ] **Step 3: Write the types**

`core/services/addons/pins/pins.go`:

```go
// SPDX-License-Identifier: MIT

// Package pins stores which add-ons each user pinned to the sidebar, and in
// which order. A pin names an add-on ID, never a generation. The service
// never deletes a pin because an add-on stopped, was updated or lost access;
// only a removed add-on's pin expires, after Retention.
package pins

import (
	"context"
	"time"
)

// HardLimit is the longest list the server accepts. The sidebar caps the list
// lower (eight); this bound only stops a runaway client.
const HardLimit = 32

// Retention is how long the pin of a removed add-on is kept. It must not be
// shorter than the time an administrator needs to reinstall after a mistake.
const Retention = 30 * 24 * time.Hour

// SeedIDs are pinned once for every user so the layout does not change under
// users who had Studio and Talk in the sidebar.
var SeedIDs = []string{"studio", "talk"}

// Pin is one row: one user, one add-on, one position.
type Pin struct {
	UserID       string     `gorm:"primaryKey;size:64" json:"-"`
	AddonID      string     `gorm:"primaryKey;size:64" json:"id"`
	Position     int        `gorm:"index" json:"position"`
	MissingSince *time.Time `json:"-"`
}

func (Pin) TableName() string { return "addon_pins" }

// Seeded records that the one-time seed ran for a user.
type Seeded struct {
	UserID string `gorm:"primaryKey;size:64"`
	At     time.Time
}

func (Seeded) TableName() string { return "addon_pin_seeds" }

// Store keeps the pins. Every method is safe for concurrent use.
type Store interface {
	List(ctx context.Context, userID string) ([]Pin, error)
	Replace(ctx context.Context, userID string, ids []string) error
	Seed(ctx context.Context, userID string, ids []string) (bool, error)
	Distinct(ctx context.Context) ([]string, error)
	MarkMissing(ctx context.Context, ids []string, now time.Time) error
	ClearMissing(ctx context.Context, ids []string) error
	DeleteMissingBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
```

- [ ] **Step 4: Write the file store**

`core/services/addons/pins/store_file.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// FileStore keeps the pins of the one local owner in a JSON file, for a
// deployment with authentication off (no account database). The behaviour is
// per instance: the file lives on one host.
type FileStore struct {
	path string
	mu   sync.Mutex
	st   fileState
}

type fileState struct {
	Pins   map[string][]Pin     `json:"pins"`
	Seeded map[string]time.Time `json:"seeded"`
}

func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, st: fileState{Pins: map[string][]Pin{}, Seeded: map[string]time.Time{}}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.st); err != nil {
		return nil, err
	}
	if s.st.Pins == nil {
		s.st.Pins = map[string][]Pin{}
	}
	if s.st.Seeded == nil {
		s.st.Seeded = map[string]time.Time{}
	}
	return s, nil
}

// save writes through a temporary file so a crash never leaves half a file.
func (s *FileStore) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(s.st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *FileStore) List(_ context.Context, userID string) ([]Pin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.st.Pins[userID]), nil
}

func (s *FileStore) Replace(_ context.Context, userID string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Pins[userID] = rebuild(s.st.Pins[userID], userID, ids)
	return s.save()
}

// rebuild lays ids out in order and carries the missing mark of an id that
// was already pinned.
func rebuild(old []Pin, userID string, ids []string) []Pin {
	marks := map[string]*time.Time{}
	for _, p := range old {
		marks[p.AddonID] = p.MissingSince
	}
	out := make([]Pin, 0, len(ids))
	for i, id := range ids {
		out = append(out, Pin{UserID: userID, AddonID: id, Position: i, MissingSince: marks[id]})
	}
	return out
}

func (s *FileStore) Seed(_ context.Context, userID string, ids []string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, done := s.st.Seeded[userID]; done {
		return false, nil
	}
	have := map[string]bool{}
	list := s.st.Pins[userID]
	for _, p := range list {
		have[p.AddonID] = true
	}
	for _, id := range ids {
		if !have[id] {
			list = append(list, Pin{UserID: userID, AddonID: id, Position: len(list)})
		}
	}
	s.st.Pins[userID] = list
	s.st.Seeded[userID] = time.Now().UTC()
	return true, s.save()
}

func (s *FileStore) Distinct(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	for _, list := range s.st.Pins {
		for _, p := range list {
			if !seen[p.AddonID] {
				seen[p.AddonID] = true
				out = append(out, p.AddonID)
			}
		}
	}
	return out, nil
}

func (s *FileStore) MarkMissing(_ context.Context, ids []string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u, list := range s.st.Pins {
		for i := range list {
			if list[i].MissingSince == nil && slices.Contains(ids, list[i].AddonID) {
				t := now
				list[i].MissingSince = &t
			}
		}
		s.st.Pins[u] = list
	}
	return s.save()
}

func (s *FileStore) ClearMissing(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u, list := range s.st.Pins {
		for i := range list {
			if slices.Contains(ids, list[i].AddonID) {
				list[i].MissingSince = nil
			}
		}
		s.st.Pins[u] = list
	}
	return s.save()
}

func (s *FileStore) DeleteMissingBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for u, list := range s.st.Pins {
		kept := list[:0]
		for _, p := range list {
			if p.MissingSince != nil && p.MissingSince.Before(cutoff) {
				n++
				continue
			}
			kept = append(kept, p)
		}
		for i := range kept {
			kept[i].Position = i
		}
		s.st.Pins[u] = kept
	}
	return n, s.save()
}
```

- [ ] **Step 5: Write the GORM store**

`core/services/addons/pins/store_gorm.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GormStore keeps the pins in the account database, which every frontend of a
// deployment shares. No process holds the only copy.
type GormStore struct{ db *gorm.DB }

func NewGormStore(db *gorm.DB) (*GormStore, error) {
	if err := db.AutoMigrate(&Pin{}, &Seeded{}); err != nil {
		return nil, err
	}
	return &GormStore{db: db}, nil
}

func (s *GormStore) List(ctx context.Context, userID string) ([]Pin, error) {
	var out []Pin
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("position asc, addon_id asc").Find(&out).Error
	return out, err
}

func (s *GormStore) Replace(ctx context.Context, userID string, ids []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old []Pin
		if err := tx.Where("user_id = ?", userID).Find(&old).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&Pin{}).Error; err != nil {
			return err
		}
		rows := rebuild(old, userID, ids)
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

func (s *GormStore) Seed(ctx context.Context, userID string, ids []string) (bool, error) {
	seeded := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Seeded{UserID: userID, At: time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		seeded = true
		var have []Pin
		if err := tx.Where("user_id = ?", userID).Find(&have).Error; err != nil {
			return err
		}
		held := map[string]bool{}
		for _, p := range have {
			held[p.AddonID] = true
		}
		next := len(have)
		for _, id := range ids {
			if held[id] {
				continue
			}
			if err := tx.Create(&Pin{UserID: userID, AddonID: id, Position: next}).Error; err != nil {
				return err
			}
			next++
		}
		return nil
	})
	return seeded, err
}

func (s *GormStore) Distinct(ctx context.Context) ([]string, error) {
	var out []string
	err := s.db.WithContext(ctx).Model(&Pin{}).Distinct().Pluck("addon_id", &out).Error
	return out, err
}

func (s *GormStore) MarkMissing(ctx context.Context, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&Pin{}).Where("addon_id IN ? AND missing_since IS NULL", ids).Update("missing_since", now).Error
}

func (s *GormStore) ClearMissing(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Model(&Pin{}).Where("addon_id IN ?", ids).Update("missing_since", gorm.Expr("NULL")).Error
}

func (s *GormStore) DeleteMissingBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("missing_since IS NOT NULL AND missing_since < ?", cutoff).Delete(&Pin{})
	return res.RowsAffected, res.Error
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./core/services/addons/pins/ -count=1` then `go test -tags auth ./core/services/addons/pins/ -count=1`
Expected: PASS in both. The second run needs CGO for SQLite, as the other `auth`-tagged tests do.

- [ ] **Step 7: Commit**

```bash
git add core/services/addons/pins
git commit -m "feat(addons): store the sidebar pins per user

A GORM store for deployments with an account database and a JSON file
store for the single local owner when authentication is off. Both keep
the order, seed once per user and track when a pinned add-on went
missing.

Assisted-by: Claude:claude-sonnet-5-5 go"
```

---

### Task 3: Pin service and the `GET`/`PUT /api/addons/pins` routes

**Files:**
- Create: `core/services/addons/pins/service.go`, `service_test.go`
- Create: `core/http/endpoints/addons/pins.go`, `pins_test.go`
- Modify: `core/http/routes/addons.go` (new `RegisterAddonPinRoutes`, called from `RegisterAddonRoutes`)
- Modify: `docs/content/features/addons.md` (route and the distributed-state mode)

**Interfaces:**
- Consumes: `pins.Store` (Task 2), `builtin.Visible`, `builtin.Viewer` (Task 1), `builtinViewer(c, db)` (Task 1), `auth.CSRFMiddleware()` (`core/http/auth/csrf.go:12`), `endpoints.BrowserProtection()` (`core/http/endpoints/addons/gateway.go:248`), `actor(c, db, s)` and `resolve(opts)` (`gateway.go`), `(*gateway.Gateway).Apps(ctx, contract.Actor) (apps.List, error)` (`core/services/addons/gateway/apps.go:28`), `auth.AddonLocalOwnerID`, `auth.FeatureAddons`.
- Produces:
  - `pins.Service{Store Store}` with
    - `List(ctx context.Context, userID string, visible map[string]bool) ([]string, error)`: seeds once, returns the caller's visible pins in order.
    - `Put(ctx context.Context, userID string, ids []string, visible map[string]bool) ([]string, error)`: dedupes, drops ids the caller cannot see, keeps stored pins the caller cannot see, returns the visible list.
  - `pins.ErrTooMany`.
  - `endpoints.GetPins(gw *gateway.Gateway, db *gorm.DB, svc *pins.Service, opts ...Option) echo.HandlerFunc`, `endpoints.PutPins(...)` with the same arguments.
  - Routes: `GET /api/addons/pins` returns a JSON array of add-on IDs; `PUT /api/addons/pins` takes a JSON array and returns the saved visible array. Both need an authenticated caller, no admin role and no `addons` feature.
  - `routes.RegisterAddonPinRoutes(e *echo.Echo, gw *gateway.Gateway, db *gorm.DB, ownership string, svc *pins.Service)`.

- [ ] **Step 1: Write the failing service tests**

`core/services/addons/pins/service_test.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func vis(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func newService(t *testing.T) *Service {
	s, err := NewFileStore("")
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Store: s}
}

func TestListSeedsStudioAndTalkOnce(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	got, err := svc.List(ctx, "u1", vis("studio", "talk"))
	if err != nil || !slices.Equal(got, []string{"studio", "talk"}) {
		t.Fatalf("seed: %v %v", got, err)
	}
	if _, err := svc.Put(ctx, "u1", []string{"talk"}, vis("studio", "talk")); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.List(ctx, "u1", vis("studio", "talk"))
	if !slices.Equal(got, []string{"talk"}) {
		t.Fatalf("the seed ran twice, an unpin did not stick: %v", got)
	}
}

func TestPutKeepsPinsTheCallerCannotSee(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	_ = svc.Store.Replace(ctx, "u1", []string{"a", "hidden", "b", "c"})
	_, _ = svc.Store.Seed(ctx, "u1", nil) // mark the seed as done
	got, err := svc.Put(ctx, "u1", []string{"c", "a", "b"}, vis("a", "b", "c"))
	if err != nil || !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("visible result: %v %v", got, err)
	}
	stored, _ := svc.Store.List(ctx, "u1")
	if !slices.Equal(idsOf(stored), []string{"c", "a", "b", "hidden"}) {
		t.Fatalf("the hidden pin was lost or the order is wrong: %v", idsOf(stored))
	}
}

func TestPutDropsUnknownAndDuplicateIDs(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	_, _ = svc.Store.Seed(ctx, "u1", nil)
	got, err := svc.Put(ctx, "u1", []string{"a", "nope", "a", "b"}, vis("a", "b"))
	if err != nil || !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestPutRejectsAListOverTheHardLimit(t *testing.T) {
	svc := newService(t)
	long := make([]string, HardLimit+1)
	for i := range long {
		long[i] = "a"
	}
	_, err := svc.Put(context.Background(), "u1", long, vis("a"))
	if !errors.Is(err, ErrTooMany) {
		t.Fatalf("want ErrTooMany, got %v", err)
	}
}

func TestListNeverRevealsAHiddenID(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	_ = svc.Store.Replace(ctx, "u1", []string{"secret", "studio"})
	_, _ = svc.Store.Seed(ctx, "u1", nil)
	got, _ := svc.List(ctx, "u1", vis("studio"))
	if !slices.Equal(got, []string{"studio"}) {
		t.Fatalf("a hidden id leaked: %v", got)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./core/services/addons/pins/ -run 'Seeds|Keeps|Drops|Rejects|Reveals' -v`
Expected: FAIL with `undefined: Service`.

- [ ] **Step 3: Write the service**

`core/services/addons/pins/service.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"errors"
)

// ErrTooMany means the list is longer than HardLimit.
var ErrTooMany = errors.New("too many pins")

// Service applies the visibility rules over a Store. `visible` is the set of
// add-on IDs the caller may see right now; the HTTP layer builds it from the
// built-in catalog and the Apps list, so a pin is filtered exactly like a tile.
type Service struct{ Store Store }

// List returns the caller's visible pins in order. The first call for a user
// also runs the one-time seed.
func (s *Service) List(ctx context.Context, userID string, visible map[string]bool) ([]string, error) {
	if _, err := s.Store.Seed(ctx, userID, SeedIDs); err != nil {
		return nil, err
	}
	stored, err := s.Store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, p := range stored {
		if visible[p.AddonID] {
			out = append(out, p.AddonID)
		}
	}
	return out, nil
}

// Put replaces the caller's visible pins with ids. Stored pins the caller
// cannot see are kept after them, so a save never erases a pin that is only
// hidden for now (a removed add-on inside its window, or an access change).
func (s *Service) Put(ctx context.Context, userID string, ids []string, visible map[string]bool) ([]string, error) {
	if len(ids) > HardLimit {
		return nil, ErrTooMany
	}
	if _, err := s.Store.Seed(ctx, userID, SeedIDs); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	shown := []string{}
	for _, id := range ids {
		if visible[id] && !seen[id] {
			seen[id] = true
			shown = append(shown, id)
		}
	}
	stored, err := s.Store.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	final := append([]string{}, shown...)
	for _, p := range stored {
		if !visible[p.AddonID] && !seen[p.AddonID] {
			seen[p.AddonID] = true
			final = append(final, p.AddonID)
		}
	}
	if err := s.Store.Replace(ctx, userID, final); err != nil {
		return nil, err
	}
	return shown, nil
}
```

- [ ] **Step 4: Run the service tests**

Run: `go test ./core/services/addons/pins/ -count=1`
Expected: PASS.

- [ ] **Step 5: Write the failing handler tests**

`core/http/endpoints/addons/pins_test.go` (no account database, so every request is the local owner):

```go
// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/pins"
)

func pinsApp(t *testing.T) *echo.Echo {
	store, err := pins.NewFileStore("")
	if err != nil {
		t.Fatal(err)
	}
	svc := &pins.Service{Store: store}
	e := echo.New()
	e.GET("/api/addons/pins", GetPins(nil, nil, svc))
	e.PUT("/api/addons/pins", PutPins(nil, nil, svc))
	return e
}

func call(e *echo.Echo, method, body string) (int, []string) {
	req := httptest.NewRequest(method, "/api/addons/pins", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var out []string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestPinsRoundTripForTheLocalOwner(t *testing.T) {
	e := pinsApp(t)
	code, got := call(e, http.MethodGet, "")
	if code != 200 || len(got) != 2 || got[0] != "studio" || got[1] != "talk" {
		t.Fatalf("seed: %d %v", code, got)
	}
	code, got = call(e, http.MethodPut, `["talk","studio"]`)
	if code != 200 || got[0] != "talk" {
		t.Fatalf("put: %d %v", code, got)
	}
	_, got = call(e, http.MethodGet, "")
	if len(got) != 2 || got[0] != "talk" {
		t.Fatalf("round trip: %v", got)
	}
}

func TestPutDropsIDsTheCallerCannotSee(t *testing.T) {
	e := pinsApp(t)
	_, got := call(e, http.MethodPut, `["studio","not-installed"]`)
	if len(got) != 1 || got[0] != "studio" {
		t.Fatalf("an unknown id must be dropped without a hint: %v", got)
	}
}

func TestPutRejectsABodyThatIsNotAListOfStrings(t *testing.T) {
	e := pinsApp(t)
	for _, body := range []string{`{"a":1}`, `[1,2]`, `not json`, ``} {
		if code, _ := call(e, http.MethodPut, body); code != 400 {
			t.Fatalf("body %q: want 400, got %d", body, code)
		}
	}
}

func TestPutRejectsAListOverTheHardLimit(t *testing.T) {
	e := pinsApp(t)
	items := make([]string, pins.HardLimit+1)
	for i := range items {
		items[i] = `"studio"`
	}
	if code, _ := call(e, http.MethodPut, "["+strings.Join(items, ",")+"]"); code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
}
```

- [ ] **Step 6: Run to see it fail**

Run: `go test ./core/http/endpoints/addons/ -run 'Pins|PutDrops|PutRejects' -v`
Expected: FAIL with `undefined: GetPins`.

- [ ] **Step 7: Write the handlers**

`core/http/endpoints/addons/pins.go`:

```go
// SPDX-License-Identifier: MIT
package addons

import (
	"errors"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/builtin"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/pins"
	"gorm.io/gorm"
)

// pinUser is the key of the caller's rows. Without an account database the
// caller is the one local owner.
func pinUser(c echo.Context, db *gorm.DB) (string, bool) {
	if u := auth.GetUser(c); u != nil {
		return u.ID, true
	}
	if db == nil {
		return auth.AddonLocalOwnerID, true
	}
	return "", false
}

// visibleIDs is the set of add-on IDs the caller may see: the built-in
// entries that pass their gates, plus the apps the owner lists for the caller
// when the caller holds the addons feature. apps is false when the owner could
// not answer; the caller then must not treat the set as complete.
func visibleIDs(c echo.Context, gw *gateway.Gateway, db *gorm.DB, s settings) (set map[string]bool, complete bool) {
	set = map[string]bool{}
	v, ok := builtinViewer(c, db)
	if !ok {
		return set, false
	}
	for _, e := range builtin.Visible(v) {
		set[e.ID] = true
	}
	if !v.Admin && (v.Has == nil || !v.Has(auth.FeatureAddons)) {
		return set, true
	}
	if gw == nil {
		return set, true
	}
	a, err := actor(c, db, s)
	if err != nil {
		return set, false
	}
	list, err := gw.Apps(c.Request().Context(), a)
	if err != nil {
		return set, false
	}
	for _, app := range list.Apps {
		set[app.ID] = true
	}
	return set, true
}

// GetPins returns the caller's pinned add-on IDs.
// @Summary List the add-ons the caller pinned
// @Description Returns the ordered add-on IDs the caller pinned to the sidebar. The first call of a user also pins Studio and Talk, once. The list holds only IDs the caller may see right now: a hidden or removed add-on is left out and the route does not reveal whether it exists. Needs an authenticated caller and no administrator role.
// @Tags Addons
// @Produce json
// @Success 200 {array} string "Ordered add-on IDs"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Failure 503 {object} AddonErrorResponse "The pin store is unavailable"
// @Router /api/addons/pins [get]
func GetPins(gw *gateway.Gateway, db *gorm.DB, svc *pins.Service, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		user, ok := pinUser(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		visible, _ := visibleIDs(c, gw, db, s)
		list, err := svc.List(c.Request().Context(), user, visible)
		if err != nil {
			return failure(c, 503, "Pins unavailable")
		}
		return c.JSON(200, list)
	}
}

// PutPins replaces the caller's pinned add-on IDs.
// @Summary Replace the add-ons the caller pinned
// @Description Replaces the caller's pins with the ordered list in the body. IDs the caller may not see are dropped without any hint, and pins that are only hidden for now are kept. At most 32 IDs are accepted. Needs an authenticated caller from a same-origin browser request or a credential header, and no administrator role.
// @Tags Addons
// @Accept json
// @Produce json
// @Param pins body []string true "Ordered add-on IDs"
// @Success 200 {array} string "The saved list the caller can see"
// @Failure 400 {object} AddonErrorResponse "Not a list of strings, or longer than 32"
// @Failure 401 {object} AddonErrorResponse "Authentication required"
// @Failure 503 {object} AddonErrorResponse "The pin store or the add-on owner is unavailable"
// @Router /api/addons/pins [put]
func PutPins(gw *gateway.Gateway, db *gorm.DB, svc *pins.Service, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		user, ok := pinUser(c, db)
		if !ok {
			return failure(c, 401, "Authentication required")
		}
		var ids []string
		if err := decode(c, &ids); err != nil || ids == nil {
			return failure(c, 400, "A list of add-on IDs is required")
		}
		visible, complete := visibleIDs(c, gw, db, s)
		if !complete {
			// Dropping IDs because the owner did not answer would lose pins.
			return failure(c, 503, "Add-ons unavailable")
		}
		saved, err := svc.Put(c.Request().Context(), user, ids, visible)
		if errors.Is(err, pins.ErrTooMany) {
			return failure(c, 400, "Too many pins")
		}
		if err != nil {
			return failure(c, 503, "Pins unavailable")
		}
		return c.JSON(200, saved)
	}
}
```

`decode(c echo.Context, v any) error` is the helper at `core/http/endpoints/addons/gateway.go:109` (it bounds the body to 2 MiB). Check how it treats unknown fields and an empty body with `sed -n 105,125p core/http/endpoints/addons/gateway.go`; the tests above require an error for `not json`, an empty body, `{"a":1}` and `[1,2]`. If `decode` accepts an empty body, keep `ids == nil` as the check (it is nil for an empty body).

- [ ] **Step 8: Run the handler tests**

Run: `go test ./core/http/endpoints/addons/ -run 'Pins|PutDrops|PutRejects' -v`
Expected: PASS (4 tests).

- [ ] **Step 9: Register the routes and wire the store**

In `core/http/routes/addons.go` add imports `"path/filepath"` and `"github.com/mudler/LocalAI/core/services/addons/pins"` and replace `RegisterAddonRoutes` with:

```go
func RegisterAddonRoutes(e *echo.Echo, a *application.Application) {
	RegisterAddonRoutesFor(e, a.AddonGateway(), a.AuthDB(), a.AddonOwnershipMode())
	e.GET("/api/addons/builtin", endpoints.ListBuiltin(a.AuthDB()))
	store := newPinStore(a)
	RegisterAddonPinRoutes(e, a.AddonGateway(), a.AuthDB(), a.AddonOwnershipMode(), &pins.Service{Store: store})
}

// newPinStore picks the store: the shared account database when there is one,
// otherwise a file next to the other add-on data (one local owner, one host).
func newPinStore(a *application.Application) pins.Store {
	if db := a.AuthDB(); db != nil {
		if s, err := pins.NewGormStore(db); err == nil {
			return s
		}
	}
	path := ""
	if dir := a.ApplicationConfig().DataPath; dir != "" {
		path = filepath.Join(dir, "addon-pins.json")
	}
	s, _ := pins.NewFileStore(path)
	return s
}

// RegisterAddonPinRoutes registers the two user-scoped pin routes. They sit
// outside the addons feature group on purpose: a user who sees built-in tiles
// can pin them. The caller is filtered like the Apps list.
func RegisterAddonPinRoutes(e *echo.Echo, gw *gateway.Gateway, db *gorm.DB, ownership string, svc *pins.Service) {
	mode := endpoints.WithOwnership(ownership)
	e.GET("/api/addons/pins", endpoints.GetPins(gw, db, svc, mode))
	e.PUT("/api/addons/pins", endpoints.PutPins(gw, db, svc, mode), auth.CSRFMiddleware(), endpoints.BrowserProtection())
}
```

`pins.NewFileStore` returns an error only when the file exists and is unreadable; on that error `s` is nil and the line above would panic, so replace the last two lines of `newPinStore` with:

```go
	s, err := pins.NewFileStore(path)
	if err != nil {
		xlog.Error("addon pins file unreadable, pins are kept in memory only", "error", err)
		s, _ = pins.NewFileStore("")
	}
	return s
```

and import `"github.com/mudler/xlog"`.

- [ ] **Step 10: Run the route and MCP-map tests, then document**

Run: `go test ./core/http/routes/ ./core/http/endpoints/addons/ ./pkg/mcp/localaitools/ -count=1`
Expected: PASS. If `TestToolHTTPRouteMappingComplete` in `pkg/mcp/localaitools` lists `/api/addons/pins` as an unmapped route, add the two routes to the list of non-admin routes that need no tool in that test (`.agents/localai-assistant-mcp.md` says why): pins are a per-user UI preference, not an admin action.

In `docs/content/features/addons.md` document: both routes, the filtering rule, the 32 ID limit, the retention window (Task 4), and the distributed-state mode: "Shared. With an account database the table `addon_pins` is shared by every frontend. With authentication off there is one local owner and the pins are kept in `addon-pins.json` in the data path, per instance."

- [ ] **Step 11: Commit**

```bash
git add core/services/addons/pins core/http/endpoints/addons/pins.go core/http/endpoints/addons/pins_test.go core/http/routes/addons.go docs/content/features/addons.md pkg/mcp/localaitools
git commit -m "feat(addons): add the user-scoped pin routes

GET and PUT /api/addons/pins read and replace the caller's ordered pins.
IDs the caller cannot see are dropped without a hint, and pins that are
only hidden for now survive a save. The first read seeds Studio and Talk
once.

Assisted-by: Claude:claude-sonnet-5-5 go"
```

---

### Task 4: Retention of the pins of removed add-ons

**Files:**
- Create: `core/services/addons/pins/sweep.go`, `sweep_test.go`
- Modify: `core/services/addons/manager.go` (add `InstalledIDs` after `Apps`, near line 790-820)
- Modify: `core/services/addons/apps_test.go` (one `It` in the Describe that holds `lists apps by visibility`)
- Modify: `core/services/advisorylock/keys.go` (new key `KeyAddonPinSweep int64 = 109`)
- Modify: `core/application/addon_gateway.go` (start the sweep)

**Interfaces:**
- Consumes: `pins.Store`, `pins.Retention`, `builtin.IDs()`, `(*addons.Manager)` fields `mu`, `journal.Addons`, method `check(ctx)` as used in `Apps` (`core/services/addons/manager.go:790-799`), `advisorylock.RunLeaderLoop(ctx context.Context, db *gorm.DB, lockKey int64, interval time.Duration, fn func())` (`core/services/advisorylock/leader_loop.go:16`), `(*Application).AuthDB()`, `(*Application).AddonGateway()` whose `Manager` field is a `*addons.Manager` (`core/services/addons/gateway/gateway.go:21`).
- Produces:
  - `pins.Sweep(ctx context.Context, store Store, present []string, now time.Time) (deleted int64, err error)`: marks pinned IDs not in `present` as missing, clears the mark of IDs that are present again, deletes pins missing for longer than `Retention`.
  - `(*addons.Manager).InstalledIDs(ctx context.Context) ([]string, error)`: sorted IDs with `Installed == true`, including disabled ones.
  - `advisorylock.KeyAddonPinSweep`.

- [ ] **Step 1: Write the failing sweep test**

`core/services/addons/pins/sweep_test.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestSweepKeepsARemovedPinForThirtyDaysAndRestoresItOnReinstall(t *testing.T) {
	ctx := context.Background()
	s, _ := NewFileStore("")
	_ = s.Replace(ctx, "u1", []string{"studio", "notes"})
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// notes is uninstalled: the pin is marked, not deleted.
	if n, err := Sweep(ctx, s, []string{"studio"}, t0); err != nil || n != 0 {
		t.Fatalf("first sweep: %d %v", n, err)
	}
	// Day 29: still kept.
	if n, _ := Sweep(ctx, s, []string{"studio"}, t0.Add(29*24*time.Hour)); n != 0 {
		t.Fatalf("deleted inside the window: %d", n)
	}
	// The add-on is installed again on day 29: the mark is cleared.
	_, _ = Sweep(ctx, s, []string{"studio", "notes"}, t0.Add(29*24*time.Hour))
	if n, _ := Sweep(ctx, s, []string{"studio"}, t0.Add(40*24*time.Hour)); n != 0 {
		t.Fatalf("the window must restart after a reinstall: %d", n)
	}
	// 31 days after the new start it expires.
	n, _ := Sweep(ctx, s, []string{"studio"}, t0.Add(40*24*time.Hour).Add(Retention).Add(time.Hour))
	if n != 1 {
		t.Fatalf("expected the expired pin to be deleted, got %d", n)
	}
	got, _ := s.List(ctx, "u1")
	if !slices.Equal(idsOf(got), []string{"studio"}) {
		t.Fatalf("unexpected list %v", idsOf(got))
	}
}

func TestSweepNeverDeletesAPinWhenThePresentListIsEmptyDueToAnOutage(t *testing.T) {
	ctx := context.Background()
	s, _ := NewFileStore("")
	_ = s.Replace(ctx, "u1", []string{"notes"})
	// A nil present list means the caller could not read the owner: do nothing.
	if n, err := Sweep(ctx, s, nil, time.Now().Add(365*24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("a sweep without an answer must not delete: %d %v", n, err)
	}
	got, _ := s.List(ctx, "u1")
	if len(got) != 1 || got[0].MissingSince != nil {
		t.Fatal("a sweep without an answer must not mark either")
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./core/services/addons/pins/ -run Sweep -v`
Expected: FAIL with `undefined: Sweep`.

- [ ] **Step 3: Write the sweep**

`core/services/addons/pins/sweep.go`:

```go
// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"slices"
	"time"
)

// Sweep applies the retention rule. present is every add-on ID that exists
// right now: the built-in IDs and every installed add-on, enabled or not. A
// nil present means the owner did not answer, and then nothing changes: an
// outage must never expire a pin.
func Sweep(ctx context.Context, store Store, present []string, now time.Time) (int64, error) {
	if present == nil {
		return 0, nil
	}
	pinned, err := store.Distinct(ctx)
	if err != nil {
		return 0, err
	}
	var gone, back []string
	for _, id := range pinned {
		if slices.Contains(present, id) {
			back = append(back, id)
		} else {
			gone = append(gone, id)
		}
	}
	if err := store.ClearMissing(ctx, back); err != nil {
		return 0, err
	}
	if err := store.MarkMissing(ctx, gone, now); err != nil {
		return 0, err
	}
	return store.DeleteMissingBefore(ctx, now.Add(-Retention))
}
```

- [ ] **Step 4: Run the sweep tests**

Run: `go test ./core/services/addons/pins/ -count=1`
Expected: PASS.

- [ ] **Step 5: Add `InstalledIDs` with a test**

Add to `core/services/addons/apps_test.go`, in the same Describe as the case `lists apps by visibility and serves an icon only for a visible app at its generation`, using the same helpers `fixture()`, `open(cfg, c, i)` and `ctx`:

```go
	It("lists the identifiers of installed addons, disabled ones included", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		for id, st := range map[string]Status{
			"on":      {Installed: true, DesiredEnabled: true, Generation: 1},
			"off":     {Installed: true, Generation: 1},
			"removed": {Installed: false, Generation: 1},
		} {
			st.AddonID = id
			m.journal.Addons[id] = st
		}
		got, e := m.InstalledIDs(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"off", "on"}))
	})
```

Run: `go test ./core/services/addons/ -run Apps -count=1` (the Ginkgo suite: add `-ginkgo.focus "installed addons, disabled"` to focus).
Expected: FAIL with `m.InstalledIDs undefined`.

In `core/services/addons/manager.go`, after the `Apps` method (it ends before `func currentReceipt`), add (add `"sort"` to the imports if it is missing):

```go
// InstalledIDs returns the identifiers of every installed addon, enabled or
// not, sorted. It is for the host's own housekeeping (the pin sweep); it takes
// no actor and returns no detail.
func (m *Manager) InstalledIDs(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.check(ctx); e != nil {
		return nil, e
	}
	if m.uncertain || m.runtimeRecoveryRequired {
		return nil, ErrRecoveryRequired
	}
	out := make([]string, 0, len(m.journal.Addons))
	for id, st := range m.journal.Addons {
		if st.Installed {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}
```

Run the same test. Expected: PASS.

- [ ] **Step 6: Start the sweep**

In `core/services/advisorylock/keys.go` add after `KeyFailoverProber`: `KeyAddonPinSweep int64 = 109`.

In `core/application/addon_gateway.go` add (and call `a.startAddonPinSweep(ctx)` where the gateway is created and the application context is available; find the call with `grep -n "addonGateway =" core/application/*.go`):

```go
// startAddonPinSweep expires the pins of removed add-ons. With an account
// database one frontend at a time runs it (a non-sticky leader loop). Without
// one there is one process and a plain ticker runs it.
func (a *Application) startAddonPinSweep(ctx context.Context, store pins.Store) {
	gw := a.addonGateway
	if gw == nil || gw.Manager == nil || store == nil {
		return
	}
	run := func() {
		ids, err := gw.Manager.InstalledIDs(ctx)
		if err != nil {
			return // no answer: Sweep would do nothing with nil anyway
		}
		present := append(ids, builtin.IDs()...)
		if _, err := pins.Sweep(ctx, store, present, time.Now().UTC()); err != nil {
			xlog.Warn("addon pin sweep failed", "error", err)
		}
	}
	const every = 6 * time.Hour
	if db := a.authDB; db != nil {
		go advisorylock.RunLeaderLoop(ctx, db, advisorylock.KeyAddonPinSweep, every, run)
		return
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				run()
			}
		}
	}()
}
```

Import `builtin`, `pins`, `advisorylock` (`github.com/mudler/LocalAI/core/services/advisorylock`). The store must be the same one the routes use. Move the store creation out of `routes.newPinStore` into `(*Application).AddonPinStore() pins.Store` (create it once, cache it in a field `addonPinStore`) and have `RegisterAddonRoutes` call `a.AddonPinStore()`; call `startAddonPinSweep(ctx, a.AddonPinStore())` right after the gateway is built.

- [ ] **Step 7: Build, test, commit**

Run: `go build ./... && go test ./core/services/addons/... ./core/application/... ./core/http/routes/ -count=1`
Expected: PASS.

```bash
git add core/services/addons core/services/advisorylock/keys.go core/application core/http/routes/addons.go
git commit -m "feat(addons): expire the pins of removed add-ons after 30 days

A pin of an uninstalled add-on is hidden at once and kept for 30 days,
so a reinstall inside the window brings it back. One frontend sweeps
every six hours. A sweep that cannot read the owner changes nothing.

Assisted-by: Claude:claude-sonnet-5-5 go"
```

---

### Task 5: Pure navigation model (tiles, pins, sidebar rules) and the pins client

**Files:**
- Create: `core/http/react-ui/src/utils/addonTiles.js`
- Create: `core/http/react-ui/src/utils/navigationModel.js`
- Create: `core/http/react-ui/src/utils/pinsApi.js`
- Create: `core/http/react-ui/src/utils/pinsClient.js`
- Test: `core/http/react-ui/tests/unit/navigation.test.mjs`

**Interfaces:**
- Consumes: `localizedText(app, field, language) string` from `core/http/react-ui/src/components/addons/addonState.js:46`; `navigationItems(apps, { language }) Array<{addonId, page, path, label}>` from `core/http/react-ui/src/addons/registryState.js:63`; `apiUrl(path) string` from `core/http/react-ui/src/utils/basePath`.
- Produces (`addonTiles.js`):
  - `PIN_CAP = 8`.
  - `tileFromApp(app, { language }) -> Tile` and `tileFromBuiltin(entry) -> Tile` with `Tile = { id, source: 'app'|'builtin', title, titleKey, description, icon, iconName, state: 'running'|'stopped', statusLine: ''|'starting'|'failed', target, detailPath, app }`. `target` is the page a click opens: the main page only for a running app that has one, else `''`. `detailPath` is `/app/addons?detail=<id>` and is set whenever `target` is `''`.
  - `buildTiles(apps, builtin, { language }) -> Tile[]` (built-in first, then apps sorted by id, a built-in ID wins over an app with the same ID).
  - `filterTiles(tiles, query, titleOf) -> Tile[]` (matches title and description, case-insensitive).
  - `resolvePins(ids, tiles) -> Tile[]` (ordered; a pinned ID without a tile is left out).
  - `togglePin(ids, id, cap = PIN_CAP) -> { ids, needsUnpin }`.
  - `movePin(ids, id, delta) -> string[]`.
- Produces (`navigationModel.js`): `primaryDestinations` and `visiblePrimary(items, auth) -> item[]` where `auth = { isAdmin, authEnabled, hasFeature, features, hasBuiltin }`.
- Produces (`pinsApi.js`): `createPinsApi({ base, fetchImpl }) -> { listPins(signal), putPins(ids, signal), listBuiltin(signal) }`; `pinsClient.js` exports `pinsApi` bound to `apiUrl`.

- [ ] **Step 1: Write the failing unit tests**

`core/http/react-ui/tests/unit/navigation.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  PIN_CAP, tileFromApp, tileFromBuiltin, buildTiles, filterTiles, resolvePins, togglePin, movePin,
} from '../../src/utils/addonTiles.js'
import { primaryDestinations, visiblePrimary } from '../../src/utils/navigationModel.js'
import { createPinsApi } from '../../src/utils/pinsApi.js'

const app = (over = {}) => ({
  id: 'notes', title: 'Notes', description: 'Keep notes.', enabled: true, state: 'running',
  mainPage: '/app/addons/notes/home', generation: 1, ...over,
})

test('tileFromApp: a running add-on with a main page opens it', () => {
  const t = tileFromApp(app())
  assert.equal(t.state, 'running')
  assert.equal(t.target, '/app/addons/notes/home')
  assert.equal(t.detailPath, '')
})

test('tileFromApp never targets the main page of a stopped add-on', () => {
  for (const over of [{ enabled: false, state: 'stopped' }, { state: 'starting' }, { state: 'failed' }, { state: 'stopped' }]) {
    const t = tileFromApp(app(over))
    assert.equal(t.state, 'stopped', JSON.stringify(over))
    assert.equal(t.target, '', JSON.stringify(over))
    assert.equal(t.detailPath, '/app/addons?detail=notes')
  }
})

test('tileFromApp: starting and failed carry a status line, stopped does not', () => {
  assert.equal(tileFromApp(app({ state: 'starting' })).statusLine, 'starting')
  assert.equal(tileFromApp(app({ state: 'failed' })).statusLine, 'failed')
  assert.equal(tileFromApp(app({ enabled: false, state: 'stopped' })).statusLine, '')
})

test('tileFromApp: an add-on without a main page has no target and opens the detail view', () => {
  const t = tileFromApp(app({ mainPage: undefined }))
  assert.equal(t.target, '')
  assert.equal(t.detailPath, '/app/addons?detail=notes')
})

test('tileFromApp: the title falls back to the id', () => {
  assert.equal(tileFromApp(app({ title: '' })).title, 'notes')
})

test('buildTiles: built-in first, then apps by id, a built-in id wins', () => {
  const builtin = [{ id: 'studio', titleKey: 'items.studio', icon: 'palette', mainPath: '/app/studio' }]
  const tiles = buildTiles([app({ id: 'zed' }), app({ id: 'studio' }), app({ id: 'abc' })], builtin)
  assert.deepEqual(tiles.map(t => t.id), ['studio', 'abc', 'zed'])
  assert.equal(tiles[0].source, 'builtin')
  assert.equal(tiles[0].target, '/app/studio')
})

test('filterTiles matches title and description, case-insensitive', () => {
  const tiles = [tileFromApp(app()), tileFromApp(app({ id: 'x', title: 'Faces', description: 'Who is it' }))]
  const t = (tile) => tile.title
  assert.deepEqual(filterTiles(tiles, 'NOTE', t).map(x => x.id), ['notes'])
  assert.deepEqual(filterTiles(tiles, 'who', t).map(x => x.id), ['x'])
  assert.equal(filterTiles(tiles, '  ', t).length, 2)
})

test('resolvePins keeps the order and hides a pin without a tile', () => {
  const tiles = buildTiles([app({ id: 'a' }), app({ id: 'b' })], [])
  assert.deepEqual(resolvePins(['b', 'gone', 'a'], tiles).map(t => t.id), ['b', 'a'])
})

test('togglePin enforces the cap of eight and asks the user to unpin one', () => {
  const eight = Array.from({ length: PIN_CAP }, (_, i) => `a${i}`)
  assert.deepEqual(togglePin(eight, 'new'), { ids: eight, needsUnpin: true })
  assert.deepEqual(togglePin(['a'], 'b'), { ids: ['a', 'b'], needsUnpin: false })
  assert.deepEqual(togglePin(['a', 'b'], 'a'), { ids: ['b'], needsUnpin: false })
})

test('movePin moves one place and stops at the ends', () => {
  assert.deepEqual(movePin(['a', 'b', 'c'], 'c', -1), ['a', 'c', 'b'])
  assert.deepEqual(movePin(['a', 'b', 'c'], 'a', -1), ['a', 'b', 'c'])
  assert.deepEqual(movePin(['a', 'b', 'c'], 'c', 1), ['a', 'b', 'c'])
})

const user = { isAdmin: false, authEnabled: true, hasFeature: () => false, features: {}, hasBuiltin: false }

test('the sidebar has four primary items and Settings and Models are administrator items', () => {
  assert.deepEqual(primaryDestinations.map(i => i.path), ['/app/chat', '/app/models', '/app/addons', '/app/settings'])
  const admin = visiblePrimary(primaryDestinations, { ...user, isAdmin: true, hasFeature: () => true })
  assert.equal(admin.length, 4)
  const member = visiblePrimary(primaryDestinations, { ...user, hasBuiltin: true })
  assert.deepEqual(member.map(i => i.path), ['/app/chat', '/app/addons'])
})

test('addons entry shows for a built-in tile even without the addons feature, and hides with neither', () => {
  const paths = (a) => visiblePrimary(primaryDestinations, a).map(i => i.path)
  assert.ok(paths({ ...user, hasBuiltin: true }).includes('/app/addons'))
  assert.ok(paths({ ...user, hasFeature: (f) => f === 'addons' }).includes('/app/addons'))
  assert.ok(!paths(user).includes('/app/addons'))
})

test('pins api: reads and writes arrays, sends JSON, never throws on a malformed body', async () => {
  const calls = []
  const fetchImpl = async (url, init) => {
    calls.push([url, init?.method || 'GET', init?.body])
    if (url.endsWith('/api/addons/builtin')) return { ok: true, json: async () => ({ builtin: [{ id: 'studio' }] }) }
    return { ok: true, json: async () => (init?.method === 'PUT' ? JSON.parse(init.body) : ['studio', 7, null]) }
  }
  const api = createPinsApi({ base: (p) => `/base${p}`, fetchImpl })
  assert.deepEqual(await api.listPins(), ['studio'])
  assert.deepEqual(await api.putPins(['a', 'b']), ['a', 'b'])
  assert.deepEqual(await api.listBuiltin(), [{ id: 'studio' }])
  assert.equal(calls[1][0], '/base/api/addons/pins')
  assert.equal(calls[1][1], 'PUT')
  assert.equal(calls[1][2], '["a","b"]')
})

test('pins api: a failed answer is an error with the status', async () => {
  const api = createPinsApi({ base: (p) => p, fetchImpl: async () => ({ ok: false, status: 503, json: async () => ({}) }) })
  await assert.rejects(() => api.listPins(), (e) => e.status === 503)
})
```

- [ ] **Step 2: Run to see it fail**

Run: `cd core/http/react-ui && node tests/unit/run.mjs navigation.test.mjs`
Expected: a JSON line with `"status":"failed"` and the run exits 1 because `../../src/utils/addonTiles.js` does not exist (`ERR_MODULE_NOT_FOUND`).

- [ ] **Step 3: Write the model files**

`core/http/react-ui/src/utils/addonTiles.js`:

```js
// Pure rules of the Add-ons drawer and the Pinned section: how an app or a
// built-in entry becomes a tile, how tiles are searched and pinned. No React,
// no network.
import { localizedText } from '../components/addons/addonState.js'

// The sidebar shows at most this many pins. The drawer has no cap.
export const PIN_CAP = 8

function detailPathFor(id) {
  return `/app/addons?detail=${encodeURIComponent(id)}`
}

// A tile for an installed add-on. A tile never opens the main page of an
// add-on that is not running: the main page needs a live process.
export function tileFromApp(app, { language = 'en' } = {}) {
  const running = app.enabled === true && app.state === 'running'
  const state = running ? 'running' : 'stopped'
  const statusLine = app.enabled === true && (app.state === 'starting' || app.state === 'failed') ? app.state : ''
  const target = running && typeof app.mainPage === 'string' ? app.mainPage : ''
  return {
    id: app.id,
    source: 'app',
    title: localizedText(app, 'title', language) || app.id,
    titleKey: '',
    description: localizedText(app, 'description', language) || '',
    icon: app.icon || '',
    iconName: '',
    state,
    statusLine,
    target,
    detailPath: target ? '' : detailPathFor(app.id),
    app,
  }
}

// A tile for a built-in page add-on. Its page is a core route.
export function tileFromBuiltin(entry) {
  return {
    id: entry.id,
    source: 'builtin',
    title: entry.id,
    titleKey: entry.titleKey || '',
    description: '',
    icon: '',
    iconName: entry.icon || 'puzzle',
    state: 'running',
    statusLine: '',
    target: entry.mainPath,
    detailPath: '',
    app: null,
  }
}

export function buildTiles(apps, builtin, { language = 'en' } = {}) {
  const first = (builtin || []).map(tileFromBuiltin)
  const taken = new Set(first.map(t => t.id))
  const rest = [...(apps || [])]
    .filter(a => !taken.has(a.id))
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
    .map(a => tileFromApp(a, { language }))
  return [...first, ...rest]
}

// titleOf draws the displayed title (a built-in title is a translation).
export function filterTiles(tiles, query, titleOf = (t) => t.title) {
  const q = String(query || '').trim().toLowerCase()
  if (!q) return tiles
  return tiles.filter(t => titleOf(t).toLowerCase().includes(q) || t.description.toLowerCase().includes(q))
}

// The pinned tiles, in the order the user chose. A pin without a tile (hidden,
// removed) is left out silently.
export function resolvePins(ids, tiles) {
  const byId = new Map(tiles.map(t => [t.id, t]))
  return (ids || []).map(id => byId.get(id)).filter(Boolean)
}

// Pin or unpin. At the cap, a new pin is refused and the caller asks the user
// to unpin one.
export function togglePin(ids, id, cap = PIN_CAP) {
  if (ids.includes(id)) return { ids: ids.filter(x => x !== id), needsUnpin: false }
  if (ids.length >= cap) return { ids, needsUnpin: true }
  return { ids: [...ids, id], needsUnpin: false }
}

export function movePin(ids, id, delta) {
  const i = ids.indexOf(id)
  const j = i + delta
  if (i < 0 || j < 0 || j >= ids.length) return ids
  const next = [...ids]
  ;[next[i], next[j]] = [next[j], next[i]]
  return next
}
```

`core/http/react-ui/src/utils/navigationModel.js`:

```js
// Pure rules of the four-item sidebar. No React, no imports, so a node test
// drives it directly.

// Chat, Models, Add-ons and Settings. Home and the Build and Operate hubs keep
// the place they have today (see hubConfig.js and destinations.js).
export const primaryDestinations = Object.freeze([
  { path: '/app/chat', icon: 'chat', labelKey: 'items.chat' },
  { path: '/app/models', icon: 'boxes', labelKey: 'items.models', adminOnly: true },
  // Shown to a viewer who holds the addons feature or sees a built-in tile.
  { path: '/app/addons', icon: 'puzzle', labelKey: 'items.addons', addonsEntry: true },
  { path: '/app/settings', icon: 'settings', labelKey: 'items.settings', adminOnly: true },
])

export function visiblePrimary(items, { isAdmin, hasFeature, hasBuiltin }) {
  return items.filter(item => {
    if (item.adminOnly && !isAdmin) return false
    if (item.addonsEntry && !isAdmin && !hasFeature('addons') && !hasBuiltin) return false
    return true
  })
}
```

`core/http/react-ui/src/utils/pinsApi.js`:

```js
// Client of the pin and built-in routes. The base and fetch are injected so a
// node test drives it without a browser.
async function readJson(response) {
  if (!response.ok) {
    const e = new Error(`request failed with ${response.status}`)
    e.status = response.status
    throw e
  }
  return response.json()
}

const onlyStrings = (v) => (Array.isArray(v) ? v.filter(x => typeof x === 'string') : [])

export function createPinsApi({ base, fetchImpl = (...a) => fetch(...a) }) {
  const init = (extra = {}) => ({ credentials: 'same-origin', ...extra })
  return {
    async listPins(signal) {
      return onlyStrings(await readJson(await fetchImpl(base('/api/addons/pins'), init({ signal }))))
    },
    async putPins(ids, signal) {
      const response = await fetchImpl(base('/api/addons/pins'), init({
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(ids), signal,
      }))
      return onlyStrings(await readJson(response))
    },
    async listBuiltin(signal) {
      const body = await readJson(await fetchImpl(base('/api/addons/builtin'), init({ signal })))
      return Array.isArray(body?.builtin) ? body.builtin : []
    },
  }
}
```

`core/http/react-ui/src/utils/pinsClient.js`:

```js
// The browser's client of the pin routes, bound to the page's base path.
import { apiUrl } from './basePath'
import { createPinsApi } from './pinsApi'

export const pinsApi = createPinsApi({ base: apiUrl })
```

- [ ] **Step 4: Run to see it pass**

Run: `cd core/http/react-ui && node tests/unit/run.mjs navigation.test.mjs`
Expected: one `{"case":"unit: ...","status":"passed"}` line per test and exit code 0.

- [ ] **Step 5: Commit**

```bash
git add core/http/react-ui/src/utils/addonTiles.js core/http/react-ui/src/utils/navigationModel.js core/http/react-ui/src/utils/pinsApi.js core/http/react-ui/src/utils/pinsClient.js core/http/react-ui/tests/unit/navigation.test.mjs
git commit -m "feat(ui): add the pure rules of the add-ons drawer

Tile states, search, pins with a cap of eight, the four-item sidebar
and the browser client of the pin routes, all testable in node.

Assisted-by: Claude:claude-sonnet-5-5 node"
```

---

### Task 6: The drawer, the Pinned section, the four-item sidebar and the legacy flag

**Files:**
- Create: `core/http/react-ui/src/context/NavigationContext.jsx`
- Create: `core/http/react-ui/src/components/PinnedNav.jsx`
- Create: `core/http/react-ui/src/components/addons/AddonTile.jsx`
- Modify: `core/http/react-ui/src/components/addons/AppsView.jsx` (whole file: the list becomes the drawer; polling code at lines 29-70 stays)
- Modify: `core/http/react-ui/src/components/Sidebar.jsx:10-32,153-161,211-216` (four items, Pinned, flag)
- Modify: `core/http/react-ui/src/utils/destinations.js:5-24` (404 page list)
- Modify: `core/http/react-ui/src/components/hub/hubConfig.js:65-76` (remove the `addons` tab from the Build hub)
- Modify: `core/http/react-ui/src/router.jsx:241-244,319` (route out of the hub, gate, provider)
- Modify: `core/http/react-ui/src/components/addons/addons.css` (tile, pin, drawer styles)
- Modify: `core/http/react-ui/public/locales/en/addons.json`, `core/http/react-ui/public/locales/en/nav.json` (new keys)
- Modify: `core/config/application_config.go` (option `WithLegacyNavigation`), `core/cli/run.go` (flag), `core/http/routes/localai.go:471-479` (`/api/features` key)
- Modify: `docs/content/features/addons.md` (sidebar and drawer description, the flag)
- Test: `core/http/react-ui/e2e/addon-navigation.spec.js` (created here, extended in Tasks 7 and 8)

**Interfaces:**
- Consumes: Task 5 (`buildTiles`, `filterTiles`, `resolvePins`, `togglePin`, `movePin`, `PIN_CAP`, `visiblePrimary`, `primaryDestinations`, `pinsApi`); `useAuth()` returns `{ isAdmin, authEnabled, hasFeature, loading, user }` (`core/http/react-ui/src/context/AuthContext.jsx:57-65`); `useAddonRegistry()` returns `{ apps, instances, acquire, ... }` (`core/http/react-ui/src/addons/AddonRoute.jsx:36-39`); `FEATURE_ADDONS` from `core/http/react-ui/src/utils/capabilities`; `navigationItems(apps, {language})` (`registryState.js:63`); `StateChip`, `AppIcon` (`components/addons/AddonBits.jsx`); `useAddonActions` (`components/addons/useAddonActions.js`); `SideSheet` (`components/SideSheet.jsx`); `appActions(app)` (`addonState.js:38`).
- Produces:
  - `NavigationProvider({ children })` and `useNavigation() -> { status: 'idle'|'loading'|'ready'|'error', builtin: Entry[], pinIds: string[], setPins(ids): Promise<void>, pin(id): Promise<{needsUnpin: boolean}>, unpin(id): Promise<void>, move(id, delta): Promise<void> }`. Outside the provider `useNavigation()` returns a stable empty value (`builtin: []`, `pinIds: []`).
  - `PinnedNav({ onClose, collapsed })` (the Pinned section).
  - `AddonTile({ tile, pinned, onPin, onMove, onOpenDetail })`; `onMove(id, delta)` reorders a pinned tile by one place.
  - Server: `config.WithLegacyNavigation(bool) AppOption`, field `ApplicationConfig.LegacyNavigation bool`, CLI flag `LOCALAI_LEGACY_NAVIGATION`, and the `/api/features` key `addon_navigation` (true unless the flag is set).
  - Test ids: `pinned-nav`, `addon-tile`, `pin-toggle`, `addon-search`, `addon-detail`, `addons-browse`, `addons-add`.

This task changes behaviour you can only see in a browser. The pure parts are covered by Task 5. The e2e test below drives the rest.

- [ ] **Step 1: Write the failing e2e test**

Create `core/http/react-ui/e2e/addon-navigation.spec.js`. It reuses the helpers the add-on shell spec uses (`MEMBER`, `app`, `sessionFor`, `mockAddons` from `./addons-fixtures.js`); check their exact signatures first with `sed -n 1,80p core/http/react-ui/e2e/addons-fixtures.js` and adapt the two `page.route` calls for `/api/addons/apps` if `mockAddons` already installs them.

```js
import { test, expect } from '@playwright/test'
import { MEMBER, app, sessionFor, mockAddons } from './addons-fixtures.js'

const STUDIO = { id: 'studio', kind: 'page', titleKey: 'items.studio', icon: 'palette', mainPath: '/app/studio' }
const TALK = { id: 'talk', kind: 'page', titleKey: 'items.talk', icon: 'phone', mainPath: '/app/talk' }

async function setup(page, { apps = [], pins = ['studio', 'talk'], addons = true, builtin = [STUDIO, TALK] } = {}) {
  let saved = pins
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons }) }))
  await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: true } }))
  await page.route('**/api/addons/builtin', route => route.fulfill({ json: { builtin } }))
  // mockAddons installs the Apps routes with the real response shape ({ apps }).
  await mockAddons(page, { apps })
  await page.route('**/api/addons/pins', async route => {
    if (route.request().method() === 'PUT') saved = route.request().postDataJSON()
    await route.fulfill({ json: saved })
  })
  return { saved: () => saved }
}

test.describe('Add-on navigation', () => {
  test('the sidebar shows Chat, Add-ons and the Pinned section', async ({ page }) => {
    await setup(page)
    await page.goto('/app')
    const nav = page.getByRole('navigation')
    await expect(nav.getByRole('link', { name: 'Chat' })).toBeVisible()
    await expect(nav.getByRole('link', { name: 'Add-ons' })).toBeVisible()
    const pinned = page.getByTestId('pinned-nav')
    await expect(pinned.getByRole('link', { name: 'Studio' })).toBeVisible()
    await expect(pinned.getByRole('link', { name: 'Talk' })).toBeVisible()
  })

  test('an empty Pinned section is hidden', async ({ page }) => {
    await setup(page, { pins: [] })
    await page.goto('/app')
    await expect(page.getByTestId('pinned-nav')).toHaveCount(0)
  })

  test('the drawer lists tiles, searches them and pins from the keyboard', async ({ page }) => {
    const server = await setup(page, { pins: [], apps: [app({ id: 'notes', title: 'Notes', state: 'running', mainPage: '/app/addons/notes/home' })] })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile')).toHaveCount(3)
    await page.getByTestId('addon-search').fill('note')
    await expect(page.getByTestId('addon-tile')).toHaveCount(1)
    const toggle = page.getByTestId('pin-toggle').first()
    await expect(toggle).toHaveAttribute('aria-pressed', 'false')
    await expect(toggle).toHaveAccessibleName(/Pin Notes/)
    await toggle.focus()
    await page.keyboard.press('Enter')
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    expect(server.saved()).toEqual(['notes'])
  })

  test('a stopped tile opens the detail view and never the main page', async ({ page }) => {
    await setup(page, { apps: [app({ id: 'notes', title: 'Notes', enabled: false, state: 'stopped', mainPage: undefined })] })
    await page.goto('/app/addons')
    await page.getByTestId('addon-tile').filter({ hasText: 'Notes' }).getByRole('button', { name: /Notes/ }).click()
    await expect(page.getByTestId('addon-detail')).toBeVisible()
    expect(page.url()).toContain('/app/addons?detail=notes')
  })

  test('a pinned tile can be moved later and the new order is saved', async ({ page }) => {
    const server = await setup(page, { pins: ['studio', 'talk'] })
    await page.goto('/app/addons')
    await page.getByTestId('addon-tile').filter({ hasText: 'Studio' }).getByTestId('pin-later').click()
    await expect.poll(() => server.saved()).toEqual(['talk', 'studio'])
  })

  test('the ninth pin asks the user to unpin one', async ({ page }) => {
    const nine = Array.from({ length: 9 }, (_, i) => app({ id: `a${i}`, title: `App ${i}`, state: 'running', mainPage: `/app/addons/a${i}/home` }))
    const eight = nine.slice(0, 8).map(a => a.id)
    await setup(page, { apps: nine, pins: eight })
    await page.goto('/app/addons')
    await page.getByTestId('addon-tile').filter({ hasText: 'App 8' }).getByTestId('pin-toggle').click()
    await expect(page.getByRole('alertdialog').or(page.getByRole('dialog'))).toContainText(/unpin/i)
  })

  test('member without the add-ons feature sees the built-in tiles and no error', async ({ page }) => {
    let appsCalled = false
    await setup(page, { addons: false })
    // Registered after setup, so it wins over the mocked Apps route.
    await page.route(/\/api\/addons\/apps$/, route => { appsCalled = true; return route.fulfill({ status: 403, json: {} }) })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile')).toHaveCount(2)
    await expect(page.getByTestId('addons-browse')).toHaveCount(0)
    await expect(page.getByRole('alert')).toHaveCount(0)
    expect(appsCalled).toBe(false)
  })

  test('the legacy flag brings the old sidebar back', async ({ page }) => {
    await setup(page)
    await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: false } }))
    await page.goto('/app')
    await expect(page.getByTestId('pinned-nav')).toHaveCount(0)
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Studio' })).toBeVisible()
  })

  test('the layout holds at phone width', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 700 })
    await setup(page)
    await page.goto('/app/addons')
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
    expect(overflow).toBe(false)
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd core/http/react-ui && npx playwright test e2e/addon-navigation.spec.js`
Expected: FAIL. `getByTestId('pinned-nav')` and `addon-tile` find nothing; the first test fails on `Pinned`.

- [ ] **Step 3: Add the server flag**

In `core/config/application_config.go` add the field `LegacyNavigation bool` to `ApplicationConfig` (next to `DisableLocalAIAssistant` at line 262) and, after `WithDisableLocalAIAssistant` (line 1178):

```go
// WithLegacyNavigation brings the old sidebar back without a rebuild. The UI
// reads it from /api/features. It is a rollback switch and is removed one
// release after the add-on navigation ships.
func WithLegacyNavigation(on bool) AppOption {
	return func(o *ApplicationConfig) {
		o.LegacyNavigation = on
	}
}
```

In `core/cli/run.go` after line 135 add `LegacyNavigation bool `env:"LOCALAI_LEGACY_NAVIGATION" default:"false" help:"Show the previous sidebar instead of Chat, Models, Add-ons and Settings with pinned add-ons" group:"ui"``, and after the block at line 591-593:

```go
	if r.LegacyNavigation {
		opts = append(opts, config.WithLegacyNavigation(true))
	}
```

In `core/http/routes/localai.go:471-479` add the key `"addon_navigation": !appConfig.LegacyNavigation,` to the map. If a Go test asserts the exact key set of `/api/features`, extend it: `grep -rn '"localai_assistant"' core --include=*_test.go`.

Run: `go build ./... && go test ./core/config/ ./core/http/routes/ -count=1`
Expected: PASS.

- [ ] **Step 4: Write the navigation context**

`core/http/react-ui/src/context/NavigationContext.jsx`:

```jsx
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useAuth } from './AuthContext'
import { pinsApi } from '../utils/pinsClient'
import { PIN_CAP, togglePin, movePin } from '../utils/addonTiles'

// What the shell needs to draw the Add-ons item, the drawer and the Pinned
// section: the built-in catalog and the caller's pins. Both come from the
// server, so a pin follows the user across browsers.
const EMPTY = Object.freeze({
  status: 'idle', builtin: [], pinIds: [],
  setPins: async () => {}, pin: async () => ({ needsUnpin: false }), unpin: async () => {}, move: async () => {},
})
const NavigationContext = createContext(EMPTY)

// eslint-disable-next-line react-refresh/only-export-components
export function useNavigation() {
  return useContext(NavigationContext)
}

export function NavigationProvider({ children }) {
  const { loading, authEnabled, user } = useAuth()
  const [state, setState] = useState({ status: 'idle', builtin: [], pinIds: [] })
  const pinsRef = useRef([])
  pinsRef.current = state.pinIds
  const signedIn = !loading && (!authEnabled || Boolean(user))

  useEffect(() => {
    if (!signedIn) return undefined
    const ctl = new AbortController()
    setState(s => ({ ...s, status: 'loading' }))
    Promise.all([pinsApi.listBuiltin(ctl.signal), pinsApi.listPins(ctl.signal)])
      .then(([builtin, pinIds]) => setState({ status: 'ready', builtin, pinIds }))
      .catch(() => { if (!ctl.signal.aborted) setState(s => ({ ...s, status: 'error' })) })
    return () => ctl.abort()
  }, [signedIn, user?.id])

  const setPins = useCallback(async (ids) => {
    const previous = pinsRef.current
    setState(s => ({ ...s, pinIds: ids }))
    try {
      const saved = await pinsApi.putPins(ids)
      setState(s => ({ ...s, pinIds: saved }))
    } catch (error) {
      setState(s => ({ ...s, pinIds: previous }))
      throw error
    }
  }, [])

  const pin = useCallback(async (id) => {
    const next = togglePin(pinsRef.current.filter(x => x !== id), id, PIN_CAP)
    if (next.needsUnpin) return { needsUnpin: true }
    await setPins(next.ids)
    return { needsUnpin: false }
  }, [setPins])
  const unpin = useCallback((id) => setPins(pinsRef.current.filter(x => x !== id)), [setPins])
  const move = useCallback((id, delta) => setPins(movePin(pinsRef.current, id, delta)), [setPins])

  const value = useMemo(() => ({ ...state, setPins, pin, unpin, move }), [state, setPins, pin, unpin, move])
  return <NavigationContext.Provider value={value}>{children}</NavigationContext.Provider>
}
```

- [ ] **Step 5: Write the tile and the Pinned section**

`core/http/react-ui/src/components/addons/AddonTile.jsx`:

```jsx
/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { apiUrl } from '../../utils/basePath'
import { AppIcon } from './AddonBits'

// One tile of the drawer: icon, name, state and the pin control. A running
// tile opens the add-on; every other tile opens the detail view. The pin
// control is a real button with a name and a state.
export default function AddonTile({ tile, pinned, onPin, onMove, onOpenDetail }) {
  const { t } = useTranslation('addons')
  const { t: tn } = useTranslation('nav')
  const title = tile.titleKey ? tn(tile.titleKey) : tile.title
  const stateText = tile.state === 'running' ? t('drawer.running') : t('drawer.stopped')
  const line = tile.statusLine ? t(`drawer.status.${tile.statusLine}`) : ''
  const body = (
    <>
      {tile.source === 'builtin'
        ? <span className="ad-icon" aria-hidden="true"><Icon name={tile.iconName} /></span>
        : <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} />}
      <span className="ad-tile__text">
        <span className="ad-tile__title">{title}</span>
        <span className="ad-tile__state" data-state={tile.state}>{stateText}{line ? `, ${line}` : ''}</span>
      </span>
    </>
  )
  return (
    <li className="ad-tile" data-testid="addon-tile" data-app={tile.id} data-state={tile.state}>
      {tile.target
        ? <Link className="ad-tile__open" to={tile.target}>{body}</Link>
        : <button type="button" className="ad-tile__open" onClick={() => onOpenDetail(tile.id)} aria-label={`${title}, ${stateText}`}>{body}</button>}
      {pinned && (
        <span className="ad-tile__order">
          <button type="button" className="ad-tile__move" data-testid="pin-earlier" aria-label={t('drawer.moveEarlier', { name: title })} onClick={() => onMove(tile.id, -1)}><Icon name="chevron-up" aria-hidden="true" /></button>
          <button type="button" className="ad-tile__move" data-testid="pin-later" aria-label={t('drawer.moveLater', { name: title })} onClick={() => onMove(tile.id, 1)}><Icon name="chevron-down" aria-hidden="true" /></button>
        </span>
      )}
      <button
        type="button"
        className="ad-tile__pin"
        data-testid="pin-toggle"
        aria-pressed={pinned}
        aria-label={pinned ? t('drawer.unpin', { name: title }) : t('drawer.pin', { name: title })}
        onClick={() => onPin(tile.id)}
      >
        <Icon name="star" aria-hidden="true" />
      </button>
    </li>
  )
}
```

`core/http/react-ui/src/components/PinnedNav.jsx`:

```jsx
/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo } from 'react'
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import { useNavigation } from '../context/NavigationContext'
import { useAddonRegistry } from '../addons/useAddonRegistry'
import { navigationItems } from '../addons/registryState'
import { buildTiles, resolvePins } from '../utils/addonTiles'
import { apiUrl } from '../utils/basePath'

// The Pinned section of the sidebar: the caller's pins in the chosen order.
// Each pin is resolved against the built-in catalog and the Apps list at
// render time, so a stopped add-on is muted, and a removed or hidden one is
// not drawn at all. An empty section is not drawn.
export default function PinnedNav({ onClose, collapsed }) {
  const { t } = useTranslation('nav')
  const { t: ta, i18n } = useTranslation('addons')
  const { builtin, pinIds } = useNavigation()
  const registry = useAddonRegistry()
  const { acquire } = registry
  const needsApps = pinIds.some(id => !builtin.some(b => b.id === id))
  // Resolving an add-on pin needs the Apps list once; the registry keeps it
  // current while an add-on contributes UI.
  useEffect(() => (needsApps ? acquire() : undefined), [needsApps, acquire])

  const tiles = useMemo(() => buildTiles(registry.apps, builtin, { language: i18n.language }), [registry.apps, builtin, i18n.language])
  const pinned = useMemo(() => resolvePins(pinIds, tiles), [pinIds, tiles])
  const extra = useMemo(() => navigationItems(registry.apps, { language: i18n.language }), [registry.apps, i18n.language])
  if (pinned.length === 0) return null

  return (
    <div className="sidebar-section" data-testid="pinned-nav">
      {!collapsed && <div className="sidebar-section-title">{t('sections.pinned')}</div>}
      <div className="sidebar-section-items">
        {pinned.map(tile => {
          const title = tile.titleKey ? t(tile.titleKey) : tile.title
          const to = tile.target || tile.detailPath
          const links = extra.filter(x => x.addonId === tile.id && x.path !== tile.target)
          return (
            <div key={tile.id}>
              <NavLink
                to={to}
                onClick={onClose}
                className={({ isActive }) => `nav-item${isActive ? ' active' : ''}${tile.state === 'stopped' ? ' nav-item--muted' : ''}`}
                title={collapsed ? title : undefined}
              >
                {tile.source === 'builtin' || !tile.icon
                  ? <Icon name={tile.iconName || 'puzzle'} className="nav-icon" aria-hidden="true" />
                  : <img className="nav-icon" src={apiUrl(tile.icon)} alt="" width="18" height="18" />}
                <span className="nav-label">{title}</span>
                {tile.state === 'stopped' && <span className="sr-only">{ta('drawer.stopped')}</span>}
              </NavLink>
              {tile.state === 'running' && links.map(link => (
                <NavLink key={link.path} to={link.path} onClick={onClose} className="nav-item nav-item--sub">
                  <span className="nav-label">{link.label}</span>
                </NavLink>
              ))}
            </div>
          )
        })}
      </div>
    </div>
  )
}
```

- [ ] **Step 6: Turn the Apps tab into the drawer**

In `core/http/react-ui/src/components/addons/AppsView.jsx`:

1. Keep the polling effect (lines 29-70) and `useAddonActions`/`startStop` (lines 72-85). Change the polling effect so it does nothing for a viewer without the feature (there is no list to read and the route would answer 403):

```jsx
  const { hasFeature, isAdmin } = useAuth()
  const canList = hasFeature(FEATURE_ADDONS)
  // ...inside the existing useEffect, as its first lines:
  //   if (!canList) { setState({ status: 'ready', apps: [], error: null, stale: false }); return undefined }
  // and add canList to the dependency array: [api, reload, canList]
```

Add the imports `import { useAuth } from '../../context/AuthContext'`, `import { FEATURE_ADDONS } from '../../utils/capabilities'`, `import { useSearchParams } from 'react-router-dom'` (extend the existing `react-router-dom` import), `import SideSheet from '../SideSheet'`, `import AddonTile from './AddonTile'`, `import { useNavigation } from '../../context/NavigationContext'` and `import { buildTiles, filterTiles, resolvePins } from '../../utils/addonTiles'`.

2. Replace everything from `const sorted = useMemo(...)` (line 87) to the end of the `AppsView` function (line 126, the closing brace before `function AppCard`) with:

```jsx
  const nav = useNavigation()
  const [query, setQuery] = useState('')
  const [params, setParams] = useSearchParams()
  const [limit, setLimit] = useState(false)
  const detailId = params.get('detail') || ''
  const { t: tn } = useTranslation('nav')

  const tiles = useMemo(() => buildTiles(state.apps, nav.builtin, { language: i18n.language }), [state.apps, nav.builtin, i18n.language])
  const titleOf = useCallback((tile) => (tile.titleKey ? tn(tile.titleKey) : tile.title), [tn])
  const shown = useMemo(() => filterTiles(tiles, query, titleOf), [tiles, query, titleOf])
  const pinnedIds = useMemo(() => new Set(resolvePins(nav.pinIds, tiles).map(x => x.id)), [nav.pinIds, tiles])
  const detail = tiles.find(x => x.id === detailId) || null

  const onPin = async (id) => {
    if (pinnedIds.has(id)) { await nav.unpin(id); return }
    const result = await nav.pin(id)
    if (result.needsUnpin) setLimit(true)
  }
  const openDetail = (id) => setParams(prev => { const out = new URLSearchParams(prev); out.set('detail', id); return out })
  const closeDetail = () => setParams(prev => { const out = new URLSearchParams(prev); out.delete('detail'); return out }, { replace: true })

  if (state.status === 'loading') {
    return <div className="op-loading" role="status" aria-label={t('apps.loading')}><LoadingSpinner size="lg" /></div>
  }
  if (state.status === 'error') {
    return (
      <div className="op-error" role="alert">
        <Icon name="alert-circle" />
        <span>{describeError(state.error, t)}</span>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => { setState({ status: 'loading', apps: [], error: null, stale: false }); setReload(n => n + 1) }}>
          <Icon name="refresh" /> {t('catalog.retry')}
        </button>
      </div>
    )
  }

  return (
    <div className="ad-apps">
      {state.stale && (
        <div className="op-notice" role="status">
          <Icon name="alert-circle" />
          <span>{t('apps.stale')}</span>
        </div>
      )}
      <div className="ad-drawer__bar">
        <input
          type="search"
          className="dk-input ad-drawer__search"
          data-testid="addon-search"
          aria-label={t('drawer.search')}
          placeholder={t('drawer.search')}
          value={query}
          onChange={e => setQuery(e.target.value)}
        />
        {isAdmin && (
          <>
            <Link className="dk-btn dk-btn--sm" to="/app/addons?tab=browse" data-testid="addons-browse"><Icon name="search" /> {t('drawer.browse')}</Link>
            <Link className="dk-btn dk-btn--primary dk-btn--sm" to="/app/addons?tab=browse" data-testid="addons-add"><Icon name="plus" /> {t('drawer.add')}</Link>
          </>
        )}
      </div>
      {shown.length === 0 ? (
        <CatalogEmpty icon="puzzle" title={t('apps.emptyTitle')} text={t('apps.emptyBody')} />
      ) : (
        <ul className="ad-tiles" aria-label={t('apps.listLabel')}>
          {shown.map(tile => (
            <AddonTile key={tile.id} tile={tile} pinned={pinnedIds.has(tile.id)} onPin={onPin} onMove={nav.move} onOpenDetail={openDetail} />
          ))}
        </ul>
      )}
      {detail && (
        <SideSheet title={titleOf(detail)} onClose={closeDetail} closeLabel={t('drawer.close')} testId="addon-detail">
          {detail.app
            ? <ul className="ad-grid"><AppCard app={detail.app} t={t} language={i18n.language} actions={actions} onStartStop={startStop} /></ul>
            : <p>{t('drawer.builtinDetail')}</p>}
        </SideSheet>
      )}
      {limit && (
        <SideSheet title={t('drawer.limitTitle')} onClose={() => setLimit(false)} closeLabel={t('drawer.close')} testId="pin-limit">
          <p>{t('drawer.limitBody')}</p>
        </SideSheet>
      )}
    </div>
  )
}
```

`SideSheet` already renders as a `role="dialog"` (see its header comment in `components/SideSheet.jsx`); the e2e test accepts `alertdialog` or `dialog`. The detail sheet shows the existing `AppCard` (start and stop for an administrator, tools, version). The sheet for a built-in tile has only the text `drawer.builtinDetail`.

3. The "ui.navigation" links of an add-on appear in the detail view: below `AppCard` in the `detail.app` branch add

```jsx
            {navigationItems([detail.app], { language: i18n.language }).map(link => (
              <Link key={link.path} className="dk-btn dk-btn--ghost dk-btn--sm" to={link.path}>{link.label}</Link>
            ))}
```

and import `navigationItems` from `'../../addons/registryState'`.

- [ ] **Step 7: Move the route and the gate, remove the hub tab, provide the context**

`core/http/react-ui/src/router.jsx`:

- Remove lines 241-244 (the two `addons` routes and their comment) from the Build hub `children`.
- Add after the `account` route (line 217) two top-level routes, outside any hub, so the drawer is a full page without the hub tab bar:

```jsx
  // The Add-ons drawer. A viewer without the addons feature still reaches it
  // when a built-in tile (Studio, Talk) is theirs to open, so no one loses a
  // page they can open today.
  { path: 'addons', element: <DrawerGate><Addons /></DrawerGate> },
  { path: 'addons/:addonId/*', element: <Feature feature="addons"><AddonRoute /></Feature> },
```

- Add next to `Feature` (line 190):

```jsx
function DrawerGate({ children }) {
  const { isAdmin, hasFeature, loading } = useAuth()
  const { builtin, status } = useNavigation()
  if (loading || status === 'idle' || status === 'loading') return null
  if (!isAdmin && !hasFeature('addons') && builtin.length === 0) return <Navigate to="/app" replace />
  return children
}
```

  and import `useAuth` from `./context/AuthContext` and `useNavigation` from `./context/NavigationContext`. If the built-in request fails (`status === 'error'`), `builtin` is empty and a viewer without the feature goes to `/app`, which is the safe side.
- Wrap the app element (line 319): `<RequireAuth><AddonRegistryProvider><NavigationProvider><App /></NavigationProvider></AddonRegistryProvider></RequireAuth>` and import `NavigationProvider`.

`core/http/react-ui/src/components/hub/hubConfig.js`: delete the `addons` tab (lines 65-76 `{ id: 'addons', ... addonNavigation: true }`), and delete the now unused import `FEATURE_ADDONS` (line 31) if nothing else in the file uses it (`grep -n FEATURE_ADDONS core/http/react-ui/src/components/hub/hubConfig.js`). Leave the `addonNavigation` branch of `visibleItems` in place: it is inert without a tab that sets it, and the third-party links now appear in the detail view and the pinned group.

`core/http/react-ui/src/utils/destinations.js`: so the 404 page lists the same places as the sidebar, replace `createDestinations` consumers: keep the exports (the legacy sidebar uses them) and add

```js
import { primaryDestinations, visiblePrimary } from './navigationModel.js'

// The four-item layout: Home, then Chat, Models, Add-ons and Settings, then the hubs.
export function destinationsForNavigation(auth) {
  const hubItems = hubs.flatMap(hub => {
    const path = hubEntryPath(hub, auth)
    return path ? [{ path, icon: hub.icon, labelKey: hub.titleKey }] : []
  })
  return [
    ...topDestinations.filter(i => i.path === '/app'),
    ...visiblePrimary(primaryDestinations, auth),
    ...hubItems,
  ]
}
```

and in `core/http/react-ui/src/pages/NotFound.jsx:17` call `destinationsForNavigation` when the flag is on (`features.addon_navigation !== false`) and `destinationsFor` otherwise, passing `hasBuiltin: useNavigation().builtin.length > 0` in `auth`. (`NotFound.jsx` builds `auth` at lines 10-16; add the field there.)

- [ ] **Step 8: Draw the sidebar**

In `core/http/react-ui/src/components/Sidebar.jsx`:

- Imports: `import { primaryDestinations, visiblePrimary } from '../utils/navigationModel'`, `import { useNavigation } from '../context/NavigationContext'`, `import PinnedNav from './PinnedNav'`.
- After `const { operations } = useOperations()` (line 86) add `const { builtin } = useNavigation()`.
- The flag: `features` already holds the answer of `/api/features` (line 80, 92-94). Add after line 161:

```jsx
  // The previous sidebar returns when the operator sets the legacy switch.
  const legacy = features.addon_navigation === false
  const hasBuiltin = builtin.length > 0
  const navAuth = { isAdmin, hasFeature, hasBuiltin }
  const primary = visiblePrimary(primaryDestinations, navAuth)
```

- In the JSX, replace the block at lines 211-216 (`Top-level items`) with:

```jsx
          <div className="sidebar-section">
            {(legacy ? visibleTopItems : [...visibleTopItems.filter(i => i.path === '/app'), ...primary]).map(item => (
              <NavItem key={item.path} item={item} onClose={onClose} collapsed={collapsed} />
            ))}
          </div>

          {!legacy && <PinnedNav onClose={onClose} collapsed={collapsed} />}
```

- Replace `{sections.map(section => {` (line 219) with `{(legacy ? sections : sections.filter(s => s.hubs)).map(section => {` so the Create group (Chat, Studio, Talk) goes away in the new layout and the Workspace hubs stay. The effect at lines 120-132 only reads `section.items`; it needs no change.

Add the `sections.pinned` key to the `nav` locale and the `drawer.*` keys to the `addons` locale. Run this node one-liner once for each file to insert keys and keep the existing sorted order (replace the file name and the object):

```bash
cd core/http/react-ui/public/locales/en && node -e '
const fs=require("fs");
const sort=o=>Object.fromEntries(Object.keys(o).sort().map(k=>[k,o[k]&&typeof o[k]==="object"&&!Array.isArray(o[k])?sort(o[k]):o[k]]));
const nav=JSON.parse(fs.readFileSync("nav.json"));nav.sections.pinned="Pinned";
fs.writeFileSync("nav.json",JSON.stringify(nav,null,2)+"\n");
const ad=JSON.parse(fs.readFileSync("addons.json"));
ad.drawer={add:"Add a new add-on",browse:"Browse add-ons",builtinDetail:"This add-on ships with LocalAI.",close:"Close",limitBody:"The sidebar shows at most eight pinned add-ons. Unpin one first.",limitTitle:"Eight add-ons are pinned",moveEarlier:"Move {{name}} earlier",moveLater:"Move {{name}} later",pin:"Pin {{name}}",running:"Running",search:"Search add-ons",status:{failed:"failed",starting:"starting"},stopped:"Stopped",unpin:"Unpin {{name}}"};
fs.writeFileSync("addons.json",JSON.stringify(sort(ad),null,2)+"\n");'
git diff --stat -- .
```

Expected: the diff touches only the added keys. If the files were not key-sorted before, `git diff` shows reordering: restore with `git checkout -- addons.json nav.json` and insert the keys by hand at the right alphabetical place instead. Other languages fall back to English for missing keys; run `npm run i18n:extract` only if the repository's i18n gate requires it.

- [ ] **Step 9: Styles**

Append to `core/http/react-ui/src/components/addons/addons.css`:

```css
.ad-drawer__bar { display: flex; flex-wrap: wrap; gap: var(--dk-space-2, 8px); align-items: center; margin-bottom: var(--dk-space-4, 16px); }
.ad-drawer__search { flex: 1 1 14rem; min-width: 0; }
.ad-tiles { list-style: none; margin: 0; padding: 0; display: grid; grid-template-columns: repeat(auto-fill, minmax(14rem, 1fr)); gap: var(--dk-space-3, 12px); }
.ad-tile { position: relative; display: flex; align-items: stretch; border: 1px solid var(--dk-edge); border-radius: var(--dk-radius-1); background: var(--dk-surface, transparent); }
.ad-tile__open { flex: 1; min-width: 0; display: flex; gap: var(--dk-space-3, 12px); align-items: center; padding: var(--dk-space-3, 12px); color: inherit; text-decoration: none; background: none; border: 0; text-align: start; cursor: pointer; }
.ad-tile__open:hover { background: var(--dk-hover); }
.ad-tile__text { display: flex; flex-direction: column; min-width: 0; }
.ad-tile__title { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ad-tile__state { font-size: 0.8125rem; color: var(--dk-text-muted, var(--dk-text)); }
.ad-tile[data-state='stopped'] .ad-icon { opacity: 0.5; }
.ad-tile__order { flex: none; display: flex; flex-direction: column; justify-content: center; }
.ad-tile__move { background: none; border: 0; padding: 0.125rem 0.5rem; color: var(--dk-text-muted, var(--dk-text)); cursor: pointer; }
.ad-tile__move:focus-visible { outline: 2px solid var(--dk-accent); outline-offset: -2px; }
.ad-tile__pin { flex: none; width: 2.75rem; background: none; border: 0; border-inline-start: 1px solid var(--dk-edge); color: var(--dk-text-muted, var(--dk-text)); cursor: pointer; }
.ad-tile__pin[aria-pressed='true'] { color: var(--dk-accent); }
.ad-tile__open:focus-visible, .ad-tile__pin:focus-visible { outline: 2px solid var(--dk-accent); outline-offset: -2px; }
.nav-item--muted { opacity: 0.55; }
.nav-item--sub { padding-inline-start: 2.25rem; }
@media (max-width: 480px) { .ad-tiles { grid-template-columns: minmax(0, 1fr); } }
```

If `scripts/inline-style-gate.mjs` or the lint complains, fix the finding; do not widen the baseline.

- [ ] **Step 10: Run the checks**

Run:
```bash
cd core/http/react-ui && npm run lint && npm run lint:inline-styles && node tests/unit/run.mjs navigation.test.mjs && npx playwright test e2e/addon-navigation.spec.js e2e/addons-shell.spec.js
```
Expected: lint clean, unit tests pass, all e2e tests pass. The existing add-on specs (`addons-shell.spec.js` and the other `addons-*.spec.js`, which assumed the Apps tab inside the Build hub) must still pass; if one fails on the removed hub tab, update the assertion to the new drawer, not the product code.

- [ ] **Step 11: Document and commit**

In `docs/content/features/addons.md` replace the paragraph that describes the Add-ons tab of the Build hub with the drawer: tiles, search, Browse and Add (administrators), pins (per user, cap of eight in the sidebar), built-in tiles, and the `LOCALAI_LEGACY_NAVIGATION` switch. In the sidebar documentation page (find it with `grep -rln "sidebar" docs/content`) describe the new layout.

```bash
git add core/http/react-ui core/config/application_config.go core/cli/run.go core/http/routes/localai.go docs/content
git commit -m "feat(ui): add the Add-ons drawer and the Pinned sidebar

The sidebar shows Chat, Models, Add-ons and Settings with a per-user
Pinned section. The Add-ons page becomes a grid of tiles with search,
pins and a detail view. LOCALAI_LEGACY_NAVIGATION brings the old
sidebar back without a rebuild.

Assisted-by: Claude:claude-sonnet-5-5 node playwright"
```

---

### Task 7: Redirects for every moved route

**Files:**
- Create: `core/http/react-ui/src/utils/legacyRoutes.js`
- Modify: `core/http/react-ui/src/router.jsx:198-211` (the seven standalone generator routes), `:212-215` (add the model route of Studio)
- Test: `core/http/react-ui/tests/unit/legacyroutes.test.mjs`
- Test: `core/http/react-ui/e2e/addon-navigation.spec.js` (add a redirect case)
- Modify: `docs/content/features/addons.md` (list the redirects)

**Interfaces:**
- Consumes: the Studio tab keys in `core/http/react-ui/src/pages/Studio.jsx:25-33` (`images`, `video`, `threed`, `tts`, `sound`, `transform`, `diarization`).
- Produces (`legacyRoutes.js`):
  - `STUDIO_TABS = { image: 'images', video: 'video', '3d': 'threed', tts: 'tts', sound: 'sound', diarization: 'diarization', transform: 'transform' }`.
  - `studioRedirect(legacy, model, search, hash) -> string`: `/app/studio/<tab>[/<encoded model>]<search><hash>`.
  - `MIGRATION_ROUTES`: the old-route table of the spec as data, `[{ from, to, kind }]`, used by the coverage test.
- Router: `StudioTabRedirect({ legacy })`, new route `studio/:tab/:model`.

The face, voice and fine-tuning redirects are added by Tasks 17, 19 and the fine-tuning group, where their add-ons exist.

- [ ] **Step 1: Write the failing tests**

`core/http/react-ui/tests/unit/legacyroutes.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { STUDIO_TABS, studioRedirect, MIGRATION_ROUTES } from '../../src/utils/legacyRoutes.js'

const here = dirname(fileURLToPath(import.meta.url))
const router = readFileSync(join(here, '../../src/router.jsx'), 'utf8')

test('studioRedirect keeps the model, the query and the hash', () => {
  assert.equal(studioRedirect('image', 'stable/diffusion', '?x=1', '#top'), '/app/studio/images/stable%2Fdiffusion?x=1#top')
  assert.equal(studioRedirect('3d', undefined, '', ''), '/app/studio/threed')
  assert.equal(studioRedirect('tts', 'piper', '?voice=a', ''), '/app/studio/tts/piper?voice=a')
})

test('every standalone generator route maps to a Studio tab', () => {
  assert.deepEqual(Object.keys(STUDIO_TABS).sort(), ['3d', 'diarization', 'image', 'sound', 'transform', 'tts', 'video'])
})

test('the old-route table has a redirect entry in the router for every moved route', () => {
  const moved = MIGRATION_ROUTES.filter(r => r.kind === 'studio-tab')
  assert.equal(moved.length, 7)
  for (const r of moved) {
    const key = r.from.replace('/app/', '')
    assert.ok(router.includes(`path: '${key}', element:`) && router.includes(`legacy="${key}"`), `router.jsx has no redirect entry for ${r.from}`)
    assert.ok(router.includes(`path: '${key}/:model'`), `router.jsx has no model redirect entry for ${r.from}`)
  }
})

test('Studio has a route that carries a model', () => {
  assert.ok(router.includes("path: 'studio/:tab/:model'"))
})

test('the same-path routes need no redirect', () => {
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'same')) assert.equal(r.from, r.to)
})
```

- [ ] **Step 2: Run to see it fail**

Run: `cd core/http/react-ui && node tests/unit/run.mjs legacyroutes.test.mjs`
Expected: `failed` with `ERR_MODULE_NOT_FOUND` for `legacyRoutes.js`.

- [ ] **Step 3: Write the table and the function**

`core/http/react-ui/src/utils/legacyRoutes.js`:

```js
// The old routes of the migration table and where they go now. Redirects stay
// in the app until a separate decision removes them.

// Old generator route segment -> Studio tab key (pages/Studio.jsx MODALITIES).
export const STUDIO_TABS = Object.freeze({
  image: 'images',
  video: 'video',
  '3d': 'threed',
  tts: 'tts',
  sound: 'sound',
  diarization: 'diarization',
  transform: 'transform',
})

// The target of an old generator route. The model segment, the query string
// and the hash are kept as they came.
export function studioRedirect(legacy, model, search = '', hash = '') {
  const tab = STUDIO_TABS[legacy]
  const tail = model ? `/${encodeURIComponent(model)}` : ''
  return `/app/studio/${tab}${tail}${search}${hash}`
}

// One row per old route of the spec's migration table that changes or keeps
// its path. `kind` is 'same' (no redirect), 'studio-tab' or 'addon-page'.
export const MIGRATION_ROUTES = Object.freeze([
  { from: '/app/chat', to: '/app/chat', kind: 'same' },
  { from: '/app/models', to: '/app/models', kind: 'same' },
  { from: '/app/settings', to: '/app/settings', kind: 'same' },
  { from: '/app/studio', to: '/app/studio', kind: 'same' },
  { from: '/app/talk', to: '/app/talk', kind: 'same' },
  { from: '/app/image', to: '/app/studio/images', kind: 'studio-tab' },
  { from: '/app/video', to: '/app/studio/video', kind: 'studio-tab' },
  { from: '/app/3d', to: '/app/studio/threed', kind: 'studio-tab' },
  { from: '/app/tts', to: '/app/studio/tts', kind: 'studio-tab' },
  { from: '/app/sound', to: '/app/studio/sound', kind: 'studio-tab' },
  { from: '/app/diarization', to: '/app/studio/diarization', kind: 'studio-tab' },
  { from: '/app/transform', to: '/app/studio/transform', kind: 'studio-tab' },
])
```

- [ ] **Step 4: Change the router**

In `core/http/react-ui/src/router.jsx`, add the import `import { studioRedirect } from './utils/legacyRoutes'` and the component next to `BrowseRedirect` (line 180):

```jsx
// An old generator link opens the same generator inside Studio. The model,
// the query string and the hash are kept.
function StudioTabRedirect({ legacy }) {
  const { model } = useParams()
  const { search, hash } = useLocation()
  return <Navigate to={studioRedirect(legacy, model, search, hash)} replace />
}
```

Replace the routes at lines 198-211 (`image` through `transform/:model`) with:

```jsx
  { path: 'image', element: <StudioTabRedirect legacy="image" /> },
  { path: 'image/:model', element: <StudioTabRedirect legacy="image" /> },
  { path: 'video', element: <StudioTabRedirect legacy="video" /> },
  { path: 'video/:model', element: <StudioTabRedirect legacy="video" /> },
  { path: '3d', element: <Feature feature="3d"><StudioTabRedirect legacy="3d" /></Feature> },
  { path: '3d/:model', element: <Feature feature="3d"><StudioTabRedirect legacy="3d" /></Feature> },
  { path: 'tts', element: <StudioTabRedirect legacy="tts" /> },
  { path: 'tts/:model', element: <StudioTabRedirect legacy="tts" /> },
  { path: 'sound', element: <StudioTabRedirect legacy="sound" /> },
  { path: 'sound/:model', element: <StudioTabRedirect legacy="sound" /> },
  { path: 'diarization', element: <Feature feature="audio_diarization"><StudioTabRedirect legacy="diarization" /></Feature> },
  { path: 'diarization/:model', element: <Feature feature="audio_diarization"><StudioTabRedirect legacy="diarization" /></Feature> },
  { path: 'transform', element: <Feature feature="audio_transform"><StudioTabRedirect legacy="transform" /></Feature> },
  { path: 'transform/:model', element: <Feature feature="audio_transform"><StudioTabRedirect legacy="transform" /></Feature> },
```

The `Feature` wrappers stay, so a user without `3d`, `audio_diarization` or `audio_transform` still goes to `/app` as before and the redirect reveals nothing. Replace the `Studio` routes (lines 212-215) with:

```jsx
  { path: 'studio', element: <Studio /> },
  // Tabs are path segments, not a query parameter: switching generator is
  // navigation, and ?tab= reads like a filter. Legacy ?tab= links redirect.
  { path: 'studio/:tab', element: <Studio /> },
  // A generator opened on one model: /app/studio/images/<model>.
  { path: 'studio/:tab/:model', element: <Studio /> },
```

The generator pages read the model with `useParams().model` (for example `core/http/react-ui/src/pages/ImageGen.jsx:25`), so they pick it up inside Studio. The `?tab=` redirect of `Studio.jsx:78-81` is untouched.

The seven standalone page imports (`ImageGen`, `VideoGen`, `ThreeDGen`, `TTS`, `Sound`, `Diarization`, `AudioTransform`) are still used by `Studio.jsx`, not by the router: delete the seven `const X = page(...)` lines from `router.jsx` that nothing references any more (`npm run lint` lists them as unused), but keep `page('image', ...)`-style preload keys that the sidebar or Studio call through `preloadRoute` (`grep -rn "preloadRoute(" core/http/react-ui/src`).

- [ ] **Step 5: Run the unit test**

Run: `cd core/http/react-ui && node tests/unit/run.mjs legacyroutes.test.mjs && npm run lint`
Expected: all `passed`, lint clean.

- [ ] **Step 6: Add the e2e redirect case and run it**

Append to the `describe` block of `core/http/react-ui/e2e/addon-navigation.spec.js` (it reuses `setup`):

```js
  test('an old generator link keeps its model, query and hash', async ({ page }) => {
    await setup(page)
    await page.goto('/app/image/stable%2Fdiffusion?x=1#top')
    await expect(page).toHaveURL(/\/app\/studio\/images\/stable%2Fdiffusion\?x=1#top$/)
  })

  test('the legacy ?tab= link of Studio still works', async ({ page }) => {
    await setup(page)
    await page.goto('/app/studio?tab=tts')
    await expect(page).toHaveURL(/\/app\/studio\/tts$/)
  })

  test('a viewer without the 3d feature still ends on the app home', async ({ page }) => {
    await setup(page)
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, '3d': false }) }))
    await page.goto('/app/3d')
    await expect(page).toHaveURL(/\/app$/)
  })
```

Run: `cd core/http/react-ui && npx playwright test e2e/addon-navigation.spec.js -g "old generator|legacy ?tab|3d feature"`
Expected: PASS. If `sessionFor` takes permissions in another shape, mirror how `e2e/addons-fixtures.js` builds `permissions` and adapt the `'3d': false` entry.

- [ ] **Step 7: Document and commit**

List the redirects in `docs/content/features/addons.md` ("Old links").

```bash
git add core/http/react-ui docs/content/features/addons.md
git commit -m "feat(ui): redirect the old generator routes into Studio

/app/image, video, 3d, tts, sound, diarization and transform open the
same generator inside Studio and keep the model, the query string and
the hash. Studio gets a route that carries a model. A test fails when a
route of the old table has no redirect entry.

Assisted-by: Claude:claude-sonnet-5-5 node playwright"
```

---

### Task 8: Navigation acceptance and quality gates

**Files:**
- Modify: `core/http/react-ui/e2e/addon-navigation.spec.js` (accessibility, permission and rollback cases)
- Modify: `core/http/react-ui/coverage-baseline.txt` only if the ratchet requires a raise (never lower it)
- Test: the full UI suites

**Interfaces:**
- Consumes: everything from Tasks 1-7.
- Produces: the group A definition-of-done checks.

- [ ] **Step 1: Add the remaining permission and access cases**

Append to the `describe` block (they use `setup` from Task 6):

```js
  test('an administrator sees a stopped add-on as a tile with a Start action in the detail view', async ({ page }) => {
    await setup(page, { apps: [app({ id: 'notes', title: 'Notes', enabled: false, state: 'stopped', admin: { canStart: true, canStop: false, managementUrl: '/app/addons?manage=notes' } })] })
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor({ ...MEMBER, role: 'admin' }, { addons: true }) }))
    await page.goto('/app/addons?detail=notes')
    await expect(page.getByTestId('addon-detail').getByRole('button', { name: 'Start' })).toBeVisible()
  })

  test('the pin control has a visible focus state and a name', async ({ page }) => {
    await setup(page)
    await page.goto('/app/addons')
    const toggle = page.getByTestId('pin-toggle').first()
    await toggle.focus()
    const outline = await toggle.evaluate(el => getComputedStyle(el).outlineStyle)
    expect(outline).not.toBe('none')
    await expect(toggle).toHaveAccessibleName(/(Pin|Unpin) /)
  })

  test('a pin of an add-on that is no longer listed is not drawn and shows no error', async ({ page }) => {
    await setup(page, { pins: ['studio', 'removed-addon'] })
    await page.goto('/app')
    const pinned = page.getByTestId('pinned-nav')
    await expect(pinned.getByRole('link')).toHaveCount(1)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  test('a stopped pinned add-on is muted and opens the detail view', async ({ page }) => {
    await setup(page, { pins: ['notes'], apps: [app({ id: 'notes', title: 'Notes', enabled: false, state: 'stopped' })] })
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor({ ...MEMBER, role: 'admin' }, { addons: true }) }))
    await page.goto('/app')
    const link = page.getByTestId('pinned-nav').getByRole('link', { name: /Notes/ })
    await expect(link).toHaveClass(/nav-item--muted/)
    await link.click()
    await expect(page).toHaveURL(/\/app\/addons\?detail=notes$/)
  })
```

- [ ] **Step 2: Run the whole UI suite and the gates**

Run:
```bash
cd core/http/react-ui && npm run lint && npm run lint:inline-styles && npm run test:unit && npx playwright test
```
Expected: all pass. Then run the repository's coverage ratchet as `.agents/building-and-testing.md` describes (`grep -n -i "coverage" .agents/building-and-testing.md`). If a gate is red, add tests; never lower `coverage-baseline.txt` or widen a tolerance.

- [ ] **Step 3: Run the Go suites that this group touched**

Run: `go build ./... && go test ./core/services/addons/... ./core/http/... ./pkg/addons/... -count=1 && go test -tags auth ./core/services/addons/pins/ ./core/http/auth/ -count=1`
Expected: PASS. API route tests pass without edits (except the reserved-ID and `/api/features` additions made in Tasks 1 and 6).

- [ ] **Step 4: Commit**

```bash
git add core/http/react-ui
git commit -m "test(ui): cover the permission, focus and pin states of the drawer

Assisted-by: Claude:claude-sonnet-5-5 playwright"
```

---

## Task group B: SDK version 2

### Task 9: Host SDK version 2 and a clear install-time refusal

**Files:**
- Modify: `pkg/addons/contract/manifest.go:327-330` (separate SDK message), add `HostSDKVersion` near line 17
- Modify: `core/application/application.go:836`, `core/services/addons/runtime/supervisor.go:251`, `pkg/addons/packaging/build.go:34` (use the constant)
- Modify: `addons/tables/manifest.json:584-587` (`max` 2)
- Modify: `tests/addons/fixture/ui/*/manifest.json` (the five fixture manifests: `uifixture`, `uifixture-2`, `slots`, `cssb`, `bare`; `max` 2)
- Modify: `core/http/react-ui/src/addons/sdk.js:13` (`SDK_VERSION = 2`)
- Test: `pkg/addons/contract/manifest_test.go`
- Modify: `docs/content/features/addons.md` (SDK versions)

**Interfaces:**
- Consumes: `contract.HostCompatibility`, `VersionRange.accepts` (`manifest.go:43`), `ParseManifest`, `ValidateManifest`, the fixtures `tests/addons/fixtures/contracts/page.json`.
- Produces: `contract.HostSDKVersion uint64 = 2`; the error text `the add-on needs SDK <min> to <max> and this host provides SDK <host>`; JS `SDK_VERSION = 2`.

Compatibility decision: SDK 2 is a superset of SDK 1. A manifest with `sdk: {min: 1, max: 1}` is refused by a version 2 host (the existing range rule), so every manifest in this repository moves to `max: 2`. Check `git log` and the release notes for a shipped add-on that declares `max: 1` before you merge; none ships from this repository today (`grep -rn '"sdk"' --include=manifest.json .`).

- [ ] **Step 1: Write the failing tests**

In `pkg/addons/contract/manifest_test.go`, inside the `Manifest` Describe:

```go
	It("refuses an add-on that needs a newer SDK than the host provides, and says so", func() {
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(e).NotTo(HaveOccurred())
		m, e := ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.UI.SDK = VersionRange{Min: 2, Max: 2}
		host := compatible() // SDK 1
		e = ValidateManifest(m, host)
		Expect(e).To(MatchError("the add-on needs SDK 2 to 2 and this host provides SDK 1"))
	})

	It("accepts a range that contains the host SDK", func() {
		b, _ := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		m, _ := ParseManifest(b)
		m.Capabilities.UI.SDK = VersionRange{Min: 1, Max: 2}
		host := compatible()
		host.SDK = HostSDKVersion
		Expect(ValidateManifest(m, host)).To(Succeed())
	})

	It("refuses an add-on built for SDK 1 only on a version 2 host", func() {
		b, _ := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		m, _ := ParseManifest(b)
		m.Capabilities.UI.SDK = VersionRange{Min: 1, Max: 1}
		host := compatible()
		host.SDK = HostSDKVersion
		Expect(ValidateManifest(m, host)).To(MatchError("the add-on needs SDK 1 to 1 and this host provides SDK 2"))
	})

	It("keeps the generic message for a range that is malformed", func() {
		b, _ := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		m, _ := ParseManifest(b)
		m.Capabilities.UI.SDK = VersionRange{Min: 3, Max: 2}
		Expect(ValidateManifest(m, compatible())).To(MatchError("invalid or incompatible UI"))
	})
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./pkg/addons/contract/ -count=1`
Expected: FAIL with `undefined: HostSDKVersion` (compile error).

- [ ] **Step 3: Implement**

In `pkg/addons/contract/manifest.go`, after `const SchemaVersion = 1` (line 17):

```go
// HostSDKVersion is the add-on UI SDK version this host provides. Version 2
// adds the shared UI primitives, the hooks, the viewer and sdk.core.fetch. An
// add-on declares the range it accepts in ui.sdk; a host outside that range
// refuses it at install.
const HostSDKVersion uint64 = 2
```

Replace lines 327-330:

```go
	if u := c.UI; u != nil {
		if u.SDK.Min > 0 && u.SDK.Max >= u.SDK.Min && !u.SDK.accepts(h.SDK) {
			return fmt.Errorf("the add-on needs SDK %d to %d and this host provides SDK %d", u.SDK.Min, u.SDK.Max, h.SDK)
		}
		if !relativePath(u.Entry) || !u.SDK.accepts(h.SDK) || !uniqueIDs(u.Pages) || !uniqueIDs(u.Navigation) {
			return fmt.Errorf("invalid or incompatible UI")
		}
```

Note that `ParseManifest` (line 293-296) validates with `h.SDK = m.Capabilities.UI.SDK.Min`, so parsing never trips the new message; only a real host does.

In `core/application/application.go:836`, `core/services/addons/runtime/supervisor.go:251` and `pkg/addons/packaging/build.go:34` replace `SDK: 1` with `SDK: contract.HostSDKVersion`. Leave the test hosts under `tests/` as they are: they model other hosts.

Set `"max": 2` in the `sdk` range of `addons/tables/manifest.json` and of each manifest under `tests/addons/fixture/ui/` (`uifixture`, `uifixture-2`, `slots`, `cssb`, `bare`). Where a Go or JS test hard-codes the range of those fixtures, update it (`grep -rn "sdk: { min: 1, max: 1 }\|SDK: contract.VersionRange{Min: 1, Max: 1}\|\"max\":1" tests core/http/react-ui/tests core/http/react-ui/e2e addons`). Keep a few `max: 1` fixtures on purpose where a test asserts the refusal of an SDK-1-only add-on.

Set `export const SDK_VERSION = 2` in `core/http/react-ui/src/addons/sdk.js:13`.

- [ ] **Step 4: Run the tests**

Run: `go build ./... && go test ./pkg/addons/... ./core/services/addons/... ./core/application/... -count=1 && cd core/http/react-ui && npm run test:unit`
Expected: PASS. In `tests/unit/slots.test.mjs`, `addons.test.mjs` and `esm.test.mjs` the ranges `{ min: 1, max: 1 }` now fail `sdkAccepts` against version 2: change each fixture range that is not meant to be incompatible to `{ min: 1, max: 2 }`, and add one assertion `assert.equal(sdkAccepts({ min: 1, max: 1 }), false)` in `addons.test.mjs`.

- [ ] **Step 5: Document and commit**

In `docs/content/features/addons.md` add "SDK versions": host SDK 2, the `ui.sdk` range, the refusal message, and that a manifest declaring only SDK 1 must raise `max` to 2 after checking it against the new surface.

```bash
git add pkg core addons tests docs/content/features/addons.md
git commit -m "feat(addons): raise the host SDK to version 2

The host provides SDK 2, an add-on that needs another range is refused
at install with a message that names both ranges, and the manifests in
this repository accept SDK 1 to 2.

Assisted-by: Claude:claude-sonnet-5-5 go node"
```

---

### Task 10: SDK primitives, widgets, hooks and the viewer

**Files:**
- Modify: `core/http/react-ui/src/addons/hostComponents.jsx` (re-export the shared components)
- Modify: `core/http/react-ui/src/addons/hostServices.js:50` (components, hooks)
- Modify: `core/http/react-ui/src/addons/registry.jsx:64-80` (the `viewer`)
- Modify: `core/http/react-ui/src/addons/sdk.js:17-22,160-163,254-262` (icons, `hooks`, `viewer`)
- Test: `core/http/react-ui/tests/unit/sdkv2.test.mjs`
- Test: `core/http/react-ui/e2e/addon-navigation.spec.js` (core render is unchanged)

**Interfaces:**
- Consumes: the component files named in the spec's classification table.
- Produces: in `sdk.ui` (wrapped once per SDK instance, props passed through unchanged): `PageHeader`, `ConfirmDialog`, `ErrorWithTraceLink`, `ModelSelector`, `MediaInput`, `WaveformPlayer`, `DistributionBars`, `EmbeddingInspector`, `TabSwitch`; in `sdk.hooks`: `useMediaCapture`, `useObjectUrl`, `useDelayedAction`, `audioBufferToWavBlob` (a pure encoder the reference-audio helper needs; `core/http/react-ui/src/hooks/useMediaCapture.js:8`); `sdk.viewer = { isAdmin(): boolean, hasFeature(name: string): boolean }`; 26 more names in `ICON_NAMES`.

Single copy rule: do not move or edit the component files. The SDK re-exports the file core already imports, so core pages render what they rendered before. The test below pins this by the diff, and the e2e case pins a core page.

- [ ] **Step 1: Write the failing unit test**

`core/http/react-ui/tests/unit/sdkv2.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import * as React from 'react'
import { renderToString } from 'react-dom/server'
import { createHostSDK, ICON_NAMES, SDK_VERSION } from '../../src/addons/sdk.js'

const C = (tag) => (p) => React.createElement(tag, { 'data-props': JSON.stringify(Object.keys(p).sort()) }, p.children)
const NAMES = ['PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'ModelSelector', 'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch']

function make(host = {}) {
  const controller = new AbortController()
  const components = Object.fromEntries(NAMES.map(n => [n, C('div')]))
  const sdk = createHostSDK({
    addonId: 'voice', generation: 1, signal: controller.signal, pages: ['recognition'],
    host: {
      React, components, Icon: () => null, hooks: { useMediaCapture: () => 'cap', useObjectUrl: () => 'url', useDelayedAction: () => 'act', audioBufferToWavBlob: () => 'wav' },
      viewer: { isAdmin: () => true, hasFeature: (f) => f === 'face_recognition' },
      language: () => 'en', theme: { mode: () => 'light', subscribe: () => () => {} }, subscribeLanguage: () => () => {}, api: {},
      ...host,
    },
  })
  return { sdk, controller }
}

test('SDK version is 2', () => assert.equal(SDK_VERSION, 2))

test('sdk.ui exposes every shared primitive and widget, and the wrapper passes props through unchanged', () => {
  const { sdk } = make()
  for (const n of NAMES) assert.equal(typeof sdk.ui[n], 'function', n)
  const html = renderToString(React.createElement(sdk.ui.PageHeader, { title: 'T', supporting: 'S', actions: null }))
  assert.match(html, /\["actions","supporting","title"\]/)
})

test('sdk.hooks exposes the shared hooks as the host functions', () => {
  const { sdk } = make()
  assert.equal(sdk.hooks.useMediaCapture('audio'), 'cap')
  assert.equal(sdk.hooks.useObjectUrl(null), 'url')
  assert.equal(sdk.hooks.useDelayedAction(() => {}), 'act')
  assert.equal(sdk.hooks.audioBufferToWavBlob({}), 'wav')
})

test('sdk.viewer answers for the signed-in user and nothing else', () => {
  const { sdk } = make()
  assert.equal(sdk.viewer.isAdmin(), true)
  assert.equal(sdk.viewer.hasFeature('face_recognition'), true)
  assert.equal(sdk.viewer.hasFeature('addons'), false)
  assert.deepEqual(Object.keys(sdk.viewer).sort(), ['hasFeature', 'isAdmin'])
})

test('a host without viewer or hooks gives a closed viewer and no hooks, never a throw', () => {
  const { sdk } = make({ viewer: undefined, hooks: undefined })
  assert.equal(sdk.viewer.isAdmin(), false)
  assert.equal(sdk.viewer.hasFeature('x'), false)
  assert.deepEqual(Object.keys(sdk.hooks), [])
})

test('the icon allowlist holds the names the voice and face pages draw', () => {
  for (const n of ['arrow-left', 'arrow-right', 'chart-bar', 'circle-dot', 'clipboard', 'code', 'cube', 'filter-off', 'gender', 'globe', 'headphones', 'help-circle', 'mic', 'play', 'plug-off', 'save', 'shield', 'smile', 'waveform', 'camera', 'stop', 'circle', 'image', 'spinner', 'user-shield', 'mask']) {
    assert.ok(ICON_NAMES.includes(n), n)
  }
})

test('the hooks and the viewer stay frozen', () => {
  const { sdk } = make()
  assert.throws(() => { 'use strict'; sdk.viewer.isAdmin = () => false }, TypeError)
  assert.throws(() => { 'use strict'; sdk.hooks.extra = 1 }, TypeError)
})
```

- [ ] **Step 2: Run to see it fail**

Run: `cd core/http/react-ui && node tests/unit/run.mjs sdkv2.test.mjs`
Expected: `failed`: `sdk.hooks` is undefined (`Cannot read properties of undefined`) and the icon names are missing.

- [ ] **Step 3: Implement the SDK side**

In `core/http/react-ui/src/addons/sdk.js`:

1. Extend `ICON_NAMES` (line 17-22): append the 26 names `'arrow-left', 'arrow-right', 'chart-bar', 'circle-dot', 'clipboard', 'code', 'cube', 'filter-off', 'gender', 'globe', 'headphones', 'help-circle', 'mic', 'play', 'plug-off', 'save', 'shield', 'smile', 'waveform', 'camera', 'stop', 'circle', 'image', 'spinner', 'user-shield', 'mask'`.
2. After the `ui` loop (line 162) add:

```js
  // Shared hooks of the host, exposed as the host's own functions. They run in
  // the host's React, so a hook of an add-on page and a hook of a core page are
  // the same code.
  const hooks = {}
  for (const [name, fn] of Object.entries(host.hooks || {})) {
    if (typeof fn === 'function') hooks[name] = fn
  }
  // What the add-on may know about the signed-in user: the role and the
  // feature flags the host already shows in its own UI. The server still
  // decides every call.
  const viewer = Object.freeze({
    isAdmin: () => host.viewer?.isAdmin?.() === true,
    hasFeature: (name) => host.viewer?.hasFeature?.(String(name)) === true,
  })
```

3. In the `sdk` object (line 254) add `hooks: Object.freeze(hooks), viewer,` after `ui: Object.freeze(ui),`.

In `core/http/react-ui/src/addons/hostComponents.jsx` add at the end:

```jsx
// Shared primitives and widgets that more than one add-on uses. They are the
// very components the core pages import: one copy, so a core page and an
// add-on page draw the same thing.
export { default as PageHeader } from '../components/PageHeader'
export { default as ConfirmDialog } from '../components/ConfirmDialog'
export { default as ErrorWithTraceLink } from '../components/ErrorWithTraceLink'
export { default as ModelSelector } from '../components/ModelSelector'
export { default as MediaInput } from '../components/biometrics/MediaInput'
export { default as WaveformPlayer } from '../components/audio/WaveformPlayer'
export { default as DistributionBars } from '../components/biometrics/DistributionBars'
export { default as EmbeddingInspector } from '../components/biometrics/EmbeddingInspector'
export { default as TabSwitch } from '../components/biometrics/TabSwitch'
```

In `core/http/react-ui/src/addons/hostServices.js` change the import (line 6) and the `host` object:

```js
import {
  Button, Spinner, EmptyState, Badge, Stack, Text,
  PageHeader, ConfirmDialog, ErrorWithTraceLink, ModelSelector, MediaInput, WaveformPlayer,
  DistributionBars, EmbeddingInspector, TabSwitch,
} from './hostComponents'
import { useMediaCapture, audioBufferToWavBlob } from '../hooks/useMediaCapture'
import useObjectUrl from '../hooks/useObjectUrl'
import { useDelayedAction } from '../hooks/useDelayedAction'
import { basePath } from '../utils/basePath'
```

```js
export const host = Object.freeze({
  React,
  components: {
    Button, Spinner, EmptyState, Badge, Stack, Text, ArtifactCard,
    PageHeader, ConfirmDialog, ErrorWithTraceLink, ModelSelector, MediaInput, WaveformPlayer,
    DistributionBars, EmbeddingInspector, TabSwitch,
  },
  hooks: { useMediaCapture, useObjectUrl, useDelayedAction, audioBufferToWavBlob },
  basePath,
  Icon,
  theme: { mode: themeMode, subscribe: subscribeTheme },
  language: () => i18n.language || 'en',
  subscribeLanguage,
  api: gatewayApi,
})
```

(`basePath` is used by Task 11.) The viewer depends on the signed-in user, so the registry supplies it. In `core/http/react-ui/src/addons/registry.jsx`, `useAuth()` already gives `hasFeature, user, isAdmin` (line 41). Add after `const navigateRef = useRef(navigate)`:

```jsx
  const viewerRef = useRef({ isAdmin, hasFeature })
  viewerRef.current = { isAdmin, hasFeature }
```

and in the `createHostSDK({ ... host, ...` call (line 80) change `host,` to:

```jsx
        host: { ...host, viewer: { isAdmin: () => viewerRef.current.isAdmin === true, hasFeature: (name) => viewerRef.current.hasFeature(name) === true } },
```

- [ ] **Step 4: Run the unit tests and the diff check**

Run: `cd core/http/react-ui && node tests/unit/run.mjs sdkv2.test.mjs && npm run test:unit && npm run lint`
Expected: PASS.

Single-copy check: `git diff --stat -- core/http/react-ui/src/components/PageHeader.jsx core/http/react-ui/src/components/ConfirmDialog.jsx core/http/react-ui/src/components/ErrorWithTraceLink.jsx core/http/react-ui/src/components/ModelSelector.jsx core/http/react-ui/src/components/biometrics core/http/react-ui/src/components/audio core/http/react-ui/src/hooks`
Expected: empty output. A change to any of those files means a behaviour change and fails this task.

- [ ] **Step 5: Add the core-render e2e case and run it**

Append to the `describe` block of `core/http/react-ui/e2e/addon-navigation.spec.js`:

```js
  test('core pages that share a primitive with the SDK draw the same header', async ({ page }) => {
    await setup(page)
    await page.goto('/app/addons')
    const header = page.locator('header.page-header').first()
    await expect(header.locator('h1.page-title')).toHaveText('Add-ons')
    expect(await header.getAttribute('class')).toBe('page-header page-header--editorial')
  })
```

Run: `cd core/http/react-ui && npx playwright test e2e/addon-navigation.spec.js e2e/addons-shell.spec.js e2e/addons-slots.spec.js`
Expected: PASS.

- [ ] **Step 6: Document and commit**

In `docs/content/features/addons.md` list the SDK 2 surface: `ui` components, `hooks`, `viewer`, and the icon names.

```bash
git add core/http/react-ui docs/content/features/addons.md
git commit -m "feat(addons): export the shared UI from the SDK

SDK 2 re-exports the primitives and widgets that more than one add-on
uses, the shared hooks and a read-only viewer. The components are the
ones core pages already import, so there is one copy.

Assisted-by: Claude:claude-sonnet-5-5 node playwright"
```

---

### Task 11: `sdk.core.fetch` and the 403 paths

**Files:**
- Modify: `core/http/react-ui/src/addons/sdk.js` (add `coreRequestUrl` and `sdk.core`)
- Test: `core/http/react-ui/tests/unit/sdkv2.test.mjs`
- Create: `core/http/auth/core_calls_test.go` (`//go:build auth`)
- Modify: `docs/content/features/addons.md` (core routes from add-ons)

**Interfaces:**
- Consumes: `host.basePath` (Task 10), `host.fetch` (optional, for tests), `anySignal(a, b)` and `alive()` inside `createHostSDK` (`sdk.js:98-108,128-130`), `AddonSdkError`.
- Produces:
  - `coreRequestUrl(path: string, basePath = ''): string | null`: the same-origin URL for a core path, or `null` for an absolute URL, a path with `..`, `//`, a backslash, `/api/addons` or `/api/auth`.
  - `sdk.core.fetch(path: string, init?: { method?, headers?, body?, signal? }): Promise<Response>`. It sets `credentials: 'same-origin'`, `mode: 'same-origin'`, `redirect: 'error'`, `cache: 'no-store'`; it removes `authorization` and `cookie` headers; it refuses after the add-on is withdrawn.

- [ ] **Step 1: Write the failing tests**

Append to `core/http/react-ui/tests/unit/sdkv2.test.mjs`:

```js
import { coreRequestUrl } from '../../src/addons/sdk.js'

test('coreRequestUrl accepts same-origin core paths and keeps the base path and query', () => {
  assert.equal(coreRequestUrl('/v1/face/verify'), '/v1/face/verify')
  assert.equal(coreRequestUrl('/api/voice-profiles?x=1', '/localai'), '/localai/api/voice-profiles?x=1')
  assert.equal(coreRequestUrl('/v1/audio/speech#frag'), '/v1/audio/speech')
})

test('coreRequestUrl refuses what is not a plain core API path', () => {
  for (const bad of [
    'https://evil.example/v1/x', '//evil.example/x', 'v1/x', '', '/v1/../api/addons/apps', '/v1/%2e%2e/x',
    '/api/addons/apps', '/api/addons', '/API/ADDONS/apps', '/api/auth/status', '/api/auth', '/v1\\x', 7, null, undefined,
  ]) {
    assert.equal(coreRequestUrl(bad), null, String(bad))
  }
})

function withFetch() {
  const calls = []
  const { sdk, controller } = make({ basePath: '', fetch: async (url, init) => { calls.push([url, init]); return { ok: true } } })
  return { sdk, controller, calls }
}

test('sdk.core.fetch calls with the browser session and no credential of its own', async () => {
  const { sdk, calls } = withFetch()
  await sdk.core.fetch('/v1/face/verify', { method: 'POST', headers: { Authorization: 'Bearer x', Cookie: 'a=b', 'Content-Type': 'application/json' }, body: '{}' })
  const [url, init] = calls[0]
  assert.equal(url, '/v1/face/verify')
  assert.equal(init.credentials, 'same-origin')
  assert.equal(init.mode, 'same-origin')
  assert.equal(init.redirect, 'error')
  assert.equal(init.headers.has('authorization'), false)
  assert.equal(init.headers.has('cookie'), false)
  assert.equal(init.headers.get('content-type'), 'application/json')
})

test('sdk.core.fetch refuses a bad path without calling the network', async () => {
  const { sdk, calls } = withFetch()
  await assert.rejects(() => sdk.core.fetch('/api/addons/apps'), (e) => e.code === 'invalid_path')
  await assert.rejects(() => sdk.core.fetch('https://evil.example/'), (e) => e.code === 'invalid_path')
  assert.equal(calls.length, 0)
})

test('sdk.core.fetch refuses after the add-on is withdrawn', async () => {
  const { sdk, controller, calls } = withFetch()
  controller.abort('disabled')
  await assert.rejects(() => sdk.core.fetch('/v1/voice/verify'), (e) => e.code === 'withdrawn')
  assert.equal(calls.length, 0)
})

test('sdk.core.fetch aborts an in-flight call when the add-on is withdrawn', async () => {
  let seen
  const { sdk, controller } = make({ basePath: '', fetch: (url, init) => new Promise((_, reject) => { seen = init.signal; init.signal.addEventListener('abort', () => reject(new Error('aborted'))) }) })
  const p = sdk.core.fetch('/v1/voice/verify')
  controller.abort('disabled')
  await assert.rejects(p, /aborted/)
  assert.equal(seen.aborted, true)
})
```

- [ ] **Step 2: Run to see it fail**

Run: `cd core/http/react-ui && node tests/unit/run.mjs sdkv2.test.mjs`
Expected: `failed`: `coreRequestUrl` is not exported (`SyntaxError: ... does not provide an export named 'coreRequestUrl'`).

- [ ] **Step 3: Implement**

In `core/http/react-ui/src/addons/sdk.js`, after `resolveSubpath` (line 87), add:

```js
// Core routes an add-on page may call. The add-on calls them with the signed-in
// user's own session, through the same authentication and authorization as any
// browser request, so it can do nothing the user cannot. Two prefixes are
// refused: the add-on gateway has its own identity-bound typed calls, and the
// auth routes hold account credentials.
const CORE_DENIED = ['/api/addons', '/api/auth']

export function coreRequestUrl(path, basePath = '') {
  if (typeof path !== 'string' || path.length === 0 || path.length > 2048) return null
  if (!path.startsWith('/') || path.startsWith('//') || path.includes('\\')) return null
  let url
  try { url = new URL(path, 'https://host.invalid') } catch { return null }
  if (url.origin !== 'https://host.invalid') return null
  for (const segment of url.pathname.split('/')) {
    let decoded
    try { decoded = decodeURIComponent(segment) } catch { return null }
    if (decoded === '.' || decoded === '..') return null
  }
  const lower = url.pathname.toLowerCase()
  if (CORE_DENIED.some(p => lower === p || lower.startsWith(`${p}/`))) return null
  return `${basePath}${url.pathname}${url.search}`
}
```

In `createHostSDK`, before `const sdk = {` add:

```js
  const core = Object.freeze({
    // fetch for a core route, with the user's own session. No token, cookie
    // value or header of the add-on's choosing reaches the request, and the
    // call ends when the add-on is withdrawn.
    async fetch(path, init = {}) {
      alive()
      const url = coreRequestUrl(path, host.basePath || '')
      if (!url) throw new AddonSdkError('invalid_path', 'An add-on may call only same-origin core API paths')
      const headers = new Headers(init.headers || {})
      headers.delete('authorization')
      headers.delete('cookie')
      return (host.fetch || globalThis.fetch)(url, {
        method: init.method,
        headers,
        body: init.body,
        signal: anySignal(signal, init.signal),
        credentials: 'same-origin',
        mode: 'same-origin',
        redirect: 'error',
        cache: 'no-store',
      })
    },
  })
```

and add `core,` to the `sdk` object after `viewer,`.

- [ ] **Step 4: Run the unit tests**

Run: `cd core/http/react-ui && node tests/unit/run.mjs sdkv2.test.mjs && npm run lint`
Expected: PASS.

- [ ] **Step 5: Write and run the Go test: a user without the right role gets 403 from a core route called by an add-on**

`core/http/auth/core_calls_test.go`:

```go
//go:build auth

// SPDX-License-Identifier: MIT

package auth_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// An add-on page calls core routes with the signed-in user's own session and
// the browser's same-origin metadata. These cases pin that the existing
// middleware, not the add-on, decides.
var _ = Describe("core routes called from an add-on page", func() {
	var (
		db     *gorm.DB
		e      *echo.Echo
		member *auth.User
		admin  *auth.User
	)
	call := func(method, path string, user *auth.User) int {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: createTestSession(db, user.ID)})
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}

	BeforeEach(func() {
		db = testDB()
		e = echo.New()
		e.Use(auth.Middleware(db, config.NewApplicationConfig()))
		e.Use(auth.RequireRouteFeature(db))
		e.Use(auth.CSRFMiddleware())
		e.POST("/v1/face/verify", ok)
		e.POST("/api/voice-profiles", ok, auth.RequireAdmin())
		member = createTestUser(db, "member@example.com", auth.RoleUser, auth.ProviderLocal)
		admin = createTestUser(db, "admin@example.com", auth.RoleAdmin, auth.ProviderLocal)
	})

	It("answers 403 for a user whose face_recognition feature is off", func() {
		Expect(auth.UpdateUserPermissions(db, member.ID, auth.PermissionMap{auth.FeatureFaceRecognition: false})).To(Succeed())
		Expect(call(http.MethodPost, "/v1/face/verify", member)).To(Equal(http.StatusForbidden))
	})

	It("lets the same user through when the feature is on", func() {
		Expect(call(http.MethodPost, "/v1/face/verify", member)).To(Equal(http.StatusOK))
	})

	It("answers 403 from an administrator-only route for a user without the role", func() {
		Expect(call(http.MethodPost, "/api/voice-profiles", member)).To(Equal(http.StatusForbidden))
	})

	It("keeps an administrator route open to an administrator", func() {
		Expect(call(http.MethodPost, "/api/voice-profiles", admin)).To(Equal(http.StatusOK))
	})

	It("answers 401 when the session is missing", func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/face/verify", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})
})
```

Run: `go test -tags auth ./core/http/auth/ -run . -count=1 -ginkgo.focus "core routes called from an add-on page"`
Expected: PASS (5 specs). If the first spec fails because `FeatureFaceRecognition` is matched against `POST /v1/face/verify` only by the real registry, check that `auth.RequireRouteFeature(db)` is in `e.Use` as above.

- [ ] **Step 6: Document and commit**

In `docs/content/features/addons.md` add "Core routes from add-ons": the rule of the spec's "Core routes from add-ons" section, `sdk.core.fetch`, the two refused prefixes and the 403 behaviour.

```bash
git add core/http/react-ui core/http/auth/core_calls_test.go docs/content/features/addons.md
git commit -m "feat(addons): let an add-on call core routes as the signed-in user

sdk.core.fetch wraps fetch with the browser session and never adds a
credential. It refuses absolute URLs, traversal, the add-on gateway
and the auth routes. Tests pin that the existing middleware answers
403 for a user without the feature or the role.

Assisted-by: Claude:claude-sonnet-5-5 go node"
```

---

## Task group C: add-on packaging base

### Task 12: Share the add-on protocol server and add an idle runtime

**Files:**
- Move: `addons/tables/internal/server/protocol.go` to `addons/internal/protocol/protocol.go` (package `protocol`)
- Modify: `addons/tables/internal/server/run.go`, `tables.go` and any file in `addons/tables/internal/server` that uses `ReadStartup`, `ExitWithParent`, `Serve`, `Handler`, `Startup`, `Registration`, `ApplyRequest`, `ReceiptRequest`, `OutputRequest` (compile errors list them)
- Create: `addons/internal/idle/idle.go`, `idle_test.go`

**Interfaces:**
- Consumes: the functions of `protocol.go`: `ReadStartup(parent io.Reader) (Startup, io.Reader, error)`, `ExitWithParent(rest io.Reader)`, `Handler(startup Startup, rt contract.Runtime) http.Handler`, `Serve(ctx context.Context, l net.Listener, startup Startup, rt contract.Runtime) error`; `contract.Runtime` (`pkg/addons/contract/protocol.go:77-83`).
- Produces: package `addons/internal/protocol` with the same names; `idle.ErrNoTools`, `idle.Runtime` (implements `contract.Runtime`), `idle.Run(args []string) int`.

This is a move with no behaviour change. The existing Tables tests are its safety net. The docs say "every addon requires an executable; a UI bundle alone is not an addon", so a UI add-on ships a small executable that serves no tools. That choice adds no contract change.

- [ ] **Step 1: Write the failing test for the idle runtime**

`addons/internal/idle/idle_test.go`:

```go
// SPDX-License-Identifier: MIT
package idle

import (
	"context"
	"errors"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ contract.Runtime = Runtime{}

func TestEveryOperationRefusesBecauseTheAddonDeclaresNoTools(t *testing.T) {
	ctx := context.Background()
	rt := Runtime{}
	if _, err := rt.Read(ctx, contract.Envelope{}); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Read: %v", err)
	}
	if _, err := rt.Preview(ctx, contract.Envelope{}); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := rt.Apply(ctx, contract.Envelope{}, contract.Revision{}); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := rt.Receipt(ctx, "x"); !errors.Is(err, ErrNoTools) {
		t.Fatalf("Receipt: %v", err)
	}
	if _, err := rt.OpenOutput(ctx, contract.Envelope{}, "x"); !errors.Is(err, ErrNoTools) {
		t.Fatalf("OpenOutput: %v", err)
	}
}

func TestRunRefusesWithoutTheProtocolFlag(t *testing.T) {
	if got := Run([]string{"voice"}); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
	if got := Run([]string{"voice", "--other"}); got != 2 {
		t.Fatalf("exit code %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./addons/internal/idle/ -count=1`
Expected: FAIL with `no non-test Go files` (the package does not exist).

- [ ] **Step 3: Move the protocol server**

```bash
mkdir -p addons/internal/protocol
git mv addons/tables/internal/server/protocol.go addons/internal/protocol/protocol.go
sed -i 's/^package server$/package protocol/' addons/internal/protocol/protocol.go
go build ./addons/... 2>&1 | head -30
```

Expected: compile errors in `addons/tables/internal/server` for the moved names. In `run.go` and every other file with an error, add the import `"github.com/mudler/LocalAI/addons/internal/protocol"` and prefix each moved name with `protocol.` (for example `protocol.ReadStartup`, `protocol.ExitWithParent`, `protocol.Serve`, `protocol.Startup`). `ClearExports` stays in `export.go` of the Tables server. Keep going until `go build ./addons/... && go vet ./addons/...` is clean.

- [ ] **Step 4: Write the idle runtime**

`addons/internal/idle/idle.go`:

```go
// SPDX-License-Identifier: MIT

// Package idle is the executable of an add-on that has a UI and no tools. The
// contract requires an executable for every add-on, so such an add-on ships
// this one: it speaks the add-on protocol, declares nothing and refuses every
// operation. All of its work happens in the browser, through the SDK.
package idle

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrNoTools is what every operation answers.
var ErrNoTools = errors.New("this add-on declares no tools")

// Runtime implements contract.Runtime with no tools.
type Runtime struct{}

func (Runtime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, ErrNoTools
}

func (Runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, ErrNoTools
}

func (Runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, ErrNoTools
}

func (Runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, ErrNoTools
}

func (Runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, ErrNoTools
}

// Run is the whole behaviour of a UI add-on executable: the owner starts it
// with "--localai-addon-v1", descriptor 3 is a bound loopback listener and
// descriptor 4 carries the startup object. It returns the process exit code.
func Run(args []string) int {
	if len(args) != 2 || args[1] != "--localai-addon-v1" {
		return 2
	}
	startup, rest, err := protocol.ReadStartup(os.NewFile(4, "startup"))
	if err != nil || !filepath.IsAbs(startup.DataDir) {
		return 3
	}
	protocol.ExitWithParent(rest)
	listener, err := net.FileListener(os.NewFile(3, "listener"))
	if err != nil {
		return 6
	}
	if err := protocol.Serve(context.Background(), listener, startup, Runtime{}); err != nil {
		return 8
	}
	return 0
}
```

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test ./addons/... -count=1`
Expected: PASS, including every existing Tables test unchanged.

- [ ] **Step 6: Commit**

```bash
git add addons
git commit -m "refactor(addons): share the add-on protocol server

The protocol server moves out of the Tables add-on so other add-ons
can use it. A new idle runtime serves an add-on that has a UI and no
tools. Tables behaves as before.

Assisted-by: Claude:claude-sonnet-5-5 go"
```

---

### Task 13: The shared UI bundle builder

**Files:**
- Create: `addons/internal/uibuild/uibuild.mjs`
- Create: `addons/internal/uibuild/test/uibuild.test.mjs`, `addons/internal/uibuild/test/fixture/src/index.jsx`
- Create: `addons/internal/uibuild/package.json`

**Interfaces:**
- Consumes: the rolldown bundler of `core/http/react-ui/node_modules` (the same use as `addons/tables/ui/build.mjs:75-86`, classic JSX with pragma `h`).
- Produces: `buildAddonUI({ root, name, rgb }) -> Promise<string>`: bundles `<root>/src/index.jsx` to `<root>/dist/ui/<name>.js`, copies `<root>/src/style.css` to `dist/ui/<name>.css`, writes a solid-colour `dist/ui/icon.png`, refuses a bundle that imports React, and returns `<root>/dist`.

- [ ] **Step 1: Write the failing test**

`addons/internal/uibuild/test/fixture/src/index.jsx`:

```jsx
export function createAddon(sdk) {
  const h = sdk.React.createElement
  return { Component: () => h('p', null, 'fixture'), dispose() {} }
}
```

`addons/internal/uibuild/test/fixture/src/style.css`:

```css
.fx { color: inherit; }
```

`addons/internal/uibuild/test/uibuild.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { buildAddonUI } from '../uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))

test('buildAddonUI writes the module, the style sheet and a PNG icon', async () => {
  const dist = await buildAddonUI({ root: join(here, 'fixture'), name: 'fixture', rgb: [26, 62, 138] })
  assert.ok(existsSync(join(dist, 'ui/fixture.js')))
  assert.ok(existsSync(join(dist, 'ui/fixture.css')))
  const png = readFileSync(join(dist, 'ui/icon.png'))
  assert.deepEqual([...png.subarray(0, 8)], [137, 80, 78, 71, 13, 10, 26, 10])
  assert.ok(png.length < 256 * 1024, 'the icon must fit the 256 KiB manifest bound')
})

test('the bundle never imports React', async () => {
  const dist = await buildAddonUI({ root: join(here, 'fixture'), name: 'fixture', rgb: [1, 2, 3] })
  const js = readFileSync(join(dist, 'ui/fixture.js'), 'utf8')
  assert.doesNotMatch(js, /from\s+['"](react|react-dom)/)
  assert.match(js, /createAddon/)
})
```

- [ ] **Step 2: Run to see it fail**

Run: `node --test addons/internal/uibuild/test/uibuild.test.mjs`
Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `../uibuild.mjs`.

- [ ] **Step 3: Write the builder**

`addons/internal/uibuild/package.json`:

```json
{
  "name": "localai-addon-uibuild",
  "private": true,
  "version": "0.1.0",
  "description": "Shared bundle builder of the first-party add-on UIs.",
  "type": "module",
  "scripts": { "test": "node --test test/uibuild.test.mjs" }
}
```

`addons/internal/uibuild/uibuild.mjs`:

```js
// Builds the UI package files of a first-party add-on, laid out as the
// package root expects them: dist/ui/<name>.js (one ES module), dist/ui/<name>.css
// and dist/ui/icon.png. It uses the bundler the host UI already has installed
// (core/http/react-ui/node_modules), compiles JSX to calls of `h`, and never
// bundles React: the host's React arrives through the SDK. The same method as
// addons/tables/ui/build.mjs, for add-ons that share this builder.
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { deflateSync } from 'node:zlib'

const here = dirname(fileURLToPath(import.meta.url))
const hostUi = resolve(here, '../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))

// A solid rounded square, 128 by 128, written by hand so a package build needs
// no image tool and no download.
function solidIcon([r, g, b]) {
  const size = 128
  const row = size * 4 + 1
  const px = Buffer.alloc(size * row)
  const radius = 22
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const cx = Math.min(Math.max(x, radius), size - 1 - radius)
      const cy = Math.min(Math.max(y, radius), size - 1 - radius)
      const inside = (x - cx) ** 2 + (y - cy) ** 2 <= radius * radius
      const o = y * row + 1 + x * 4
      px[o] = r; px[o + 1] = g; px[o + 2] = b; px[o + 3] = inside ? 255 : 0
    }
  }
  const table = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0 })
  const crc = (buf) => { let c = 0xffffffff; for (const byte of buf) c = table[(c ^ byte) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0 }
  const chunk = (type, data) => {
    const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
    const body = Buffer.concat([Buffer.from(type), data])
    const sum = Buffer.alloc(4); sum.writeUInt32BE(crc(body))
    return Buffer.concat([len, body, sum])
  }
  const head = Buffer.alloc(13)
  head.writeUInt32BE(size, 0); head.writeUInt32BE(size, 4)
  head[8] = 8; head[9] = 6
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', head), chunk('IDAT', deflateSync(px, { level: 9 })), chunk('IEND', Buffer.alloc(0))])
}

export async function buildAddonUI({ root, name, rgb }) {
  const out = join(root, 'dist')
  const { build: bundle } = await import(require.resolve('rolldown'))
  rmSync(out, { recursive: true, force: true })
  mkdirSync(join(out, 'ui'), { recursive: true })
  await bundle({
    input: join(root, 'src/index.jsx'),
    external: [],
    output: { file: join(out, `ui/${name}.js`), format: 'esm', minify: false, sourcemap: false },
    transform: { jsx: { runtime: 'classic', pragma: 'h', pragmaFrag: 'Fragment' } },
    logLevel: 'warn',
  })
  copyFileSync(join(root, 'src/style.css'), join(out, `ui/${name}.css`))
  writeFileSync(join(out, 'ui/icon.png'), solidIcon(rgb))
  const js = readFileSync(join(out, `ui/${name}.js`), 'utf8')
  if (/from\s+['"](react|react-dom)/.test(js) || /require\(/.test(js)) throw new Error('the bundle must not import React')
  return out
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [root, name, r, g, b] = process.argv.slice(2)
  buildAddonUI({ root: resolve(root), name, rgb: [Number(r), Number(g), Number(b)] })
    .then((dir) => console.log(`built ${dir}`), (e) => { console.error(e); process.exit(1) })
}
```

- [ ] **Step 4: Run to see it pass**

Run: `node --test addons/internal/uibuild/test/uibuild.test.mjs`
Expected: PASS (2 tests). Rolldown resolves from `core/http/react-ui/node_modules`, so run `cd core/http/react-ui && npm ci` first if it is missing. Add `addons/internal/uibuild/test/fixture/dist` to `.gitignore` if the repository does not ignore `dist/` already (`git status` after the run shows it).

- [ ] **Step 5: Commit**

```bash
git add addons/internal/uibuild .gitignore
git commit -m "feat(addons): add a shared builder for add-on UI bundles

Same method as the Tables UI build: rolldown from the host UI, classic
JSX on h, no React in the bundle, and a hand-written solid icon.

Assisted-by: Claude:claude-sonnet-5-5 node"
```

---

## Task group D: the Voice add-on

The Voice add-on is one package, `addons/voice`, with id `voice`. Its pages: `recognition` (main page), `library`, `create` and `speech`. Core keeps `/v1/voice/*`, `/api/voice-profiles`, `/v1/audio/speech`, the voice registry and the voice profile store. The add-on calls them through `sdk.core.fetch`.

Page-body port rule used by Tasks 15, 16 and 18. The core pages are React function components that import the host. The add-on versions are factories in the style of `addons/tables/ui/src/index.jsx`: each file exports `createX(ctx, ...)` and returns components built with `h` and the SDK. Apply this table to every ported file, with no other change of behaviour:

| Core page code | In the add-on |
|---|---|
| `import { useState, useMemo, useEffect, useRef, useCallback } from 'react'` | `const { useState, useMemo, useEffect, useRef, useCallback } = ctx.React` inside the factory |
| `<Link to="/app/...">` (`react-router-dom`) | `<ctx.Link page="library">` (defined in `ctx.jsx`, Task 14) |
| `useParams().model` | `props.path.split('/')[0]` (the page receives `{ page, path }`) |
| `useSearchParams()` | `ctx.useSearch()` (defined in `ctx.jsx`) |
| `useOutletContext().addToast(msg, kind)` | `ctx.toast(msg, kind)` |
| `useTranslation(ns).t(key, vars)` | `ctx.t(key, vars)`; the English bundle is `src/i18n/en.js`, built from the same keys of `biometrics.json` and `media.json` |
| `Icon` | `ctx.Icon` (`sdk.Icon`) |
| `PageHeader`, `ConfirmDialog`, `ErrorWithTraceLink`, `ModelSelector`, `MediaInput`, `WaveformPlayer`, `DistributionBars`, `EmbeddingInspector`, `TabSwitch` | `ctx.ui.<Name>` (`sdk.ui`) |
| `LoadingSpinner`, `EmptyState` | `ctx.ui.Spinner`, `ctx.ui.EmptyState` |
| `SideSheet` | `sdk.layers.openPanel({ title, render })` through `ctx.openPanel(title, render)` |
| `UnsavedChangesGuard` | `sdk.markDirty(flag)` through `ctx.markDirty(flag)` |
| `HomeUndoToast` | `ctx.UndoBar` (Task 14) |
| `useAuth().isAdmin`, `hasFeature(name)` | `ctx.viewer.isAdmin()`, `ctx.viewer.hasFeature(name)` |
| `useMediaCapture`, `useObjectUrl`, `useDelayedAction` | `ctx.hooks.<name>` (`sdk.hooks`) |
| `fetch(apiUrl(path), init)` in `utils/api.js` | `ctx.core.fetch(path, init)`; the add-on's `api.js` copies the URL builders it needs from `core/http/react-ui/src/utils/api.js` (`modelsApi.listCapabilities`, `modelsApi.list`, `voiceProfilesApi`, `ttsApi.generateV1`) |
| `utils/capabilities`, `utils/identity`, `utils/referenceAudio`, `utils/clipboard`, `utils/markdown` | copied into `src/lib/` unchanged when the page needs them (they are pure functions with no host import) |
| `import './identity.css'` | `src/style.css`, the same rules; the loader scopes them under the add-on's root |

A ported page must keep its `data-testid` values so the parity checks compare the same DOM.

### Task 14: Voice add-on package and module skeleton

**Files:**
- Create: `addons/voice/manifest.json`
- Create: `addons/voice/cmd/voice/main.go`
- Create: `addons/voice/package/platforms.yaml`
- Create: `addons/voice/ui/package.json`, `ui/build.mjs`
- Create: `addons/voice/ui/src/index.jsx`, `ctx.jsx`, `style.css`, `i18n/en.js`
- Test: `addons/voice/ui/test/render.test.mjs`, `addons/voice/ui/test/run.mjs`
- Modify: `.github/workflows/addons-package.yml` (add the `voice` package next to `tables`, same shape; read the file first)

**Interfaces:**
- Consumes: `idle.Run(args []string) int` (Task 12), `buildAddonUI` (Task 13), the module contract of `addons/tables/ui/src/index.jsx`: the module exports `createAddon(sdk, options?)` returning `{ Component, components, chatActions, dispose }`, where `Component({ page, path })` draws a page; `sdk.*` from Tasks 10 and 11.
- Produces:
  - Manifest: `id: "voice"`, `ui.sdk = {min: 2, max: 2}`, `ui.pages = ["recognition", "library", "create", "speech"]`, `ui.mainPage = "recognition"`, `ui.navigation = ["speech", "library"]`, title `Voice`.
  - `createContext(sdk) -> ctx` with `ctx = { sdk, React, h, Fragment, t(key, vars), Icon, ui, hooks, viewer, core, Link, useSearch(), toast(msg, kind), openPanel(title, render), markDirty(flag), UndoBar, json(path, init) }`.
  - `createAddon(sdk)` routing `page` to the page factories of Tasks 15-17.

- [ ] **Step 1: Write the failing render test**

`addons/voice/ui/test/run.mjs` is the runner of `addons/tables/ui/test/run.mjs` (copy it; it is a 20-line file that prints one JSON line per test). `addons/voice/ui/test/render.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { build } from '../build.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const hostUi = resolve(here, '../../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

export async function load(extra = {}) {
  const dir = await build()
  const mod = await import(pathToFileURL(join(dir, 'ui/voice.js')).href + `?t=${Date.now()}`)
  const { createHostSDK } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  const stub = (tag) => (p) => React.createElement(tag, { 'data-stub': tag }, p.title || p.label || p.children || null)
  const calls = []
  const controller = new AbortController()
  const sdk = createHostSDK({
    addonId: 'voice', generation: 1, signal: controller.signal, pages: ['recognition', 'library', 'create', 'speech'],
    host: {
      React,
      components: Object.fromEntries(['Button', 'Spinner', 'EmptyState', 'Badge', 'Stack', 'Text', 'PageHeader', 'ConfirmDialog', 'ErrorWithTraceLink', 'ModelSelector', 'MediaInput', 'WaveformPlayer', 'DistributionBars', 'EmbeddingInspector', 'TabSwitch'].map(n => [n, stub(n === 'PageHeader' ? 'header' : 'div')])),
      hooks: { useMediaCapture: () => ({}), useObjectUrl: () => null, useDelayedAction: () => ({ pending: null, schedule() {}, undo() {} }) },
      viewer: { isAdmin: () => true, hasFeature: () => true },
      basePath: '',
      fetch: async (url, init) => { calls.push([url, init]); return { ok: true, json: async () => ({ data: [] }), blob: async () => new Blob([]) } },
      Icon: () => null, theme: { mode: () => 'light', subscribe: () => () => {} }, language: () => 'en', subscribeLanguage: () => () => {}, api: {},
      ...extra,
    },
    bindings: { current: () => ({ page: 'recognition', path: '', search: '' }), openLayer: () => ({ close() {} }), notify() {} },
  })
  return { mod, sdk, controller, calls }
}

test('render: createAddon returns a page component and dispose', async () => {
  const { mod, sdk, controller } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(typeof addon.Component, 'function')
  assert.equal(typeof addon.dispose, 'function')
  addon.dispose()
  controller.abort('done')
})

test('render: an unknown page draws nothing', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(renderToString(React.createElement(addon.Component, { page: 'nope', path: '' })), '')
})

test('render: the manifest declares the four pages, the main page and SDK 2', async () => {
  const { readFileSync } = await import('node:fs')
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  assert.equal(m.id, 'voice')
  assert.deepEqual(m.capabilities.ui.pages, ['recognition', 'library', 'create', 'speech'])
  assert.equal(m.capabilities.ui.mainPage, 'recognition')
  assert.deepEqual(m.capabilities.ui.sdk, { min: 2, max: 2 })
  assert.deepEqual(m.capabilities.tools ?? [], [])
})
```

- [ ] **Step 2: Run to see it fail**

Run: `node addons/voice/ui/test/run.mjs`
Expected: a `failed` line: `Cannot find module '../build.mjs'`.

- [ ] **Step 3: Write the package files**

`addons/voice/manifest.json` (copy the shape of `addons/tables/manifest.json`; the `host`, `api`, `protocol` and `dataFormat` ranges are `{ "min": 1, "max": 1 }` as there):

```json
{
  "schemaVersion": 1,
  "id": "voice",
  "version": "0.1.0",
  "executable": "voice",
  "os": "linux",
  "arch": "amd64",
  "host": { "min": 1, "max": 1 },
  "api": { "min": 1, "max": 1 },
  "protocol": { "min": 1, "max": 1 },
  "dataFormat": { "min": 1, "max": 1 },
  "capabilities": {
    "ui": {
      "entry": "ui/voice.js",
      "css": ["ui/voice.css"],
      "sdk": { "min": 2, "max": 2 },
      "pages": ["recognition", "library", "create", "speech"],
      "navigation": ["speech", "library"],
      "mainPage": "recognition",
      "icon": "ui/icon.png",
      "title": "Voice",
      "description": "Recognise who is speaking, keep the voices that text-to-speech can use, and speak text with them."
    }
  }
}
```

`addons/voice/cmd/voice/main.go`:

```go
// SPDX-License-Identifier: MIT

// Command voice is the executable of the Voice add-on. It serves no tools:
// the add-on works in the browser, through the SDK and the core API.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/idle"
)

func main() {
	os.Exit(idle.Run(os.Args))
}
```

`addons/voice/package/platforms.yaml`: copy `addons/tables/package/platforms.yaml` and change `addon: tables` to `addon: voice`, `executable: tables` to `executable: voice`, `manifest: addons/tables/manifest.json` to `manifest: addons/voice/manifest.json` and `ui: addons/tables/ui/dist` to `ui: addons/voice/ui/dist`. Keep every platform `enabled: false`: the file says a platform ships only in the change that shows its native check passing.

`addons/voice/ui/package.json`:

```json
{
  "name": "localai-voice-ui",
  "private": true,
  "version": "0.1.0",
  "description": "Native UI of the Voice add-on: a standalone ES module that the host loads after its own build.",
  "type": "module",
  "scripts": { "build": "node build.mjs", "test": "node test/run.mjs" }
}
```

`addons/voice/ui/build.mjs`:

```js
import { fileURLToPath } from 'node:url'
import { dirname } from 'node:path'
import { buildAddonUI } from '../../internal/uibuild/uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))

export async function build() {
  return buildAddonUI({ root: here, name: 'voice', rgb: [58, 38, 120] })
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  build().then((dir) => console.log(`built ${dir}`), (e) => { console.error(e); process.exit(1) })
}
```

`addons/voice/ui/src/i18n/en.js`: `export default { 'speech.title': 'Speak text', 'speech.lede': 'Turn text into speech with a voice model of this server.', ... }` with the keys the pages use. Build it by copying the strings the pages read from `core/http/react-ui/public/locales/en/biometrics.json` (`voices.*`, `tool.*`, `voice.*`, `why.*`, `cutoff.*`) and `media.json` (`tts.*`) into one flat object whose keys are the dotted paths (`'voices.title': 'Voices'`). A one-time script does it, run from the repository root:

```bash
node -e '
const fs=require("fs");const flat=(o,p="",out={})=>{for(const[k,v]of Object.entries(o)){const key=p?p+"."+k:k;if(v&&typeof v==="object"&&!Array.isArray(v))flat(v,key,out);else out[key]=v}return out};
const loc="core/http/react-ui/public/locales/en/";
const b=flat(JSON.parse(fs.readFileSync(loc+"biometrics.json")));
const m=flat(Object.fromEntries(Object.entries(JSON.parse(fs.readFileSync(loc+"media.json"))).filter(([k])=>k==="tts")));
const only=(o,prefixes)=>Object.fromEntries(Object.entries(o).filter(([k])=>prefixes.some(p=>k===p||k.startsWith(p+"."))));
const out={...only(b,["voices","voice","tool","why","cutoff","known","speakers","enrol"]),...m};
fs.mkdirSync("addons/voice/ui/src/i18n",{recursive:true});
fs.writeFileSync("addons/voice/ui/src/i18n/en.js","// Generated once from the host locale files; edit by hand from here on.\nexport default "+JSON.stringify(out,null,2)+"\n")'
```

Array values (for example `voice.why`, which the Workbench reads with `returnObjects`) stay arrays: `ctx.t` returns a string, so `ctx.list(key)` (below) reads arrays.

`addons/voice/ui/src/ctx.jsx`:

```jsx
import en from './i18n/en.js'

// The one object every page factory of the Voice add-on receives. It binds the
// SDK once: React, the shared components, the session-bound core fetch, the
// translation and the small adapters that replace the host router and toasts.
export function createContext(sdk) {
  const { React } = sdk
  const h = React.createElement
  const Fragment = React.Fragment
  sdk.locale.define('en', Object.fromEntries(Object.entries(en).filter(([, v]) => typeof v === 'string')))
  const lists = Object.fromEntries(Object.entries(en).filter(([, v]) => Array.isArray(v)))

  const t = (key, vars) => sdk.locale.t(key, key, vars)
  // A list of strings or objects (the "why" and "stored" notes).
  const list = (key) => lists[key] || []

  // A link to a page of this add-on, as an anchor the shell navigates.
  function Link({ page, query, className, children, ...rest }) {
    const href = sdk.navigation.href(query ? `${page}?${query}` : page)
    return h('a', {
      href, className,
      onClick: (e) => { if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return; e.preventDefault(); sdk.navigation.navigate(query ? `${page}?${query}` : page) },
      ...rest,
    }, children)
  }

  const useSearch = () => new URLSearchParams(sdk.navigation.current().search)

  const toast = (message, kind = 'info') => (sdk.notify[kind] || sdk.notify.info)(message)

  const json = async (path, init) => {
    const response = await sdk.core.fetch(path, init)
    if (!response.ok) {
      const body = await response.json().catch(() => ({}))
      throw new Error(body?.error?.message || `HTTP ${response.status}`)
    }
    return response.json()
  }

  // The undo bar of a delayed action: text and an Undo button. Replaces the
  // host's toast with an undo, which the SDK does not expose.
  function UndoBar({ text, undoLabel, onUndo }) {
    return h('div', { className: 'vc-undo', role: 'status' }, h('span', null, text), h(sdk.ui.Button, { variant: 'ghost', size: 'sm', onClick: onUndo }, undoLabel))
  }

  return {
    sdk, React, h, Fragment, t, list, Icon: sdk.Icon, ui: sdk.ui, hooks: sdk.hooks, viewer: sdk.viewer, core: sdk.core,
    Link, useSearch, toast, json, UndoBar,
    openPanel: (title, render) => sdk.layers.openPanel({ title, render }),
    markDirty: (flag) => sdk.markDirty(flag),
  }
}
```

`addons/voice/ui/src/index.jsx` starts with only the router, so the skeleton test passes; Tasks 15-17 add the page imports and cases:

```jsx
import { createContext } from './ctx.jsx'

export function createAddon(sdk, options = {}) {
  const ctx = createContext(sdk, options)
  const pages = {}

  function Component({ page, path }) {
    const Page = pages[page]
    return Page ? <Page path={path || ''} /> : null
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
```

`addons/voice/ui/src/style.css`: start with `.vc-undo { display: flex; gap: var(--dk-space-2, 8px); align-items: center; }`; later tasks append the page rules.

- [ ] **Step 4: Run to see it pass**

Run: `node addons/voice/ui/test/run.mjs && go build ./addons/voice/...`
Expected: 3 `passed` lines and a clean build.

- [ ] **Step 5: Wire the release workflow and commit**

Read `.github/workflows/addons-package.yml` and add `voice` wherever `tables` is listed (build, package and check steps), with the same structure and the same `enabled: false` platforms. Run `go test ./pkg/addons/packaging/... -count=1` to check the platform file parses.

```bash
git add addons/voice .github/workflows/addons-package.yml
git commit -m "feat(addons): add the Voice add-on package and module skeleton

An add-on package with an idle executable and a UI module on SDK 2.
The module routes its four pages; the pages come in the next changes.

Assisted-by: Claude:claude-sonnet-5-5 go node"
```

---

### Task 15: Voice text-to-speech page (new)

**Files:**
- Create: `addons/voice/ui/src/Speech.jsx`
- Modify: `addons/voice/ui/src/index.jsx`, `style.css`, `i18n/en.js`
- Test: `addons/voice/ui/test/speech.test.mjs`

**Interfaces:**
- Consumes: `ctx` (Task 14); core routes `GET /api/models/capabilities` (the capability list `modelsApi.listCapabilities` reads; confirm the URL with `grep -n "listCapabilities" core/http/react-ui/src/utils/api.js`), `GET /api/voice-profiles` (`core/http/routes/localai.go:205`, response `{ data: [{ id, name, voice, ... }] }`), `POST /v1/audio/speech` (`core/http/routes/openai.go:239`, JSON `{ model, input, voice?, instructions? }`, audio body), the capability flag `FLAG_TTS` (`core/http/react-ui/src/utils/capabilities.js:15`) and the model field `voice_cloning`.
- Produces: `createSpeech(ctx) -> { Speech }` where `Speech({ path })` is the `speech` page; the first path segment is an optional model id.

Studio keeps its own audio generation; this page is separate and uses the same core route.

- [ ] **Step 1: Write the failing test**

`addons/voice/ui/test/speech.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { load } from './render.test.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(join(resolve(here, '../../../../core/http/react-ui'), 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

test('speech: the first paint shows the title and the text box', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'speech', path: '' }))
  assert.match(html, /data-testid="voice-speech"/)
  assert.match(html, /<textarea/)
})

test('speech: the request is POST /v1/audio/speech with the model, the text and the chosen voice', async () => {
  const { buildSpeechRequest } = await import('../src/speechRequest.js')
  assert.deepEqual(buildSpeechRequest({ model: 'piper', text: ' hello ', voice: 'amy', instructions: ' calm ' }), {
    model: 'piper', input: 'hello', voice: 'amy', instructions: 'calm',
  })
  assert.deepEqual(buildSpeechRequest({ model: 'piper', text: 'hi', voice: '', instructions: '' }), { model: 'piper', input: 'hi' })
})

test('speech: nothing is sent without text or without a model', async () => {
  const { buildSpeechRequest } = await import('../src/speechRequest.js')
  assert.equal(buildSpeechRequest({ model: '', text: 'x' }), null)
  assert.equal(buildSpeechRequest({ model: 'm', text: '   ' }), null)
})

test('speech: a failed answer surfaces the API message', async () => {
  const { speak } = await import('../src/speechRequest.js')
  const core = { fetch: async () => ({ ok: false, status: 403, json: async () => ({ error: { message: 'feature not enabled for your account' } }), blob: async () => null }) }
  await assert.rejects(() => speak(core, { model: 'm', input: 'x' }), /feature not enabled for your account/)
})
```

- [ ] **Step 2: Run to see it fail**

Run: `node addons/voice/ui/test/run.mjs`
Expected: `failed` lines: `Cannot find module '../src/speechRequest.js'` and `speech: ...` no `data-testid="voice-speech"`.

- [ ] **Step 3: Write the request helper and the page**

`addons/voice/ui/src/speechRequest.js`:

```js
// The one request of the speech page: POST /v1/audio/speech, the OpenAI-shaped
// route of the core API, answered with audio.
export function buildSpeechRequest({ model, text, voice = '', instructions = '' }) {
  const input = String(text || '').trim()
  if (!model || !input) return null
  const request = { model, input }
  if (String(voice).trim()) request.voice = String(voice).trim()
  if (String(instructions).trim()) request.instructions = String(instructions).trim()
  return request
}

export async function speak(core, request) {
  const response = await core.fetch('/v1/audio/speech', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(request),
  })
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new Error(body?.error?.message || `HTTP ${response.status}`)
  }
  return response.blob()
}
```

`addons/voice/ui/src/Speech.jsx`:

```jsx
import { buildSpeechRequest, speak } from './speechRequest.js'

const CAP_TTS = 'FLAG_TTS'

export function createSpeech(ctx) {
  const { h, t, ui, hooks, viewer } = ctx
  const { useState, useEffect, useMemo } = ctx.React

  function Speech({ path }) {
    const [model, setModel] = useState(String(path || '').split('/')[0])
    const [models, setModels] = useState({ status: 'loading', list: [] })
    const [profiles, setProfiles] = useState([])
    const [voiceId, setVoiceId] = useState('')
    const [manualVoice, setManualVoice] = useState('')
    const [text, setText] = useState('')
    const [instructions, setInstructions] = useState('')
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const [blob, setBlob] = useState(null)
    const audioUrl = hooks.useObjectUrl(blob)

    useEffect(() => {
      let live = true
      ctx.json('/api/models/capabilities')
        .then(body => { if (live) setModels({ status: 'ready', list: (body?.data || []).filter(m => !m.disabled && m.capabilities?.includes(CAP_TTS)) }) })
        .catch(() => { if (live) setModels({ status: 'error', list: [] }) })
      return () => { live = false }
    }, [])

    const selected = useMemo(() => models.list.find(m => m.id === model) || null, [models.list, model])
    const cloning = Boolean(selected?.voice_cloning)

    useEffect(() => {
      if (!cloning) { setProfiles([]); setVoiceId(''); return undefined }
      let live = true
      ctx.json('/api/voice-profiles')
        .then(body => { if (live) setProfiles(body?.data || []) })
        .catch(() => { if (live) setProfiles([]) })
      return () => { live = false }
    }, [cloning])

    const profile = profiles.find(p => p.id === voiceId) || null
    const run = async () => {
      const request = buildSpeechRequest({ model, text, voice: cloning ? profile?.voice : manualVoice, instructions })
      if (!request) { ctx.toast(t(model ? 'tts.toasts.noText' : 'tts.toasts.noModel'), 'warning'); return }
      setBusy(true); setError(''); setBlob(null)
      try {
        setBlob(await speak(ctx.core, request))
        ctx.toast(t('tts.toasts.generated'), 'success')
      } catch (e) {
        setError(e.message)
      } finally {
        setBusy(false)
      }
    }

    return (
      <main className="page vc-speech" data-testid="voice-speech">
        <ui.PageHeader title={t('speech.title')} supporting={t('speech.lede')} />
        <label className="vc-field">
          <span>{t('speech.model')}</span>
          <ui.ModelSelector value={model} onChange={setModel} options={models.list.map(m => m.id)} loading={models.status === 'loading'} />
        </label>
        {cloning ? (
          <label className="vc-field">
            <span>{t('speech.voice')}</span>
            <select className="dk-input" value={voiceId} onChange={e => setVoiceId(e.target.value)} aria-label={t('speech.voice')}>
              <option value="">{t('speech.defaultVoice')}</option>
              {profiles.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </label>
        ) : (
          <label className="vc-field">
            <span>{t('speech.voice')}</span>
            <input className="dk-input" value={manualVoice} onChange={e => setManualVoice(e.target.value)} />
          </label>
        )}
        <label className="vc-field">
          <span>{t('speech.text')}</span>
          <textarea className="dk-input" rows={5} value={text} onChange={e => setText(e.target.value)} />
        </label>
        <label className="vc-field">
          <span>{t('speech.instructions')}</span>
          <input className="dk-input" value={instructions} onChange={e => setInstructions(e.target.value)} />
        </label>
        <ui.Button variant="primary" busy={busy} onClick={run}>{t('speech.generate')}</ui.Button>
        {error && <ui.ErrorWithTraceLink message={error} />}
        {audioUrl && <ui.WaveformPlayer src={audioUrl} label={t('speech.result')} />}
        {!viewer.hasFeature('audio_speech') && <p className="vc-note">{t('speech.noAccess')}</p>}
      </main>
    )
  }

  return { Speech }
}
```

Add to `i18n/en.js` the keys `'speech.model': 'Model'`, `'speech.voice': 'Voice'`, `'speech.defaultVoice': 'Default voice'`, `'speech.text': 'Text'`, `'speech.instructions': 'Instructions (optional)'`, `'speech.generate': 'Generate speech'`, `'speech.result': 'Result'`, `'speech.noAccess': 'Your account has no access to text-to-speech. The server will refuse the request.'`. The `tts.toasts.*` keys come from the generated bundle. In `index.jsx` import `createSpeech` and set `pages.speech = createSpeech(ctx).Speech`. Append `.vc-field { display: flex; flex-direction: column; gap: var(--dk-space-1, 4px); margin-bottom: var(--dk-space-3, 12px); } .vc-note { color: var(--dk-text-muted, var(--dk-text)); }` to `style.css`.

- [ ] **Step 4: Run to see it pass**

Run: `node addons/voice/ui/test/run.mjs`
Expected: all `passed`.

- [ ] **Step 5: Commit**

```bash
git add addons/voice
git commit -m "feat(addons): add the Voice text-to-speech page

A page of the Voice add-on that speaks text with a voice model through
the core POST /v1/audio/speech route, using the signed-in user's
session. Studio keeps its own audio generation.

Assisted-by: Claude:claude-sonnet-5-5 node"
```

---

### Task 16: Voice library pages (Voice Library, Voice Profile Create)

**Files:**
- Create: `addons/voice/ui/src/api.js`, `lib/` (copies), `VoiceLibrary.jsx`, `VoiceProfileCreate.jsx`
- Modify: `addons/voice/ui/src/index.jsx`, `style.css`, `i18n/en.js`
- Test: `addons/voice/ui/test/library.test.mjs`
- Modify: `core/http/react-ui/src/router.jsx` and `core/http/react-ui/src/utils/legacyRoutes.js` (the conditional redirect, Step 6)

**Interfaces:**
- Consumes: the source pages `core/http/react-ui/src/pages/VoiceLibrary.jsx` and `VoiceProfileCreate.jsx`; `utils/referenceAudio` (`MAX_AUDIO_BYTES`, `normalizeAudioSample`); `voiceProfilesApi.list/create/delete/audioUrl` (`core/http/react-ui/src/utils/api.js:378-388`); the port table above.
- Produces: `createApi(ctx) -> { voiceProfiles: { list(), create(formData), remove(id), audioUrl(id) }, models: { list(params), listCapabilities() } }` in `src/api.js`; `createVoiceLibrary(ctx, api) -> { VoiceLibrary }`; `createVoiceProfileCreate(ctx, api) -> { VoiceProfileCreate }`; pages `library` and `create` of the add-on. The route redirects `/app/voice-library` and `/app/voice-library/new` to them while the add-on is usable.

- [ ] **Step 1: Write the failing test**

`addons/voice/ui/test/library.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { load } from './render.test.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(join(resolve(here, '../../../../core/http/react-ui'), 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

test('library: the first paint of the library page has the core test id', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'library', path: '' }))
  assert.match(html, /data-testid="voice-library"/)
})

test('create: the first paint of the create page has the core test id', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'create', path: '' }))
  assert.match(html, /data-testid="voice-profile-create"/)
})

test('api: the voice profile calls use the core routes through sdk.core.fetch', async () => {
  const { mod, sdk, calls } = await load()
  const { createApi } = await import('../src/api.js')
  const { createContext } = await import('../src/ctx.jsx').catch(() => ({}))
  const ctx = mod.__ctxFor ? mod.__ctxFor(sdk) : { core: sdk.core, json: async (p, i) => (await sdk.core.fetch(p, i)).json() }
  const api = createApi(ctx)
  await api.voiceProfiles.list()
  await api.voiceProfiles.remove('a/b')
  assert.equal(calls[0][0], '/api/voice-profiles')
  assert.equal(calls[1][0], '/api/voice-profiles/a%2Fb')
  assert.equal(calls[1][1].method, 'DELETE')
  assert.equal(api.voiceProfiles.audioUrl('a/b'), '/api/voice-profiles/a%2Fb/audio')
  void createContext
})

test('library: the admin-only actions are hidden for a viewer who is not an administrator', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: () => true } })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'library', path: '' }))
  assert.doesNotMatch(html, /data-testid="voice-library-new"/)
})
```

- [ ] **Step 2: Run to see it fail**

Run: `node addons/voice/ui/test/run.mjs`
Expected: `failed` lines: the pages render nothing (`library` and `create` are not in `pages`), `../src/api.js` is missing.

- [ ] **Step 3: Write the data layer**

`addons/voice/ui/src/api.js`:

```js
// The core routes the Voice add-on uses, reached with the signed-in user's own
// session (sdk.core.fetch). The shapes are the ones of
// core/http/react-ui/src/utils/api.js; only the transport changed.
const enc = encodeURIComponent

export function createApi(ctx) {
  const { core, json } = ctx
  const query = (params = {}) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') q.set(k, String(v))
    const s = q.toString()
    return s ? `?${s}` : ''
  }
  return {
    voiceProfiles: {
      list: () => json('/api/voice-profiles'),
      create: (formData) => json('/api/voice-profiles', { method: 'POST', body: formData }),
      remove: (id) => json(`/api/voice-profiles/${enc(id)}`, { method: 'DELETE' }),
      audioUrl: (id) => `/api/voice-profiles/${enc(id)}/audio`,
    },
    models: {
      listCapabilities: () => json('/api/models/capabilities'),
      // The gallery list, as modelsApi.list builds it: API_CONFIG.endpoints.models
      // is '/api/models' (core/http/react-ui/src/utils/config.js:11) and empty
      // parameters are left out (utils/api.js buildUrl).
      list: (params) => json(`/api/models${query(params)}`),
    },
    raw: core,
  }
}
```

`audioUrl` is used as an `<audio src>`, so the browser sends the session cookie itself and `sdk.core.fetch` is not involved. When LocalAI is served under a path prefix the path needs that prefix: add `basePath` to `ctx` in `ctx.jsx` as `const basePath = typeof document === 'undefined' ? '' : new URL(document.baseURI).pathname.replace(/\/$/, '')`, and make `audioUrl` return `` `${ctx.basePath}/api/voice-profiles/${enc(id)}/audio` ``. The test in this task builds the context without a document, where the prefix is empty.

Copy `core/http/react-ui/src/utils/referenceAudio.js` to `addons/voice/ui/src/lib/referenceAudio.js`. It imports `audioBufferToWavBlob` from the host hook module (`core/http/react-ui/src/hooks/useMediaCapture.js:8`); in the copy, take it from `ctx.hooks.audioBufferToWavBlob` (exported by SDK 2 in Task 10) by turning the module into `createReferenceAudio(ctx)` that returns `{ MAX_AUDIO_BYTES, normalizeAudioSample }`. Check what else it imports with `grep -n "^import" core/http/react-ui/src/utils/referenceAudio.js`; any other import must be a pure helper that you copy next to it.

- [ ] **Step 4: Port the two pages**

Create `src/VoiceLibrary.jsx` and `src/VoiceProfileCreate.jsx` from `core/http/react-ui/src/pages/VoiceLibrary.jsx` and `VoiceProfileCreate.jsx` using the port table. Specific mappings for these two files:

- `VoiceLibrary.jsx`: `ConfirmDialog` to `ctx.ui.ConfirmDialog`; `EmptyState` to `ctx.ui.EmptyState`; `LoadingSpinner` to `ctx.ui.Spinner`; `SideSheet` to `ctx.openPanel`; `WaveformPlayer` to `ctx.ui.WaveformPlayer`; `useModels`, `useOperations`, `useVoiceCloningGallery` to local hooks in the factory built on `api.models.*`; `modelsApi`, `voiceProfilesApi` to `api.*`; `copyToClipboard` and `renderMarkdown` copied from `utils/clipboard` and `utils/markdown` into `src/lib/` (both are pure; `renderMarkdown` uses `marked` and `dompurify`, which the add-on bundle must not import from the host, so render the gallery description as plain text with `whiteSpace: 'pre-wrap'` and say so in the file header).
- `VoiceProfileCreate.jsx`: `UnsavedChangesGuard` to `ctx.markDirty`; `ClipInput` from the shared identity source of Task 17 (`addons/internal/identity-ui/src/ClipInput.jsx`); `WaveformPlayer` to `ctx.ui.WaveformPlayer`; `MAX_AUDIO_BYTES`, `normalizeAudioSample` from `src/lib/referenceAudio.js`.
- The root element of each page keeps `data-testid="voice-library"` and `data-testid="voice-profile-create"`. The "new profile" button of the library keeps `data-testid="voice-library-new"` and is drawn only when `ctx.viewer.isAdmin()`: the server already refuses the create call for everyone else (`core/http/routes/localai.go:207`, admin middleware), so this is a convenience and the 403 is the control.
- Routes to other pages: `/app/voice-library/new` becomes `<ctx.Link page="create">`; `/app/voice` becomes `<ctx.Link page="recognition">`; `/app/tts?voice=<id>` (the "speak with this voice" link) becomes `<ctx.Link page="speech" query={`voice=${id}`}>`, and `Speech.jsx` reads `ctx.useSearch().get('voice')` to preselect the voice (add this to Task 15's page when you reach this step: `const [voiceId, setVoiceId] = useState(ctx.useSearch().get('voice') || '')`).

In `index.jsx` set `pages.library = createVoiceLibrary(ctx, api).VoiceLibrary` and `pages.create = createVoiceProfileCreate(ctx, api).VoiceProfileCreate`, where `const api = createApi(ctx)`. Append the rules of `core/http/react-ui/src/pages/identity.css` that the two pages use to `style.css` (copy the rules whose class names the two files contain: `grep -o 'idn-[a-z_-]*\|dk-[a-z_-]*' src/VoiceLibrary.jsx src/VoiceProfileCreate.jsx | sort -u`).

- [ ] **Step 5: Run to see it pass**

Run: `node addons/voice/ui/test/run.mjs && cd core/http/react-ui && npm run test:unit`
Expected: all `passed`.

- [ ] **Step 6: Redirect the old routes while the add-on is usable**

The old pages stay in core for one release (spec, "Rollback"). The redirect applies only when the Voice add-on is listed, enabled and compatible, otherwise the core page opens. In `core/http/react-ui/src/router.jsx` add:

```jsx
import { useAddonRegistry } from './addons/useAddonRegistry'
import { appStatus } from './addons/registryState'

// A moved biometric page opens in its add-on while the add-on is usable and in
// the core page otherwise, so a user who has not installed the add-on loses
// nothing.
function MovedPage({ addon, page, children }) {
  const { apps, acquire } = useAddonRegistry()
  const { search, hash } = useLocation()
  useEffect(() => acquire(), [acquire])
  const app = apps.find(a => a.id === addon)
  if (appStatus(app) === 'ready' && app.ui.pages.includes(page)) {
    return <Navigate to={`/app/addons/${addon}/${page}${search}${hash}`} replace />
  }
  return children
}
```

(import `useEffect` from `react`.) Wrap the two core routes: `voice-library` (line 239) as `<Admin><MovedPage addon="voice" page="library"><VoiceRecognition tab="speech" /></MovedPage></Admin>` and `voice-library/new` (line 298) as `<Admin><MovedPage addon="voice" page="create"><VoiceProfileCreate /></MovedPage></Admin>`.

Add to `legacyRoutes.js` `MIGRATION_ROUTES`: `{ from: '/app/voice-library', to: '/app/addons/voice/library', kind: 'addon-page' }` and `{ from: '/app/voice-library/new', to: '/app/addons/voice/create', kind: 'addon-page' }`, and to `legacyroutes.test.mjs`:

```js
test('moved biometric routes are wrapped in MovedPage in the router', () => {
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'addon-page')) {
    const [, , , addon, page] = r.to.split('/')
    assert.ok(router.includes(`<MovedPage addon="${addon}" page="${page}">`), `router.jsx does not wrap ${r.from}`)
  }
})
```

Run: `cd core/http/react-ui && npm run test:unit && npm run lint`. Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add addons/voice core/http/react-ui
git commit -m "feat(addons): move voice library management into the Voice add-on

Voice Library and Voice Profile Create become pages of the Voice add-on
and talk to /api/voice-profiles through the user's session. The old
routes open the add-on while it is usable and the core page otherwise.

Assisted-by: Claude:claude-sonnet-5-5 node"
```

---

### Task 17: Shared identity views and the Voice Recognition page

**Files:**
- Create: `addons/internal/identity-ui/src/` (`Workbench.jsx`, `ClipInput.jsx`, `KnownList.jsx`, `EnrolSheet.jsx`, `Verdict.jsx`, `DistanceScale.jsx`, `ErrorNote.jsx`, `ModelNeeded.jsx`, `registry.js`)
- Create: `addons/voice/ui/src/VoiceRecognition.jsx`, `MoreVoiceTools.jsx`, `WaveformStrip.jsx`
- Modify: `addons/voice/ui/build.mjs`, `index.jsx`, `style.css`, `i18n/en.js`
- Test: `addons/voice/ui/test/recognition.test.mjs`, `addons/internal/identity-ui/test/identity.test.mjs`
- Modify: `core/http/react-ui/src/router.jsx`, `legacyRoutes.js`, `legacyroutes.test.mjs`
- Modify: `docs/content/features/addons.md`, `docs/content/` pages that describe the Voices page (`grep -rln "voice" docs/content/features`)

**Interfaces:**
- Consumes: the source `core/http/react-ui/src/components/identity/*` and `components/biometrics/WaveformStrip.jsx`, `pages/VoiceRecognition.jsx`; the port table; `ctx` of Task 14 plus `api` of Task 16; SDK `ui.MediaInput`, `ui.DistributionBars`, `ui.EmbeddingInspector`, `ui.TabSwitch`, `ui.ModelSelector`, `ui.ErrorWithTraceLink`, `hooks.useMediaCapture`, `hooks.useObjectUrl`, `hooks.useDelayedAction`.
- Produces:
  - Shared source (built into each add-on that imports it; the SDK never carries it): `createWorkbench(ctx, deps) -> { Workbench }` where `Workbench({ kind: 'voice'|'face', model, canSpeech, more, needed, onCount })` has the props of the core `Workbench` (`core/http/react-ui/src/components/identity/Workbench.jsx:100`); `createClipInput(ctx)`, `createModelNeeded(ctx, api)`, `createIdentityRegistry(ctx)`.
  - `createVoiceRecognition(ctx, api, identity) -> { VoiceRecognition }`; page `recognition` of the Voice add-on.
  - The build of an add-on resolves `../../internal/identity-ui/src/*` through relative imports (rolldown follows them); no alias is needed.

Where the shared views live: `addons/internal/identity-ui/src`, built twice (once into the Voice bundle, once into the Face bundle) from one source copy. They are views, so they never go in the SDK (spec, "Classification"). `BoundingBoxCanvas` is used by the face side only, so it lives in `addons/internal/identity-ui/src/BoundingBoxCanvas.jsx` because `Workbench` imports it, and is built into the Voice bundle only if the voice path references it (tree-shaking drops it otherwise); it is not exported by the SDK.

- [ ] **Step 1: Write the failing tests**

`addons/voice/ui/test/recognition.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { load } from './render.test.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(join(resolve(here, '../../../../core/http/react-ui'), 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

test('recognition: the first paint has the core test id and the page title', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="voices-page"/)
  assert.match(html, /Voices/)
})

test('recognition: a viewer without voice_recognition gets the off notice, not the workbench', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: (f) => f !== 'voice_recognition' } })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="voice-off"/)
  assert.doesNotMatch(html, /idn-bench/)
})

test('recognition: the model comes from the first path segment', async () => {
  const { mod, sdk, calls } = await load()
  const addon = await mod.createAddon(sdk)
  renderToString(React.createElement(addon.Component, { page: 'recognition', path: 'speechbrain' }))
  assert.ok(Array.isArray(calls))
})
```

`addons/internal/identity-ui/test/identity.test.mjs` tests the pure helpers of `registry.js` (the "known people" store is `localStorage` based: `loadList`/`saveList` of `core/http/react-ui/src/utils/identity.js`; copy that file's pure functions into `addons/internal/identity-ui/src/identity.js`, then test them):

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import * as identity from '../src/identity.js'

test('loadList returns an empty list for a missing or corrupt value', () => {
  globalThis.localStorage = { getItem: () => '{not json' }
  assert.deepEqual(identity.loadList('localai_voice_enrollments'), [])
  globalThis.localStorage = { getItem: () => null }
  assert.deepEqual(identity.loadList('localai_voice_enrollments'), [])
})

test('loadList keeps a stored list', () => {
  globalThis.localStorage = { getItem: () => JSON.stringify([{ id: 'a' }]) }
  assert.deepEqual(identity.loadList('k'), [{ id: 'a' }])
})
```

(Adjust the two assertions to the real behaviour of `loadList` in `core/http/react-ui/src/utils/identity.js`: read it first and assert what it does, since this test exists to pin the copied behaviour.)

- [ ] **Step 2: Run to see it fail**

Run: `node addons/voice/ui/test/run.mjs && node --test addons/internal/identity-ui/test/identity.test.mjs`
Expected: failures: the page `recognition` draws nothing; `../src/identity.js` is missing.

- [ ] **Step 3: Create the shared identity source**

For each file of the left column, create the file of the right column with the port table applied. Behaviour, class names, `data-testid` values and i18n keys stay the same.

| Core source | Shared source |
|---|---|
| `utils/identity.js` (pure) | `addons/internal/identity-ui/src/identity.js`, copied unchanged |
| `components/identity/useRegistry.js` | `registry.js`: `createIdentityRegistry(ctx)` returning the hook; `useDelayedAction` from `ctx.hooks`; `utils/api` calls from `ctx.json` against `/v1/<kind>/register`, `/v1/<kind>/identify`, `/v1/<kind>/forget` (the routes at `core/http/routes/localai.go:138-167`) |
| `components/identity/ClipInput.jsx` | `ClipInput.jsx`: `createClipInput(ctx)`; `useMediaCapture`, `useObjectUrl` from `ctx.hooks`; `WaveformPlayer` from `ctx.ui` |
| `components/identity/Workbench.jsx` | `Workbench.jsx`: `createWorkbench(ctx, deps)` where `deps = { ClipInput, KnownList, EnrolSheet, Verdict, DistanceScale, ErrorNote, useRegistry, BoundingBoxCanvas, api }` |
| `components/identity/KnownList.jsx`, `Verdict.jsx`, `DistanceScale.jsx`, `ErrorNote.jsx` | same names, each a `createX(ctx)` |
| `components/identity/EnrolSheet.jsx` | `EnrolSheet.jsx`: the side sheet becomes `ctx.openPanel` |
| `components/identity/ModelNeeded.jsx` | `ModelNeeded.jsx`: `useOperations` becomes a local poll of `GET /api/operations` (`API_CONFIG.endpoints.operations`, `core/http/react-ui/src/utils/config.js:4`, read by `modelsApi`-style helpers at `utils/api.js:176`) through `ctx.json`; keep the completion rule of the core hook (`op.name === id && (op.completed || op.error)`, as `ModelNeeded.jsx` uses it); `modelsApi.list` becomes `api.models.list` |
| `components/biometrics/BoundingBoxCanvas.jsx` | `BoundingBoxCanvas.jsx`, canvas code unchanged, `h` instead of JSX runtime |

- [ ] **Step 4: Port the Voice Recognition page**

Create from the core sources with the port table: `VoiceRecognition.jsx` (from `pages/VoiceRecognition.jsx`: the `Link` tabs become `ctx.Link` to `recognition`, `library` and `recognition?tab=recording`; the Speech voices tab embeds the library page of Task 16 as `<VoiceLibrary />`, kept as today), `MoreVoiceTools.jsx` (from `components/identity/MoreVoiceTools.jsx`: `TabSwitch`, `MediaInput`, `DistributionBars`, `EmbeddingInspector`, `ErrorWithTraceLink` from `ctx.ui`), `WaveformStrip.jsx` (from `components/biometrics/WaveformStrip.jsx`; one add-on uses it, so it stays here).

Gating, as the core route has it (`router.jsx:237`): `if (!ctx.viewer.hasFeature('voice_recognition')) return <div data-testid="voice-off">…{ctx.t('voice.off')}</div>` (the core `FeatureOff` component, `components/identity/FeatureOff.jsx`, is ported as a local `createFeatureOff(ctx)`; keep its text keys). Model: the first segment of `path` when present (`/app/voice/:model`).

Register `pages.recognition = createVoiceRecognition(ctx, api, identity).VoiceRecognition` in `index.jsx`. Append the used `idn-*` and `dk-*` rules of `pages/identity.css` to `style.css`.

- [ ] **Step 5: Run to see it pass**

Run: `node addons/voice/ui/test/run.mjs && node --test addons/internal/identity-ui/test/identity.test.mjs`
Expected: all `passed`.

- [ ] **Step 6: Parity check against the core page, redirects, docs**

Add to `core/http/react-ui/e2e/addon-navigation.spec.js` a case that serves the built Voice module the way `e2e/addons-shell.spec.js` serves the `uifixture` module (`page.route` on `/api/addons/voice/assets/<digest>/…` from `addons/voice/ui/dist`), opens `/app/addons/voice/recognition` and asserts the same `data-testid` values and tab labels as the core `/app/voice` page (`voices-page`, the tab names `Speakers` and `Speech voices`). Run the same assertions on the core page with the add-on absent.

In `router.jsx` wrap the `voice` and `voice/:model` routes (lines 237-238) in `MovedPage` (Task 16): `<MovedPage addon="voice" page="recognition"><Feature feature="voice_recognition" disabled={<FeatureOff feature="voice" />}><VoiceRecognition /></Feature></MovedPage>`. `/app/voice/:model` redirects to `/app/addons/voice/recognition` plus the model: extend `MovedPage` with an optional `withModel` prop that appends `/${useParams().model}` to the page path (import `useParams`; the add-on reads the first `path` segment). Add to `MIGRATION_ROUTES` `{ from: '/app/voice', to: '/app/addons/voice/recognition', kind: 'addon-page' }`. Update `legacyroutes.test.mjs` only if its pattern needs the `withModel` attribute.

In `hubConfig.js` the `voices` tab (lines 105-114) stays: it still serves users without the add-on. Document the Voice add-on in `docs/content/features/addons.md` (pages, the core routes it uses, install and enable) and update the Voices page description under `docs/content/features`.

Run: `cd core/http/react-ui && npm run lint && npm run test:unit && npx playwright test e2e/addon-navigation.spec.js` and `node addons/voice/ui/test/run.mjs`. Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add addons core/http/react-ui docs/content
git commit -m "feat(addons): move voice recognition into the Voice add-on

The recognition page and its workbench become a page of the Voice
add-on, built from views that the Face add-on shares through one
source directory. The old routes open the add-on while it is usable.

Assisted-by: Claude:claude-sonnet-5-5 node playwright"
```

---

## Task group E: the Face add-on

The Face add-on is one package, `addons/face`, id `face`. Its page: `recognition` (main page). The face library management (the known-faces list and the enrol sheet) is part of the same page, as it is in core today (`KnownList` and `EnrolSheet` inside `Workbench`). Core keeps `/v1/face/*` and the face registry.

### Task 18: Face add-on package and recognition page

**Files:**
- Create: `addons/face/manifest.json`, `cmd/face/main.go`, `package/platforms.yaml`, `ui/package.json`, `ui/build.mjs`
- Create: `addons/face/ui/src/index.jsx`, `ctx.jsx`, `api.js`, `FaceRecognition.jsx`, `MoreFaceTools.jsx`, `style.css`, `i18n/en.js`
- Test: `addons/face/ui/test/render.test.mjs`, `addons/face/ui/test/run.mjs`, `addons/face/ui/test/recognition.test.mjs`
- Modify: `.github/workflows/addons-package.yml`, `core/http/react-ui/src/router.jsx:235-236`, `core/http/react-ui/src/utils/legacyRoutes.js`, `core/http/react-ui/tests/unit/legacyroutes.test.mjs`
- Modify: `docs/content/features/addons.md`

**Interfaces:**
- Consumes: Tasks 12, 13, 14 (copy `ctx.jsx` and `build.mjs` shapes), 17 (shared identity source), the sources `core/http/react-ui/src/pages/FaceRecognition.jsx` and `components/identity/MoreFaceTools.jsx`; routes `/v1/face/{verify,analyze,embed,register,identify,forget}` (`core/http/routes/localai.go:129-145`), `GET /api/models/capabilities`; capability `CAP_FACE_RECOGNITION` (`core/http/react-ui/src/utils/capabilities.js`).
- Produces: manifest `id: "face"`, `ui.sdk = {min: 2, max: 2}`, `ui.pages = ["recognition"]`, `ui.mainPage = "recognition"`, title `Face`; `createFaceRecognition(ctx, api, identity) -> { FaceRecognition }`; `createMoreFaceTools(ctx, api) -> { MoreFaceTools }`.

- [ ] **Step 1: Write the failing tests**

`addons/face/ui/test/render.test.mjs` is the file of Task 14 with these differences: `import { build } from '../build.mjs'` builds `ui/face.js`; `addonId: 'face'`; `pages: ['recognition']`; the manifest test reads `../../manifest.json` and asserts `m.id === 'face'`, `pages` equal `['recognition']`, `mainPage` equal `'recognition'`, `sdk` equal `{ min: 2, max: 2 }` and no tools. `addons/face/ui/test/recognition.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { load } from './render.test.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(join(resolve(here, '../../../../core/http/react-ui'), 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

test('recognition: the first paint has the faces page test id', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="faces-page"/)
})

test('recognition: a viewer without face_recognition gets the off notice', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: (f) => f !== 'face_recognition' } })
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'recognition', path: '' }))
  assert.match(html, /data-testid="face-off"/)
})

test('recognition: register, identify and forget use the core face routes', async () => {
  const { createApi } = await import('../src/api.js')
  const seen = []
  const ctx = { core: { fetch: async (p, i) => { seen.push([p, i?.method]); return { ok: true, json: async () => ({}) } } }, json: async (p, i) => { seen.push([p, i?.method]); return {} } }
  const api = createApi(ctx)
  await api.face.register({ model: 'm', img: 'x', name: 'a' })
  await api.face.identify({ model: 'm', img: 'x' })
  await api.face.forget({ model: 'm', id: 'a' })
  assert.deepEqual(seen.map(s => s[0]), ['/v1/face/register', '/v1/face/identify', '/v1/face/forget'])
  assert.deepEqual(seen.map(s => s[1]), ['POST', 'POST', 'POST'])
})
```

- [ ] **Step 2: Run to see it fail**

Run: `node addons/face/ui/test/run.mjs`
Expected: `failed`: `Cannot find module '../build.mjs'`.

- [ ] **Step 3: Write the package**

`addons/face/manifest.json`: the manifest of Task 14 with `"id": "face"`, `"executable": "face"`, `"entry": "ui/face.js"`, `"css": ["ui/face.css"]`, `"pages": ["recognition"]`, `"navigation": []`, `"mainPage": "recognition"`, `"title": "Face"`, `"description": "Find out whose face is in a photo, or whether two photos show the same person."`. `addons/face/cmd/face/main.go`: the file of Task 14 with the package comment `Command face is the executable of the Face add-on`. `package/platforms.yaml`, `ui/package.json` (name `localai-face-ui`) and `ui/build.mjs` (name `face`, `rgb: [20, 100, 90]`) follow Task 14. Add `face` to `.github/workflows/addons-package.yml` like `voice`.

`addons/face/ui/src/api.js`:

```js
// The core face routes, reached with the signed-in user's own session. Paths
// and bodies are the ones of faceApi in core/http/react-ui/src/utils/api.js:
// register takes { model, name, img, labels }, identify { model, img, ... }.
export function createApi(ctx) {
  const post = (path, body) => ctx.json(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  return {
    face: {
      verify: (body) => post('/v1/face/verify', body),
      analyze: (body) => post('/v1/face/analyze', body),
      embed: (body) => post('/v1/face/embed', body),
      register: (body) => post('/v1/face/register', body),
      identify: (body) => post('/v1/face/identify', body),
      forget: (body) => post('/v1/face/forget', body),
    },
    models: {
      listCapabilities: () => ctx.json('/api/models/capabilities'),
      list: (params) => ctx.json(`/api/models?${new URLSearchParams(Object.entries(params || {}).filter(([, v]) => v !== undefined && v !== null && v !== '')).toString()}`),
    },
  }
}
```

The test above pins the paths and the method. The page sends the field names `faceApi` sends (`EnrolSheet.jsx:57`: `{ model, name, img: sample.dataUrl, labels }`).

`ctx.jsx` is the file of Task 14 with `i18n/en.js` built from the `faces.*`, `face.*`, `tool.*`, `cutoff.*`, `why.*`, `known.*` keys of `biometrics.json` (run the one-time script of Task 14 with the prefix list `["faces","face","tool","why","cutoff","known","enrol"]` and output `addons/face/ui/src/i18n/en.js`).

- [ ] **Step 4: Port the page**

Create `FaceRecognition.jsx` from `pages/FaceRecognition.jsx` and `MoreFaceTools.jsx` from `components/identity/MoreFaceTools.jsx` with the port table, importing the shared views from `../../../internal/identity-ui/src/` (relative path from `addons/face/ui/src`). The page root keeps `data-testid="faces-page"`; the off notice keeps `data-testid="face-off"` and shows when `!ctx.viewer.hasFeature('face_recognition')` (as the core route does, `router.jsx:235`). The model is the first segment of `path`. The face library management (known-faces list and enrol sheet) is the shared `KnownList` and `EnrolSheet` inside the shared `Workbench`, unchanged.

`index.jsx` (same structure as Task 14) sets `pages.recognition = createFaceRecognition(ctx, api, identity).FaceRecognition`.

- [ ] **Step 5: Run to see it pass**

Run: `node addons/face/ui/test/run.mjs && go build ./addons/...`
Expected: all `passed`, clean build.

- [ ] **Step 6: Redirects, parity, docs and commit**

In `router.jsx` wrap the `face` and `face/:model` routes (lines 235-236) in `MovedPage addon="face" page="recognition"` as in Task 17. Add `{ from: '/app/face', to: '/app/addons/face/recognition', kind: 'addon-page' }` to `MIGRATION_ROUTES`. Add the parity e2e case for the Face page (same method as Task 17: serve the built module, assert `faces-page` and the page title against the core `/app/face` page). Document the Face add-on in `docs/content/features/addons.md` and the face recognition docs page (`grep -rln "face" docs/content/features`).

Run: `cd core/http/react-ui && npm run lint && npm run test:unit && npx playwright test e2e/addon-navigation.spec.js`
Expected: PASS.

```bash
git add addons core/http/react-ui docs/content .github/workflows/addons-package.yml
git commit -m "feat(addons): add the Face add-on

Face Recognition and its library management become one add-on page
built on SDK 2 and the shared identity views. It calls /v1/face/* with
the user's session. The old routes open the add-on while it is usable.

Assisted-by: Claude:claude-sonnet-5-5 node playwright"
```

---

## Task group F: fine-tuning and quantization move (later; steps only)

These steps are not tasks. Start them only after group A is merged. They follow Task 1 for the catalog and Task 7 for the redirects.

1. Add two entries to `catalog` in `core/services/addons/builtin/builtin.go`: `{ID: "fine-tune", Kind: KindPage, TitleKey: "items.fineTune", Icon: "graduation-cap", MainPath: "/app/fine-tune", Feature: "fine_tuning"}` and `{ID: "quantize", Kind: KindPage, TitleKey: "items.quantize", Icon: "minimize", MainPath: "/app/quantize", Feature: "quantization"}`. Add a test case to `builtin_test.go`: a viewer without `fine_tuning` does not see `fine-tune`.
2. The built-in catalog already applies `fine_tuning` and `quantization`. The spec's gate table says the tile needs the feature plus `addons`: for these two entries the drawer shows them to a viewer who holds the `addons` feature. Add `RequiresAddons bool` to `Entry` (JSON `requiresAddons`), set it on both, make `Viewer.allows` require `Has("addons")` when it is set, and test both sides.
3. Keep the core routes `fine-tune` and `quantize` (`router.jsx:233-234`) unchanged for one release. The API routes stay in core.
4. Remove the `fine-tune` and `quantize` tabs from the Build hub (`hubConfig.js:84-97`) in the same change that adds the entries. Users without the `addons` feature keep a way in: leave the two core routes reachable by URL, and keep a link on the Build overview page (`core/http/react-ui/src/pages/BuildOverview.jsx`) when the viewer lacks the `addons` feature.
5. Add `fine-tune` and `quantize` to `MIGRATION_ROUTES` as `same` (the paths do not change, the page is opened from the drawer) and extend `legacyroutes.test.mjs` for them.
6. Add an e2e case to `addon-navigation.spec.js`: a member without `fine_tuning` does not see the fine-tuning tile; with it, the tile opens `/app/fine-tune`.
7. Update `docs/content/features/addons.md` and the fine-tuning and quantization docs pages.
8. Commit as `feat(addons): open fine-tuning and quantization from the Add-ons drawer`.

## Task group G: cleanup after one release (later; steps only)

1. Remove the legacy switch: delete `LegacyNavigation` from `core/config/application_config.go`, the flag in `core/cli/run.go`, the `addon_navigation` key in `core/http/routes/localai.go`, and the `legacy` branches in `Sidebar.jsx` and `NotFound.jsx`.
2. Delete the core copies of the moved pages (`pages/FaceRecognition.jsx`, `VoiceRecognition.jsx`, `VoiceLibrary.jsx`, `VoiceProfileCreate.jsx`, `components/identity/*`) only after the add-ons are installed by default or a separate decision removes the core fallback. Keep every redirect: the spec says redirects stay until a separate decision removes them.

## Task group H: blocked on owner decision (this plan does not start it)

- **Question (c): the drawer entry of Voice Library.** The Voice add-on contains the Voice Library page (`/app/addons/voice/library`, Task 16). Open: whether it also has a drawer tile of its own, or is reached only from inside the Voice add-on (a tab, and `ui.navigation`, which already lists it in the detail view and the pinned group). Do not add a second manifest, a second tile or a catalog entry for it until the owner answers. When the owner decides, the work is a manifest or catalog change plus one test of the tile list.

---

## Self-review

### 1. Spec coverage

| Spec section | Task |
|---|---|
| Approved decision 1: four top-level items | Task 5 (`primaryDestinations`), Task 6 (sidebar) |
| Decision 2: drawer page, tiles, search, Browse, Add | Task 6 (`AppsView`, `AddonTile`); the Add entry opens the Browse tab, which hosts the install action (`BrowseView.jsx:48-55`); Browse and Add show to administrators because the Browse tab is an administrator tab (`addonState.js:7`) |
| Decision 3: per-user pins, sidebar short list, drawer all | Tasks 2, 3, 5, 6 |
| Decision 4: Studio (Images, Audio, Video) and Talk as add-ons | Task 1 (catalog), Task 7 (redirects into Studio tabs), Task 6 (tiles and pins) |
| Decision 5: fine-tuning and quantization | Group F (steps only) |
| Decision 6: Voice and Face add-ons, core keeps API and stores | Groups C, D, E (Tasks 12-18); the routes and stores are not touched |
| Decision 7: Agents, Memory, Skills out of scope | no task, by design |
| Decision 8: core routes with the user's session | Task 11, plus the Go 403 test |
| Decision 9: SDK primitives and per-component decisions | Task 9 (version), Task 10 (exports), classification followed in Tasks 16, 17, 18 |
| Navigation model: top-level items table | Task 5 (`visiblePrimary`), Task 6 |
| Drawer page parts and tile content | Task 6 |
| Tile states | Task 5 (`tileFromApp`), Task 6 (detail view) |
| Manifest fields for navigation (no new field) | Tasks 14, 18 use existing `ui.*` fields; Task 6 shows `ui.navigation` in the detail view and the pinned group |
| Built-in add-ons (page vs packaged) | Task 1 (page), Tasks 12-18 (packaged) |
| Pinned section: order, cap of eight, muted, hidden when empty | Task 5 (`togglePin`, `resolvePins`), Task 6 (`PinnedNav`), Task 8 |
| Pinning: where the state lives, shared database, local owner | Task 2 (`GormStore`, `FileStore`), Task 3 (`pinUser`) |
| Pinning: API routes and the checklist | Task 3 (routes, swagger, docs, MCP map test); `RouteFeatureRegistry` is not used because the routes are user-scoped by design |
| Disabled or removed while pinned (table) | Task 3 (hidden pins kept on save), Task 4 (30-day sweep, reinstall restores), Task 6 and Task 8 (muted, hidden) |
| A user without access | Task 3 (`visibleIDs`, `TestListNeverRevealsAHiddenID`) |
| Permissions: Apps rules reused, `addons` feature, gate table | Task 1 (built-in gates), Task 6 (route gate, no list call without the feature); Voice and Face gates inside the add-on pages (Tasks 17, 18) |
| Decision: gate rule for Studio and Talk | Task 1, Task 5, Task 6 |
| Core routes from add-ons | Task 11 |
| Shared UI in the SDK and classification table | Tasks 9, 10; ports in Tasks 16-18 |
| Migration: mapping and `?tab=` | Task 7 (Studio routes), Tasks 16-18 (Voice, Face), Group F (fine-tuning, quantization) |
| Redirects (keep model, query, hash; permanent; no leak) | Task 7 |
| Sidebar after migration, one-time seed | Task 6 (Create group removed), Tasks 2 and 3 (seed) |
| Documentation | `docs/content/features/addons.md` is updated in Tasks 1, 3, 6, 7, 9, 10, 11, 17, 18 |
| Open question (a), (b) | decided; Tasks 11 and 9-10 |
| Open question (c) | Group H, not started |
| Test plan: route mapping | Task 7 |
| Test plan: pin storage | Tasks 2, 3 (two frontends: both frontends share the table by construction; see the gap below) |
| Test plan: permission filtering | Tasks 1, 3, 5, 6, 8, 11 |
| Test plan: tile states | Tasks 5, 6, 8 |
| Test plan: redirects | Task 7 |
| Test plan: quality gates | Task 8 |
| Definition of done | Task 8 and the groups above |
| Risks and rollback: flag, kept routes, additive table | Task 6 (flag), Tasks 16-18 (`MovedPage` keeps the core pages), Task 2 (additive table), Group G |

Gaps found and how they are closed:

- **Two frontends on one database** (test plan): closed in Task 2 (`TestTwoServicesOverOneDatabaseAgree`, `auth` tag). No message bus carries pin state, so the fake-bus pattern is not needed: the store reads and writes the shared table on every call.
- **Pin reorder control**: closed in Task 6 (`pin-earlier` and `pin-later` buttons on a pinned tile, with an e2e case that pins the saved order).
- **Studio Images, Audio and Video areas**: Task 7 routes every old generator into the Studio tab of the same name, which is where the spec's mapping table puts them. No extra work.
- **Spec test "A user without the `addons` feature sees no Add-ons item"**: the spec's own gate decision (Studio and Talk have no gate) changes this for built-in tiles; Task 5 and Task 6 test the decided behaviour, and the spec's test plan was amended in the same change.

### 2. Placeholder scan

Searched the plan for `TBD`, `TODO`, "implement later", "add appropriate", "similar to Task" and "write tests for the above": none. Where a task says "same as the core helper" it names the file, the symbol, the exact path and the field names (the gallery list path `/api/models`, the face and voice bodies of `faceApi` and `voiceApi`, `GET /api/operations`).

The page-body ports (Tasks 16, 17, 18) are specified by an exact substitution table and by per-file mappings, not by printing the converted files. That is deliberate: the converted bodies are mechanical copies of about two thousand lines of existing JSX, and the parity checks (same `data-testid`, same text, same routes called) are the acceptance test.

### 3. Type and name consistency

Checked across tasks: `builtin.Entry`, `Viewer`, `Visible`, `IDs` (Tasks 1, 3, 4); `pins.Store` methods `List`, `Replace`, `Seed`, `Distinct`, `MarkMissing`, `ClearMissing`, `DeleteMissingBefore` (Tasks 2, 3, 4); `Service.List` and `Service.Put` (Task 3); `GetPins` and `PutPins` signatures (Task 3); `RegisterAddonPinRoutes` (Task 3); `InstalledIDs` (Task 4); JS `tileFromApp`, `tileFromBuiltin`, `buildTiles`, `filterTiles`, `resolvePins`, `togglePin`, `movePin`, `PIN_CAP`, `primaryDestinations`, `visiblePrimary`, `createPinsApi` and its three methods (Tasks 5, 6); `useNavigation` fields `builtin`, `pinIds`, `pin`, `unpin`, `move` (Task 6); `studioRedirect`, `STUDIO_TABS`, `MIGRATION_ROUTES` kinds `same`, `studio-tab`, `addon-page` (Tasks 7, 16-18); `MovedPage` props `addon`, `page` (Tasks 16-18); `HostSDKVersion` (Task 9); `sdk.ui`, `sdk.hooks`, `sdk.viewer`, `sdk.core` (Tasks 10, 11, 14-18); `idle.Run`, `idle.Runtime`, `ErrNoTools` (Tasks 12, 14, 18); `buildAddonUI({ root, name, rgb })` (Tasks 13, 14, 18); `ctx` members (Task 14, used in 15-18).

Fixes made during the review: Task 4 first called the sweep input `apps`; it is `present` everywhere now. Task 6 first named the e2e id `drawer`; it is `addon-detail` everywhere. Task 10 exports `audioBufferToWavBlob` in `sdk.hooks` because Task 16's reference-audio helper needs it, so the SDK test asserts four hooks.

### 4. Review Focus coverage

1. Hidden pin wiped on save: Task 3 `TestPutKeepsPinsTheCallerCannotSee`.
2. User without the `addons` feature who uses Studio: Task 1 `TestStudioAndTalkAreVisibleWithoutAnyFeature`, Task 5 `addons entry shows for a built-in tile`, Task 6 e2e `member without the add-ons feature`.
3. Old link with a model, a query and a hash: Task 7 `studioRedirect keeps the model, the query and the hash` and the e2e case `an old generator link keeps its model, query and hash`.
4. Stopped, starting or failed tile: Task 5 `tileFromApp never targets the main page of a stopped add-on`, Task 6 e2e `a stopped tile opens the detail view`, Task 8 `a stopped pinned add-on is muted`.
5. Authentication off: Task 2 `TestFileStoreKeepsPinsAcrossAReopen`, Task 3 `TestListSeedsStudioAndTalkOnce` and `TestPinsRoundTripForTheLocalOwner`.

Each of the five has a test in the task that owns the code.
