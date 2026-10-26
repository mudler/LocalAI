package files_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// toolBox runs the model's file tools for one user in one chat.
type toolBox struct {
	t     *testing.T
	f     *files.Service
	tools map[string]session.Tool
}

func newToolBox(t *testing.T, f *files.Service, user, chat string) *toolBox {
	t.Helper()
	if err := f.EnsureHome(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	b := &toolBox{t: t, f: f, tools: map[string]session.Tool{}}
	for _, tool := range f.Tools(workspace.Actor{Principal: user, ChatID: chat}) {
		b.tools[tool.Name] = tool
	}
	return b
}

func (b *toolBox) run(name string, args map[string]any) (string, error) {
	b.t.Helper()
	tool, ok := b.tools[name]
	if !ok {
		b.t.Fatalf("no tool %q; have %v", name, len(b.tools))
	}
	raw, _ := json.Marshal(args)
	return tool.Run(context.Background(), raw)
}

func TestTheModelWritesAndReadsFilesInTheUsersHome(t *testing.T) {
	f := newFiles(t)
	b := newToolBox(t, f, "alice", "chat1")
	for _, name := range []string{"list_files", "read_file", "write_file", "move_file", "delete_file", "search_files"} {
		if _, ok := b.tools[name]; !ok {
			t.Fatalf("missing tool %s", name)
		}
	}
	if _, err := b.run("write_file", map[string]any{"path": "/Reports/q1.txt", "content": "line one\nline two\nline three\n"}); err != nil {
		t.Fatal(err)
	}
	// The path the model sees in the sandbox works too.
	got, err := b.run("read_file", map[string]any{"path": "/workspace/home/Reports/q1.txt"})
	if err != nil || !strings.Contains(got, "line two") {
		t.Fatalf("read_file = %q, %v", got, err)
	}
	part, _ := b.run("read_file", map[string]any{"path": "/Reports/q1.txt", "offset": 2, "limit": 1})
	if !strings.Contains(part, "line two") || strings.Contains(part, "line one") || strings.Contains(part, "line three") {
		t.Fatalf("read_file offset 2 limit 1 = %q, want only line two", part)
	}
	list, err := b.run("list_files", map[string]any{"path": "/Reports"})
	if err != nil || !strings.Contains(list, "q1.txt") {
		t.Fatalf("list_files = %q, %v", list, err)
	}
	if _, err := b.run("move_file", map[string]any{"from": "/Reports/q1.txt", "to": "/Archive/q1.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.run("delete_file", map[string]any{"path": "/Archive"}); err != nil {
		t.Fatal(err)
	}
	trash, _ := f.Trash(context.Background(), "alice", 10)
	if len(trash) != 1 || trash[0].Paths[0] != "/Archive" {
		t.Fatalf("trash = %+v, want the folder the model deleted", trash)
	}
}

func TestTheModelsChangesAreAgentChangesOfItsChat(t *testing.T) {
	f := newFiles(t)
	b := newToolBox(t, f, "alice", "chat7")
	_, _ = b.run("write_file", map[string]any{"path": "/a.txt", "content": "x"})
	hist, err := f.Store().History(context.Background(), f.Actor("alice"), workspace.HistoryQuery{Path: "/users/alice", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, cs := range hist {
		if cs.Kind == workspace.ChangeAgent && cs.ChatID == "chat7" {
			found = true
		}
	}
	if !found {
		t.Fatalf("history = %+v, want an agent change set of chat7", hist)
	}
}

func TestTheModelCannotLeaveTheUsersHome(t *testing.T) {
	f := newFiles(t)
	_ = f.EnsureHome(context.Background(), "bob")
	bobBox := newToolBox(t, f, "bob", "c")
	_, _ = bobBox.run("write_file", map[string]any{"path": "/secret.txt", "content": "bob's"})
	b := newToolBox(t, f, "alice", "chat1")
	for _, p := range []string{"/../bob/secret.txt", "/users/bob/secret.txt", "../bob/secret.txt", "/workspace/home/../bob/secret.txt"} {
		if out, err := b.run("read_file", map[string]any{"path": p}); err == nil {
			t.Errorf("read_file(%q) = %q, want an error", p, out)
		}
		if _, err := b.run("write_file", map[string]any{"path": p, "content": "x"}); err == nil {
			// A path inside her own home that happens to look like it is fine; it
			// must not have written into bob's.
			if got, err := bobBox.run("read_file", map[string]any{"path": "/secret.txt"}); err != nil || got != "bob's" {
				t.Errorf("write_file(%q) changed bob's file: %q, %v", p, got, err)
			}
		}
	}
	if out, _ := b.run("search_files", map[string]any{"query": "bob's"}); strings.Contains(out, "secret.txt") {
		t.Errorf("search found bob's file: %q", out)
	}
}

func TestSearchFindsNamesAndWordsInsideFilesAndSkipsWhatIsNotText(t *testing.T) {
	f := newFiles(t)
	b := newToolBox(t, f, "alice", "c")
	_, _ = b.run("write_file", map[string]any{"path": "/Notes/budget-2026.txt", "content": "nothing here"})
	_, _ = b.run("write_file", map[string]any{"path": "/Notes/meeting.txt", "content": "we discussed the Budget for Q3\nand hiring"})
	abs, _ := f.Abs("alice", "/blob.bin")
	_, _ = f.Store().Put(context.Background(), f.Actor("alice"), abs, strings.NewReader("budget\x00\x01\x02"), workspace.PutOptions{})
	big, _ := f.Abs("alice", "/big.txt")
	_, _ = f.Store().Put(context.Background(), f.Actor("alice"), big, strings.NewReader(strings.Repeat("budget ", 300000)), workspace.PutOptions{})

	out, err := b.run("search_files", map[string]any{"query": "budget"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/Notes/budget-2026.txt") {
		t.Errorf("search missed the file named budget: %q", out)
	}
	if !strings.Contains(out, "/Notes/meeting.txt") || !strings.Contains(out, "Budget for Q3") {
		t.Errorf("search missed the word inside meeting.txt: %q", out)
	}
	if strings.Contains(out, "blob.bin") && strings.Contains(out, "budget\x00") {
		t.Errorf("search read a binary file: %q", out)
	}
	if strings.Contains(out, "/big.txt:") {
		t.Errorf("search read a file over the content limit: %q", out)
	}
}

func TestTheModelReadsSharedAndBuiltInSkillsButCannotChangeThem(t *testing.T) {
	f := newFiles(t)
	b := newToolBox(t, f, "alice", "c1")
	adminPut(t, f, "/skills/team/SKILL.md", skillMD("team", "for everyone"))
	adminPut(t, f, "/skills/team/references/style.md", "use short words")
	adminPut(t, f, "/builtin-skills/base/SKILL.md", skillMD("base", "built in"))

	got, err := b.run("read_file", map[string]any{"path": "/workspace/shared-skills/team/SKILL.md"})
	if err != nil || !strings.Contains(got, "Body of team") {
		t.Fatalf("read_file of a shared skill = %q, %v", got, err)
	}
	ref, err := b.run("read_file", map[string]any{"path": "/workspace/shared-skills/team/references/style.md"})
	if err != nil || ref != "use short words" {
		t.Fatalf("read_file of a reference next to a shared skill = %q, %v", ref, err)
	}
	if got, err = b.run("read_file", map[string]any{"path": "/workspace/shared-builtin-skills/base/SKILL.md"}); err != nil || !strings.Contains(got, "Body of base") {
		t.Fatalf("read_file of a built-in skill = %q, %v", got, err)
	}
	list, err := b.run("list_files", map[string]any{"path": "/workspace/shared-skills"})
	if err != nil || !strings.Contains(list, "team/") {
		t.Fatalf("list_files of the shared skills = %q, %v", list, err)
	}

	for name, args := range map[string]map[string]any{
		"write_file":  {"path": "/workspace/shared-skills/team/SKILL.md", "content": "x"},
		"delete_file": {"path": "/workspace/shared-skills/team"},
		"move_file":   {"from": "/workspace/shared-skills/team", "to": "/stolen"},
	} {
		if _, err := b.run(name, args); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s on a shared skill = %v, want a read-only refusal", name, err)
		}
	}
	if got := wstestRead(t, f, "/skills/team/SKILL.md"); !strings.Contains(got, "Body of team") {
		t.Fatalf("the shared skill changed: %q", got)
	}
}

func TestASharedPathCannotLeadOutOfItsLayer(t *testing.T) {
	f := newFiles(t)
	b := newToolBox(t, f, "alice", "c1")
	_ = f.EnsureHome(context.Background(), "bob")
	userPut(t, f, "bob", "/.skills/private/SKILL.md", skillMD("private", "bob only"))
	adminPut(t, f, "/skills/team/SKILL.md", skillMD("team", "for everyone"))
	for _, p := range []string{
		"/workspace/shared-skills/../users/bob/.skills/private/SKILL.md",
		"/workspace/shared-skills/team/../../users/bob/.skills/private/SKILL.md",
		"/workspace/shared-skills/..",
	} {
		if out, err := b.run("read_file", map[string]any{"path": p}); err == nil {
			t.Errorf("read_file(%q) = %q, want an error", p, out)
		}
		if out, err := b.run("list_files", map[string]any{"path": p}); err == nil {
			t.Errorf("list_files(%q) = %q, want an error", p, out)
		}
	}
	// A name that only starts like the guest prefix is an ordinary path in the home.
	if _, err := b.run("read_file", map[string]any{"path": "/workspace/shared-skills-extra/x"}); err == nil {
		t.Error("a path that only looks like the shared prefix was read")
	}
}

func wstestRead(t *testing.T, f *files.Service, abs string) string {
	t.Helper()
	r, _, err := f.Store().Open(context.Background(), workspace.Actor{Principal: files.AdminPrincipal}, abs)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}
