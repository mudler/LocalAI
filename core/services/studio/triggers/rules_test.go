package triggers_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

func newWorkspace(t *testing.T) workspace.Store {
	t.Helper()
	s := openWorkspace(t, nil)
	return s
}

func grant(t *testing.T, s workspace.Store, principal, p string, m workspace.Mode) {
	t.Helper()
	if _, err := s.Mkdir(wstest.Ctx(), wstest.AdminActor, p); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(wstest.Ctx(), wstest.AdminActor, principal, p, m); err != nil {
		t.Fatal(err)
	}
}

func TestRulesAreReadOutermostFirst(t *testing.T) {
	s := newWorkspace(t)
	grant(t, s, wstest.Alice.Principal, "/users/alice", workspace.ModeRead)
	wstest.MustPut(t, s, wstest.AdminActor, "/users/alice/AGENTS.md", "Answer in Italian.")
	wstest.MustPut(t, s, wstest.AdminActor, "/users/alice/Contracts/AGENTS.md", "Never change a signed file.\n")
	wstest.MustPut(t, s, wstest.AdminActor, "/users/alice/Contracts/2026/note.txt", "x")
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/alice/Contracts/2026")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Folder rules: /users/alice\n\nAnswer in Italian.\n\n# Folder rules: /users/alice/Contracts\n\nNever change a signed file."
	if got != want {
		t.Fatalf("Rules =\n%q\nwant\n%q", got, want)
	}
}

func TestRulesLeaveOutFilesTheActorCannotReadOrThatAreMissing(t *testing.T) {
	s := newWorkspace(t)
	wstest.MustPut(t, s, wstest.AdminActor, "/AGENTS.md", "root rules, private")
	wstest.MustPut(t, s, wstest.AdminActor, "/users/alice/AGENTS.md", "alice rules")
	wstest.MustPut(t, s, wstest.AdminActor, "/users/bob/AGENTS.md", "bob rules")
	if _, err := s.Mkdir(wstest.Ctx(), wstest.AdminActor, "/users/alice/Inbox/AGENTS.md"); err != nil {
		t.Fatal(err) // a folder named like the rules file
	}
	if err := s.Grant(wstest.Ctx(), wstest.AdminActor, wstest.Alice.Principal, "/users/alice", workspace.ModeRead); err != nil {
		t.Fatal(err)
	}
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/alice/Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if got != "# Folder rules: /users/alice\n\nalice rules" {
		t.Fatalf("Rules = %q", got)
	}
	none, err := triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/bob")
	if err != nil || none != "" {
		t.Fatalf("a folder of another user: %q, %v", none, err)
	}
	if none, err := triggers.Rules(wstest.Ctx(), s, wstest.Alice, "/users/alice/nothing/here"); err != nil || !strings.Contains(none, "alice rules") {
		t.Fatalf("a folder that does not exist still has the rules above it: %q, %v", none, err)
	}
}

func TestRulesOfTheTopFolder(t *testing.T) {
	s := newWorkspace(t)
	wstest.MustPut(t, s, wstest.AdminActor, "/AGENTS.md", "top")
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/")
	if err != nil || got != "# Folder rules: /\n\ntop" {
		t.Fatalf("Rules(/) = %q, %v", got, err)
	}
}

func TestRulesRefuseABadPath(t *testing.T) {
	s := newWorkspace(t)
	for _, p := range []string{"", "users", "/a/../b", "/a//b", "/a/"} {
		if _, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, p); err == nil {
			t.Errorf("Rules(%q) did not fail", p)
		}
	}
}

func TestRulesAreCutAtTheLimitKeepingTheInnermost(t *testing.T) {
	s := newWorkspace(t)
	big := strings.Repeat("o", 20<<10)
	wstest.MustPut(t, s, wstest.AdminActor, "/a/AGENTS.md", big)
	wstest.MustPut(t, s, wstest.AdminActor, "/a/b/AGENTS.md", strings.Repeat("m", 10<<10))
	wstest.MustPut(t, s, wstest.AdminActor, "/a/b/c/AGENTS.md", strings.Repeat("i", 10<<10))
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b/c")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "were cut to fit 32 KiB") {
		t.Fatal("no note about the cut")
	}
	if strings.Contains(got, "# Folder rules: /a\n") || strings.Contains(got, "ooo") {
		t.Fatal("the outer rules were kept")
	}
	if !strings.Contains(got, "# Folder rules: /a/b\n") || !strings.Contains(got, "# Folder rules: /a/b/c\n") {
		t.Fatal("the inner rules were dropped")
	}
	if len(got) > triggers.MaxRulesBytes+200 {
		t.Fatalf("%d bytes", len(got))
	}
}

func TestOneRulesFileOverTheLimitIsCutAtItsEnd(t *testing.T) {
	s := newWorkspace(t)
	// 3-byte characters: a cut inside one must not leave half of it.
	wstest.MustPut(t, s, wstest.AdminActor, "/a/AGENTS.md", strings.Repeat("€", 20000))
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "were cut to fit 32 KiB") || !strings.Contains(got, "€€€") {
		t.Fatalf("the cut of one long file: %.80q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatal("a character was cut in half")
	}
	if len(got) > triggers.MaxRulesBytes+200 {
		t.Fatalf("%d bytes", len(got))
	}
}

func TestRulesKeepInvalidBytesOut(t *testing.T) {
	s := newWorkspace(t)
	wstest.MustPut(t, s, wstest.AdminActor, "/a/AGENTS.md", "ok \xff\xfe end")
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a")
	if err != nil || !strings.HasSuffix(got, "end") || !utf8.ValidString(got) {
		t.Fatalf("Rules = %q, %v", got, err)
	}
}

func TestAnEmptyRulesFileIsLeftOut(t *testing.T) {
	s := newWorkspace(t)
	wstest.MustPut(t, s, wstest.AdminActor, "/a/AGENTS.md", "outer rules")
	wstest.MustPut(t, s, wstest.AdminActor, "/a/b/AGENTS.md", "")
	wstest.MustPut(t, s, wstest.AdminActor, "/a/b/c/AGENTS.md", " \n\t\n")
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b/c")
	if err != nil || got != "# Folder rules: /a\n\nouter rules" {
		t.Fatalf("Rules = %q, %v", got, err)
	}
	wstest.MustPut(t, s, wstest.AdminActor, "/a/AGENTS.md", "\n")
	got, err = triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b/c")
	if err != nil || got != "" {
		t.Fatalf("only empty files: %q, %v", got, err)
	}
}

func TestAnInnermostFileOverTheLimitIsCutWhenAnOuterOneIsSmall(t *testing.T) {
	s := newWorkspace(t)
	wstest.MustPut(t, s, wstest.AdminActor, "/a/AGENTS.md", "small outer rules")
	wstest.MustPut(t, s, wstest.AdminActor, "/a/b/AGENTS.md", strings.Repeat("i", triggers.MaxRulesBytes+500))
	got, err := triggers.Rules(wstest.Ctx(), s, wstest.AdminActor, "/a/b")
	if err != nil {
		t.Fatal(err)
	}
	note := fmt.Sprintf("were cut to fit %d KiB", triggers.MaxRulesBytes>>10)
	if !strings.Contains(got, note) || !strings.Contains(got, "# Folder rules: /a/b\n") || !strings.Contains(got, "iii") {
		t.Fatalf("the cut of the innermost file: %.200q", got)
	}
	if strings.Contains(got, "small outer rules") {
		t.Fatal("the outer file was kept next to a cut innermost file")
	}
	body := got[strings.Index(got, "# Folder rules: /a/b"):]
	if len(body) > triggers.MaxRulesBytes {
		t.Fatalf("%d bytes of rules, the limit is %d", len(body), triggers.MaxRulesBytes)
	}
}
