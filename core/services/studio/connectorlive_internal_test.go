package studio

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

func newLive(mode string) *liveReply {
	return &liveReply{mode: mode, v: hostmsg.In("en")}
}

func think(s string) session.Event { return session.Event{Kind: session.KindReasoning, Text: s} }
func say(s string) session.Event   { return session.Event{Kind: session.KindText, Text: s} }
func tool(n string) session.Event {
	return session.Event{Kind: session.KindToolStart, Tool: &session.ToolEvent{Name: n}}
}

func TestReasoningModeShowsTheThinkingLabelled(t *testing.T) {
	l := newLive(connectors.ReplyReasoning)
	if got := l.view(); got != "Working… ▉" {
		t.Fatalf("before anything: %q", got)
	}
	l.observe(think("checking "))
	l.observe(think("the notes"))
	if got, want := l.view(), "Thinking:\nchecking the notes ▉"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReasoningModeSwitchesToTheAnswerAndKeepsIt(t *testing.T) {
	l := newLive(connectors.ReplyReasoning)
	l.observe(think("hmm"))
	l.observe(say("The answer"))
	if got := l.view(); got != "The answer ▉" {
		t.Fatalf("got %q", got)
	}
	l.observe(think("more thoughts"))
	if got := l.view(); got != "The answer ▉" {
		t.Fatalf("late reasoning replaced the answer: %q", got)
	}
}

func TestReasoningModeToolLineAndNextThought(t *testing.T) {
	l := newLive(connectors.ReplyReasoning)
	l.observe(think("first thought"))
	l.observe(tool("search"))
	if got := l.view(); got != "Using search… ▉" {
		t.Fatalf("tool line: %q", got)
	}
	l.observe(think("second thought"))
	if got, want := l.view(), "Thinking:\nsecond thought ▉"; got != want {
		t.Fatalf("a new thought replaces the old one: got %q", got)
	}
	l.observe(say("done"))
	if got := l.view(); got != "done ▉" {
		t.Fatalf("got %q", got)
	}
}

func TestReasoningModeCapsThePreviewWithTheCursor(t *testing.T) {
	l := newLive(connectors.ReplyReasoning)
	l.observe(think(strings.Repeat("a", maxPreviewRunes) + "END"))
	v := l.view()
	if n := utf8.RuneCountInString(v); n > maxPreviewRunes {
		t.Fatalf("preview is %d runes, the cap is %d", n, maxPreviewRunes)
	}
	if !strings.HasPrefix(v, "Thinking:\n") || !strings.HasSuffix(v, "END"+cursorMark) {
		t.Fatalf("the preview must be the label and the tail: %q...%q", v[:12], v[len(v)-8:])
	}
}

func TestOtherModesNeverShowTheReasoning(t *testing.T) {
	for _, mode := range []string{connectors.ReplyOff, connectors.ReplyProgress, connectors.ReplyStream} {
		l := newLive(mode)
		l.observe(think("secret thought"))
		if v := l.view(); strings.Contains(v, "secret") || strings.Contains(v, "Thinking") {
			t.Errorf("%s shows the reasoning: %q", mode, v)
		}
		if l.thought.Len() != 0 {
			t.Errorf("%s keeps the reasoning", mode)
		}
	}
	if got := newLive(connectors.ReplyProgress).view(); got != "Working…" {
		t.Errorf("progress view changed: %q", got)
	}
}

func TestReasoningModeIsValidAndNeverTheDefault(t *testing.T) {
	if (connectors.Binding{}).ReplyMode() != connectors.ReplyProgress {
		t.Fatal("the default reply mode changed")
	}
	if got := (connectors.Binding{Reply: connectors.ReplyReasoning}).ReplyMode(); got != connectors.ReplyReasoning {
		t.Fatalf("got %q", got)
	}
}
