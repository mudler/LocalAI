package studio_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
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
