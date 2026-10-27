package files_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func skillMD(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody of " + name + ".\n"
}

func adminPut(t *testing.T, f *files.Service, p, content string) {
	t.Helper()
	_, err := f.Store().Put(context.Background(), workspace.Actor{Principal: files.AdminPrincipal}, p, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("admin put %s: %v", p, err)
	}
}

func userPut(t *testing.T, f *files.Service, user, rel, content string) {
	t.Helper()
	abs, _ := f.Abs(user, rel)
	if _, err := f.Store().Put(context.Background(), f.Actor(user), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true}); err != nil {
		t.Fatalf("%s put %s: %v", user, rel, err)
	}
}

func resolve(t *testing.T, f *files.Service, user string) map[string]skills.Skill {
	t.Helper()
	list, _, err := skills.Resolve(context.Background(), f.Store(), f.Actor(user), f.SkillRoots(user), skills.Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]skills.Skill{}
	for _, s := range list {
		if s.Shadowed == "" {
			out[s.Name] = s
		}
	}
	return out
}

func TestAUserSeesTheSkillsOfAllFourLayersAndTheNearestWins(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	adminPut(t, f, "/builtin-skills/review/SKILL.md", skillMD("review", "built-in review"))
	adminPut(t, f, "/builtin-skills/pdfs/SKILL.md", skillMD("pdfs", "built-in pdfs"))
	adminPut(t, f, "/skills/review/SKILL.md", skillMD("review", "shared review"))
	adminPut(t, f, "/skills/house-style/SKILL.md", skillMD("house-style", "shared style"))
	userPut(t, f, "alice", "/.skills/notes/SKILL.md", skillMD("notes", "personal notes"))
	userPut(t, f, "alice", "/Projects/Atlas/.skills/atlas/SKILL.md", skillMD("atlas", "folder skill"))
	userPut(t, f, "alice", "/.skills/pdfs/SKILL.md", skillMD("pdfs", "personal pdfs"))

	got := resolve(t, f, "alice")
	want := map[string]skills.Layer{
		"review": skills.LayerShared, "house-style": skills.LayerShared, "notes": skills.LayerPersonal,
		"atlas": skills.LayerFolder, "pdfs": skills.LayerPersonal,
	}
	if len(got) != len(want) {
		t.Fatalf("skills = %v, want %v", got, want)
	}
	for name, layer := range want {
		if got[name].Layer != layer {
			t.Errorf("skill %s comes from layer %q, want %q (nearest wins)", name, got[name].Layer, layer)
		}
	}
	if g := got["notes"].GuestPath; !strings.HasPrefix(g, session.GuestRoot+"/"+session.GuestHome+"/.skills/notes") {
		t.Errorf("a personal skill's guest path is %q, want one under /workspace/home/.skills", g)
	}
	if g := got["house-style"].GuestPath; !strings.HasPrefix(g, session.GuestRoot+"/shared-skills/") {
		t.Errorf("a shared skill's guest path is %q, want one under /workspace/shared-skills", g)
	}
}

func TestPersonalSkillsStayPersonalAndSharedOnesAreReadOnly(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	for _, u := range []string{"alice", "bob"} {
		if err := f.EnsureHome(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	userPut(t, f, "alice", "/.skills/secret/SKILL.md", skillMD("secret", "alice only"))
	adminPut(t, f, "/skills/team/SKILL.md", skillMD("team", "for everyone"))
	if _, ok := resolve(t, f, "bob")["secret"]; ok {
		t.Fatal("bob sees alice's personal skill")
	}
	if _, ok := resolve(t, f, "bob")["team"]; !ok {
		t.Fatal("bob does not see the shared skill")
	}
	for _, p := range []string{"/skills/evil/SKILL.md", "/builtin-skills/evil/SKILL.md", "/skills/team/SKILL.md"} {
		_, err := f.Store().Put(ctx, f.Actor("bob"), p, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
		if err == nil {
			t.Errorf("bob wrote %s", p)
		} else if !errors.Is(err, workspace.ErrReadOnly) && !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("bob writing %s = %v, want a refusal", p, err)
		}
	}
}

func TestBuiltInSkillsAreCopiedOnceAndUpdatedWhenTheyChange(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	tree := fstest.MapFS{"summarize/SKILL.md": {Data: []byte(skillMD("summarize", "v1"))}, "summarize/references/tips.md": {Data: []byte("tips")}}
	if err := f.SyncBuiltin(ctx, tree); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, f, "alice")["summarize"]; got.Description != "v1" || got.Layer != skills.LayerBuiltin {
		t.Fatalf("built-in skill = %+v", got)
	}
	before, _ := f.Store().History(ctx, workspace.Actor{Principal: files.AdminPrincipal}, workspace.HistoryQuery{Limit: 1000})
	if err := f.SyncBuiltin(ctx, tree); err != nil {
		t.Fatal(err)
	}
	after, _ := f.Store().History(ctx, workspace.Actor{Principal: files.AdminPrincipal}, workspace.HistoryQuery{Limit: 1000})
	if len(after) != len(before) {
		t.Fatalf("a second sync of the same tree made %d changes", len(after)-len(before))
	}
	tree["summarize/SKILL.md"] = &fstest.MapFile{Data: []byte(skillMD("summarize", "v2"))}
	if err := f.SyncBuiltin(ctx, tree); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, f, "alice")["summarize"]; got.Description != "v2" {
		t.Fatalf("after the tree changed, built-in skill = %+v, want v2", got)
	}
}

func TestABuiltInSkillThatLeavesTheTreeLeavesTheBuiltInLayer(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	tree := fstest.MapFS{
		"keep/SKILL.md":            {Data: []byte(skillMD("keep", "stays"))},
		"gone/SKILL.md":            {Data: []byte(skillMD("gone", "removed later"))},
		"gone/references/notes.md": {Data: []byte("notes")},
		"keep/references/a.md":     {Data: []byte("a")},
	}
	if err := f.SyncBuiltin(ctx, tree); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolve(t, f, "alice")["gone"]; !ok {
		t.Fatal("the skill is not there before it leaves the tree")
	}
	delete(tree, "gone/SKILL.md")
	delete(tree, "gone/references/notes.md")
	delete(tree, "keep/references/a.md")
	if err := f.SyncBuiltin(ctx, tree); err != nil {
		t.Fatal(err)
	}
	got := resolve(t, f, "alice")
	if _, ok := got["gone"]; ok {
		t.Fatal("a skill that left the tree is still in the built-in layer")
	}
	if got["keep"].Description != "stays" {
		t.Fatalf("keep = %+v", got["keep"])
	}
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	for _, p := range []string{files.BuiltinSkillsPath + "/gone", files.BuiltinSkillsPath + "/keep/references/a.md"} {
		if _, err := f.Store().Stat(ctx, admin, p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	if _, err := f.Store().Stat(ctx, admin, files.BuiltinSkillsPath+"/keep/SKILL.md"); err != nil {
		t.Errorf("keep/SKILL.md: %v", err)
	}
}
