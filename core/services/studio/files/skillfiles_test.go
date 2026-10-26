package files_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func TestAPersonalSkillIsWrittenReadAndDeleted(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteSkill(ctx, "alice", skills.LayerPersonal, "review", "check a change", "Read the diff.\nThen say what you find."); err != nil {
		t.Fatal(err)
	}
	desc, body, err := f.ReadSkill(ctx, "alice", skills.LayerPersonal, "review")
	if err != nil || desc != "check a change" || !strings.Contains(body, "Then say what you find.") {
		t.Fatalf("ReadSkill = %q, %q, %v", desc, body, err)
	}
	// What was written is found by the resolver, so the chat will use it.
	if got := resolve(t, f, "alice")["review"]; got.Description != "check a change" || got.Layer != skills.LayerPersonal {
		t.Fatalf("resolved skill = %+v", got)
	}
	// Writing again replaces it.
	if err := f.WriteSkill(ctx, "alice", skills.LayerPersonal, "review", "check carefully", "New body."); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := f.ReadSkill(ctx, "alice", skills.LayerPersonal, "review"); d != "check carefully" {
		t.Fatalf("after the second write the description is %q", d)
	}
	if err := f.DeleteSkill(ctx, "alice", skills.LayerPersonal, "review"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.ReadSkill(ctx, "alice", skills.LayerPersonal, "review"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("ReadSkill after the delete = %v, want ErrNotFound", err)
	}
	if trash, _ := f.Trash(ctx, "alice", 10); len(trash) != 1 {
		t.Fatalf("trash = %+v, want the deleted skill in it", trash)
	}
}

func TestOnlyTheAdminWritesTheSharedSkills(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	_ = f.EnsureHome(ctx, "alice")
	if err := f.WriteSkill(ctx, "alice", skills.LayerShared, "team", "for all", "Body."); !errors.Is(err, files.ErrNotAdmin) {
		t.Fatalf("a user writing a shared skill = %v, want ErrNotAdmin", err)
	}
	if err := f.WriteSkill(ctx, files.AdminPrincipal, skills.LayerShared, "team", "for all", "Body."); err != nil {
		t.Fatalf("the admin writing a shared skill: %v", err)
	}
	if got := resolve(t, f, "alice")["team"]; got.Layer != skills.LayerShared {
		t.Fatalf("alice resolves %+v, want the shared skill", got)
	}
	if d, _, err := f.ReadSkill(ctx, "alice", skills.LayerShared, "team"); err != nil || d != "for all" {
		t.Fatalf("a user reading a shared skill = %q, %v", d, err)
	}
	if err := f.DeleteSkill(ctx, "alice", skills.LayerShared, "team"); !errors.Is(err, files.ErrNotAdmin) {
		t.Fatalf("a user deleting a shared skill = %v, want ErrNotAdmin", err)
	}
	if err := f.WriteSkill(ctx, files.AdminPrincipal, skills.LayerBuiltin, "x", "d", "b"); err == nil {
		t.Fatal("a built-in skill was written through the API")
	}
}

func TestASkillMustHaveAValidNameAndDescription(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	_ = f.EnsureHome(ctx, "alice")
	for _, name := range []string{"", "Has Space", "../up", "a/b", strings.Repeat("x", 80), "UPPER"} {
		if err := f.WriteSkill(ctx, "alice", skills.LayerPersonal, name, "d", "b"); !errors.Is(err, files.ErrBadSkill) {
			t.Errorf("WriteSkill(%q) = %v, want ErrBadSkill", name, err)
		}
	}
	if err := f.WriteSkill(ctx, "alice", skills.LayerPersonal, "ok", "", "b"); !errors.Is(err, files.ErrBadSkill) {
		t.Errorf("an empty description = %v, want ErrBadSkill", err)
	}
	// A description cannot smuggle in frontmatter lines.
	if err := f.WriteSkill(ctx, "alice", skills.LayerPersonal, "tricky", "fine\nname: other\n---\nextra", "b"); err != nil {
		t.Fatal(err)
	}
	if got := resolve(t, f, "alice"); got["tricky"].Name != "tricky" || got["other"].Name != "" {
		t.Fatalf("a multi-line description changed the skill: %+v", got)
	}
}
