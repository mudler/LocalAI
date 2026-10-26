package studio_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func TestSkillSwitchesAreCheckedAndKeptPerChat(t *testing.T) {
	svc := newService(t, studiotest.NewGateHarness())
	id := create(t, svc, alice)
	ctx := context.Background()

	if err := svc.SetSkillsOff(ctx, alice, id, []string{"review", "notes", "review"}); err != nil {
		t.Fatal(err)
	}
	rec, err := svc.Get(ctx, alice, id)
	if err != nil || len(rec.SkillsOff) != 2 {
		t.Fatalf("SkillsOff = %v, %v, want review and notes once", rec.SkillsOff, err)
	}
	for _, bad := range [][]string{{"Not Valid"}, {"../x"}, {""}} {
		if err := svc.SetSkillsOff(ctx, alice, id, bad); !errors.Is(err, studio.ErrBadSkillName) {
			t.Errorf("SetSkillsOff(%q) = %v, want ErrBadSkillName", bad, err)
		}
	}
	many := make([]string, 201)
	for i := range many {
		many[i] = fmt.Sprintf("skill-%d", i)
	}
	if err := svc.SetSkillsOff(ctx, alice, id, many); !errors.Is(err, studio.ErrBadSkillName) {
		t.Errorf("201 switches = %v, want ErrBadSkillName", err)
	}
	if err := svc.SetSkillsOff(ctx, "bob", id, []string{"x"}); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("another user's SetSkillsOff = %v, want ErrNotFound", err)
	}
	if rec, _ = svc.Get(ctx, alice, id); len(rec.SkillsOff) != 2 {
		t.Fatalf("a refused change altered the switches: %v", rec.SkillsOff)
	}
}

// A chat service with files on and a tool harness that reports the turn prompt.
func newSkillChat(t *testing.T) (*studio.Service, *studiotest.ToolHarness, *files.Service) {
	t.Helper()
	f := newFiles(t)
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) { d.Files = f })
	return svc, h, f
}

func writeSkill(t *testing.T, f *files.Service, who, p, name, desc string) {
	t.Helper()
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody.\n"
	a := workspace.Actor{Principal: files.AdminPrincipal}
	if who != "" {
		if err := f.EnsureHome(context.Background(), who); err != nil {
			t.Fatal(err)
		}
		a = f.Actor(who)
		p, _ = f.Abs(who, p)
	} else if err := f.EnsureShared(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Store().Put(context.Background(), a, p, strings.NewReader(body), workspace.PutOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
}

func TestTheModelIsToldWhichSkillsItHas(t *testing.T) {
	svc, h, f := newSkillChat(t)
	writeSkill(t, f, alice, "/.skills/review/SKILL.md", "review", "check a change before it ships")
	writeSkill(t, f, "", "/skills/review/SKILL.md", "review", "the shared review skill")
	writeSkill(t, f, "", "/skills/house-style/SKILL.md", "house-style", "how we write")
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hello")
	idle(t, svc, alice, id)

	prompt := h.TurnPrompt(id)
	for _, want := range []string{"review", "check a change before it ships", "house-style", "how we write", "/workspace/home/.skills/review"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the skill index lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "the shared review skill") {
		t.Errorf("the shared review skill shows although the personal one is nearer:\n%s", prompt)
	}
}

func TestASwitchedOffSkillLeavesTheIndexAtTheNextTurn(t *testing.T) {
	svc, h, f := newSkillChat(t)
	writeSkill(t, f, alice, "/.skills/review/SKILL.md", "review", "check a change")
	writeSkill(t, f, alice, "/.skills/notes/SKILL.md", "notes", "keep notes")
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "one")
	idle(t, svc, alice, id)
	if p := h.TurnPrompt(id); !strings.Contains(p, "review") || !strings.Contains(p, "notes") {
		t.Fatalf("first turn index:\n%s", p)
	}
	if err := svc.SetSkillsOff(context.Background(), alice, id, []string{"review"}); err != nil {
		t.Fatal(err)
	}
	sent(t, svc, alice, id, "two")
	idle(t, svc, alice, id)
	p := h.TurnPrompt(id)
	if strings.Contains(p, "review") || !strings.Contains(p, "notes") {
		t.Fatalf("after switching review off, the index is:\n%s", p)
	}
}

func TestABrokenSkillIsLeftOutAndTheChatStillWorks(t *testing.T) {
	svc, h, f := newSkillChat(t)
	writeSkill(t, f, alice, "/.skills/good/SKILL.md", "good", "a good one")
	abs, _ := f.Abs(alice, "/.skills/broken/SKILL.md")
	if _, err := f.Store().Put(context.Background(), f.Actor(alice), abs, strings.NewReader("no frontmatter at all"), workspace.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	idle(t, svc, alice, id)
	p := h.TurnPrompt(id)
	if !strings.Contains(p, "good") || strings.Contains(p, "broken") {
		t.Fatalf("index with one broken skill:\n%s", p)
	}
	if got := turn(t, svc, alice, id).Last; got != "done" {
		t.Fatalf("the turn ended %q, want done", got)
	}
}

func TestOnlyTheChatGetsTheIndexNotItsSubAgents(t *testing.T) {
	svc, h, f := newSkillChat(t)
	writeSkill(t, f, alice, "/.skills/review/SKILL.md", "review", "check a change")
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	kids := childrenOf(t, svc, alice, id)
	if len(kids) != 1 {
		t.Fatalf("children = %d", len(kids))
	}
	if h.TurnPrompt(id) == "" || h.TurnPrompt(kids[0].ID) != "" {
		t.Fatalf("chat prompt %q, sub-agent prompt %q: only the chat gets the index", h.TurnPrompt(id), h.TurnPrompt(kids[0].ID))
	}
}

func TestAnotherUsersSkillsNeverReachTheIndex(t *testing.T) {
	svc, h, f := newSkillChat(t)
	writeSkill(t, f, "bob", "/.skills/private/SKILL.md", "private", "bob's secret procedure")
	writeSkill(t, f, alice, "/.skills/mine/SKILL.md", "mine", "alice's own")
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	idle(t, svc, alice, id)
	if p := h.TurnPrompt(id); strings.Contains(p, "private") || strings.Contains(p, "secret") || !strings.Contains(p, "mine") {
		t.Fatalf("alice's index:\n%s", p)
	}
}
