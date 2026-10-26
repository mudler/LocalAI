package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

var (
	admin = workspace.Actor{Principal: "admin"}
	alice = workspace.Actor{Principal: "alice", ChatID: "c1"}
)

// newStore opens the GORM workspace store on a SQLite file, as the product does.
func newStore(t *testing.T) workspace.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := gormstore.New(db, blobs, gormstore.Options{Admin: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return st
}

func skillText(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody of " + name + ".\n"
}

// put writes a file as the admin, who can write everywhere.
func put(t *testing.T, st workspace.Store, p, content string) {
	t.Helper()
	if _, err := st.Put(context.Background(), admin, p, strings.NewReader(content), workspace.PutOptions{}); err != nil {
		t.Fatalf("put %s: %v", p, err)
	}
}

func grant(t *testing.T, st workspace.Store, who, p string, m workspace.Mode) {
	t.Helper()
	if _, err := st.Mkdir(context.Background(), admin, p); err != nil && !errors.Is(err, workspace.ErrExists) {
		t.Fatal(err)
	}
	if err := st.Grant(context.Background(), admin, who, p, m); err != nil {
		t.Fatal(err)
	}
}

// layers is a store where alice can read all four layers.
func layers(t *testing.T) (workspace.Store, []Root) {
	st := newStore(t)
	grant(t, st, "alice", "/users/alice", workspace.ModeWrite)
	grant(t, st, "alice", "/skills", workspace.ModeRead)
	grant(t, st, "alice", "/builtin-skills", workspace.ModeRead)
	grant(t, st, "alice", "/Shared/Team", workspace.ModeWrite)
	roots := []Root{
		{Layer: LayerBuiltin, Path: "/builtin-skills", Guest: "/workspace/shared-builtin-skills"},
		{Layer: LayerShared, Path: "/skills", Guest: "/workspace/shared-skills"},
		{Layer: LayerPersonal, Path: "/users/alice/.skills", Guest: "/workspace/home/.skills"},
		{Layer: LayerFolder, Path: "/users/alice", Guest: "/workspace/home"},
		{Layer: LayerFolder, Path: "/Shared/Team", Guest: "/workspace/shared-Team"},
	}
	return st, roots
}

func byPath(skills []Skill) map[string]Skill {
	m := map[string]Skill{}
	for _, s := range skills {
		m[s.Path] = s
	}
	return m
}

func TestTheNearestLayerWinsAndTheOthersAreShadowed(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/builtin-skills/report/SKILL.md", skillText("report", "Built-in report."))
	put(t, st, "/skills/report/SKILL.md", skillText("report", "Shared report."))
	put(t, st, "/users/alice/.skills/report/SKILL.md", skillText("report", "Personal report."))
	put(t, st, "/Shared/Team/Projects/.skills/report/SKILL.md", skillText("report", "Folder report."))
	put(t, st, "/skills/invoices/SKILL.md", skillText("invoices", "Shared invoices."))

	got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes: %v", notes)
	}
	m := byPath(got)
	want := map[string]struct {
		layer    Layer
		shadowed string
		guest    string
	}{
		"/Shared/Team/Projects/.skills/report": {LayerFolder, "", "/workspace/shared-Team/Projects/.skills/report/SKILL.md"},
		"/users/alice/.skills/report":          {LayerPersonal, "folder", "/workspace/home/.skills/report/SKILL.md"},
		"/skills/report":                       {LayerShared, "folder", "/workspace/shared-skills/report/SKILL.md"},
		"/builtin-skills/report":               {LayerBuiltin, "folder", "/workspace/shared-builtin-skills/report/SKILL.md"},
		"/skills/invoices":                     {LayerShared, "", "/workspace/shared-skills/invoices/SKILL.md"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d skills, want %d: %+v", len(got), len(want), got)
	}
	for p, w := range want {
		s, ok := m[p]
		if !ok {
			t.Fatalf("%s: missing", p)
		}
		if s.Layer != w.layer || s.Shadowed != w.shadowed || s.GuestPath != w.guest || s.Invalid != "" || len(s.Hash) != 64 {
			t.Errorf("%s: %+v, want %+v", p, s, w)
		}
	}
	// The result is ordered by layer, nearest first, then by name.
	var order []string
	for _, s := range got {
		order = append(order, string(s.Layer)+"/"+s.Name)
	}
	if strings.Join(order, " ") != "folder/report personal/report shared/invoices shared/report builtin/report" {
		t.Fatalf("order: %v", order)
	}
}

func TestTheHomeWalkDoesNotDuplicateThePersonalLayer(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/users/alice/.skills/mine/SKILL.md", skillText("mine", "Mine."))
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Layer != LayerPersonal {
		t.Fatalf("got %+v", got)
	}
}

func TestSwitchingASkillOffUncoversTheOneBelow(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/report/SKILL.md", skillText("report", "Shared report."))
	put(t, st, "/users/alice/.skills/report/SKILL.md", skillText("report", "Personal report."))
	off := func(s Skill) bool { return s.Layer == LayerPersonal && s.Name == "report" }
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{Off: off})
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(got)
	if p := m["/users/alice/.skills/report"]; !p.Off || p.Shadowed != "" {
		t.Errorf("personal: %+v", p)
	}
	if s := m["/skills/report"]; s.Off || s.Shadowed != "" {
		t.Errorf("shared should win now: %+v", s)
	}
}

func TestADeeperFolderWinsAndEqualDepthSortsByPath(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/Shared/Team/.skills/x/SKILL.md", skillText("x", "Shallow."))
	put(t, st, "/Shared/Team/A/B/.skills/x/SKILL.md", skillText("x", "Deep."))
	put(t, st, "/Shared/Team/C/.skills/y/SKILL.md", skillText("y", "C."))
	put(t, st, "/Shared/Team/A/.skills/y/SKILL.md", skillText("y", "A."))
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(got)
	if m["/Shared/Team/A/B/.skills/x"].Shadowed != "" || m["/Shared/Team/.skills/x"].Shadowed != "folder" {
		t.Errorf("deeper folder should win: %+v", got)
	}
	if m["/Shared/Team/A/.skills/y"].Shadowed != "" || m["/Shared/Team/C/.skills/y"].Shadowed != "folder" {
		t.Errorf("the path that sorts first should win at equal depth: %+v", got)
	}
}

func TestAFolderTheActorCannotReadIsInvisible(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/Secret/.skills/s/SKILL.md", skillText("s", "Secret."))
	put(t, st, "/users/bob/.skills/b/SKILL.md", skillText("b", "Bob's."))
	roots = append(roots,
		Root{Layer: LayerFolder, Path: "/Secret", Guest: "/workspace/shared-Secret"},
		Root{Layer: LayerPersonal, Path: "/users/bob/.skills", Guest: "/workspace/other"},
	)
	// A readable skill in the same store proves that Resolve finds things at all.
	put(t, st, "/skills/ok/SKILL.md", skillText("ok", "Readable."))
	got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil || len(got) != 1 || got[0].Path != "/skills/ok" || len(notes) != 0 {
		t.Fatalf("got %+v, notes %v, err %v", got, notes, err)
	}
}

func TestAnInvalidSkillIsListedWithItsReason(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/nofront/SKILL.md", "Just text.\n")
	put(t, st, "/skills/big/SKILL.md", skillText("big", "Big.")+strings.Repeat("y", MaxFileSize))
	put(t, st, "/skills/Bad Name/SKILL.md", skillText("Bad", "Bad."))
	put(t, st, "/skills/assets/logo.txt", "not a skill") // no SKILL.md: not listed at all
	put(t, st, "/skills/fine/SKILL.md", skillText("fine", "Fine."))
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, s := range got {
		reasons[s.Path] = s.Invalid
	}
	want := map[string]string{"/skills/nofront": "no frontmatter", "/skills/big": "too large", "/skills/Bad Name": "bad name", "/skills/fine": ""}
	if fmt.Sprint(reasons) != fmt.Sprint(want) {
		t.Fatalf("reasons %v, want %v", reasons, want)
	}
}

func TestOnlyTwoHundredFilesAreReadNearestFirst(t *testing.T) {
	old := maxSkillFiles
	maxSkillFiles = 3
	t.Cleanup(func() { maxSkillFiles = old })
	st, roots := layers(t)
	for i := 0; i < 3; i++ {
		put(t, st, fmt.Sprintf("/builtin-skills/b%d/SKILL.md", i), skillText(fmt.Sprintf("b%d", i), "Built-in."))
	}
	put(t, st, "/users/alice/.skills/p/SKILL.md", skillText("p", "Personal."))
	put(t, st, "/Shared/Team/.skills/f/SKILL.md", skillText("f", "Folder."))
	got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(notes) != 1 || notes[0] != NoteNotRead {
		t.Fatalf("got %d skills, notes %v", len(got), notes)
	}
	if got[0].Name != "f" || got[1].Name != "p" || got[2].Layer != LayerBuiltin {
		t.Fatalf("the nearest layers should be read first: %+v", got)
	}
}

func TestTheFolderWalkStopsAtItsLimitsWithANote(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/Shared/Team/a/b/c/.skills/deep/SKILL.md", skillText("deep", "Deep."))
	put(t, st, "/Shared/Team/.skills/top/SKILL.md", skillText("top", "Top."))

	t.Run("depth", func(t *testing.T) {
		old := maxWalkDepth
		maxWalkDepth = 2
		t.Cleanup(func() { maxWalkDepth = old })
		got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Name != "top" || len(notes) != 1 || notes[0] != NoteNotSearched {
			t.Fatalf("got %+v, notes %v", got, notes)
		}
	})
	t.Run("folder count", func(t *testing.T) {
		old := maxWalkFolders
		maxWalkFolders = 2
		t.Cleanup(func() { maxWalkFolders = old })
		_, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != 1 || notes[0] != NoteNotSearched {
			t.Fatalf("notes %v", notes)
		}
	})
	t.Run("no limit hit", func(t *testing.T) {
		got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
		if err != nil || len(got) != 2 || len(notes) != 0 {
			t.Fatalf("got %+v, notes %v, err %v", got, notes, err)
		}
	})
}

func TestARootSkipIsNotWalkedAndDoesNotUseTheBudget(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/Shared/Team/.skills/team/SKILL.md", skillText("team", "Team."))
	put(t, st, "/users/alice/Chats/c1/.skills/upload/SKILL.md", skillText("upload", "In a chat folder."))
	for i := 0; i < 5; i++ {
		put(t, st, fmt.Sprintf("/users/alice/Chats/c%d/f/x.txt", i), "x")
	}
	// The home walk comes first, as in a chat, and the budget is small.
	for i := range roots {
		if roots[i].Layer == LayerFolder && roots[i].Path == "/users/alice" {
			roots[i].Skip = []string{"/users/alice/Chats"}
		}
	}
	old := maxWalkFolders
	maxWalkFolders = 4
	t.Cleanup(func() { maxWalkFolders = old })
	got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sk := range got {
		names = append(names, sk.Name)
	}
	if fmt.Sprint(names) != "[team]" || len(notes) != 0 {
		t.Fatalf("skills %v, notes %v", names, notes)
	}
}

func TestTheWalkCountIsLoggedAtDebugLevel(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/Shared/Team/.skills/team/SKILL.md", skillText("team", "Team."))
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if _, _, err := Resolve(context.Background(), st, alice, roots, Options{Logger: log}); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "folders=") {
		t.Fatalf("log %q", out)
	}
}

// failStore fails List for one path, as a broken disk would.
type failStore struct {
	workspace.Store
	path string
}

func (f failStore) List(ctx context.Context, a workspace.Actor, p string) ([]workspace.Node, error) {
	if p == f.path {
		return nil, errors.New("disk on fire")
	}
	return f.Store.List(ctx, a, p)
}

func TestAStoreErrorIsReturned(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/a/SKILL.md", skillText("a", "A."))
	_, _, err := Resolve(context.Background(), failStore{st, "/skills"}, alice, roots, Options{})
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v", err)
	}
	_, _, err = Resolve(context.Background(), failStore{st, "/Shared/Team"}, alice, roots, Options{})
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("walk err = %v", err)
	}
}

func TestOnlySkillFilesAreRead(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/a/SKILL.md", skillText("a", "A."))
	put(t, st, "/skills/a/references/big.md", strings.Repeat("z", 5<<20))
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestADescriptionIsCleanedAndKeptOnTheSkill(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/a/SKILL.md", "---\nname: a\ndescription: \"Use when\\n   filing \x00 it.\"\n---\n")
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil || len(got) != 1 || got[0].Description != "Use when filing it." {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestTheFrontmatterNameCountsNotTheFolderName(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/old-folder/SKILL.md", skillText("real-name", "Renamed."))
	put(t, st, "/builtin-skills/real-name/SKILL.md", skillText("real-name", "Built-in."))
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(got)
	if s := m["/skills/old-folder"]; s.Name != "real-name" || s.Shadowed != "" || s.GuestPath != "/workspace/shared-skills/old-folder/SKILL.md" {
		t.Errorf("shared: %+v", s)
	}
	if b := m["/builtin-skills/real-name"]; b.Shadowed != "shared" {
		t.Errorf("builtin should lose to the renamed skill: %+v", b)
	}
}

func TestOnlyAThousandFoldersAreProbed(t *testing.T) {
	old := maxProbes
	maxProbes = 2
	t.Cleanup(func() { maxProbes = old })
	st, roots := layers(t)
	// Folders without a SKILL.md use up probes too.
	put(t, st, "/skills/a1/notes.txt", "x")
	put(t, st, "/skills/a2/notes.txt", "x")
	put(t, st, "/skills/z/SKILL.md", skillText("z", "Z."))
	got, notes, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || len(notes) != 1 || notes[0] != NoteNotRead {
		t.Fatalf("got %+v, notes %v", got, notes)
	}
}

func TestFolderDepthIsCountedBelowTheWalkedRoot(t *testing.T) {
	st := newStore(t)
	grant(t, st, "alice", "/users/alice", workspace.ModeWrite)
	grant(t, st, "alice", "/Shared", workspace.ModeWrite)
	roots := []Root{
		{Layer: LayerFolder, Path: "/users/alice", Guest: "/workspace/home"},
		{Layer: LayerFolder, Path: "/Shared", Guest: "/workspace/shared"},
	}
	// Both skills sit one folder below their root. The home one is deeper in
	// the workspace tree, but that must not decide: the path does.
	put(t, st, "/users/alice/P/.skills/x/SKILL.md", skillText("x", "Home."))
	put(t, st, "/Shared/P/.skills/x/SKILL.md", skillText("x", "Shared."))
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(got)
	if m["/Shared/P/.skills/x"].Shadowed != "" || m["/users/alice/P/.skills/x"].Shadowed != "folder" {
		t.Errorf("equal relative depth should compare by path: %+v", got)
	}
}

// The store returns ErrInvalid in two odd cases, and Resolve skips both: a
// layer folder that is a file (List) and a SKILL.md that is a folder (Open).
// Every other store error is returned (TestAStoreErrorIsReturned).
func TestAnOddShapeIsSkippedNotFatal(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/dirskill/SKILL.md/inner.txt", "a folder named SKILL.md")
	put(t, st, "/skills/ok/SKILL.md", skillText("ok", "Fine."))
	roots = append(roots, Root{Layer: LayerBuiltin, Path: "/skills/ok/SKILL.md", Guest: "/workspace/odd"})
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil || len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestNestedFolderRootsGiveOneEntry(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/Shared/Team/Sub/.skills/x/SKILL.md", skillText("x", "Nested."))
	roots = append(roots, Root{Layer: LayerFolder, Path: "/Shared/Team/Sub", Guest: "/workspace/sub"})
	got, _, err := Resolve(context.Background(), st, alice, roots, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Shadowed != "" || got[0].GuestPath != "/workspace/sub/.skills/x/SKILL.md" {
		t.Fatalf("got %+v", got)
	}
	// With the roots the other way round the result is the same.
	rev := []Root{roots[len(roots)-1], roots[4]}
	got, _, err = Resolve(context.Background(), st, alice, rev, Options{})
	if err != nil || len(got) != 1 || got[0].Shadowed != "" || got[0].GuestPath != "/workspace/sub/.skills/x/SKILL.md" {
		t.Fatalf("reversed: %+v, err %v", got, err)
	}
}

// growStore reports a small size for SKILL.md but serves more bytes, as a
// file that grew between Open and read.
type growStore struct{ workspace.Store }

func (g growStore) Open(ctx context.Context, a workspace.Actor, p string) (io.ReadSeekCloser, workspace.Node, error) {
	rc, n, err := g.Store.Open(ctx, a, p)
	n.Size = 10
	return rc, n, err
}

func TestAFileThatGrewIsTooLargeAndNotHashed(t *testing.T) {
	st, roots := layers(t)
	put(t, st, "/skills/big/SKILL.md", skillText("big", "Big.")+strings.Repeat("y", MaxFileSize))
	got, _, err := Resolve(context.Background(), growStore{st}, alice, roots, Options{})
	if err != nil || len(got) != 1 || got[0].Invalid != "too large" || got[0].Hash != "" {
		t.Fatalf("got %+v, err %v", got, err)
	}
}
