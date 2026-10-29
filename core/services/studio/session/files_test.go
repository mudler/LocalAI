package session_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

const filesAdmin = "admin"

type filesRig struct {
	m     *session.Manager
	ws    workspace.Store
	store session.Store
}

// newFilesRig is a manager with a workspace in which alice and bob have a home
// folder, and a harness whose every turn says "ok".
func newFilesRig(t *testing.T) *filesRig {
	t.Helper()
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := jsonstore.New(filepath.Join(t.TempDir(), "ws"), blobs, jsonstore.Options{Admin: filesAdmin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	admin := workspace.Actor{Principal: filesAdmin}
	for _, p := range []string{"alice", "bob"} {
		if _, err := ws.Mkdir(context.Background(), admin, session.ChatsPath(p)); err != nil {
			t.Fatal(err)
		}
		if err := ws.Grant(context.Background(), admin, p, session.HomePath(p), workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
	}
	turns := make([][]sessiontest.Step, 20)
	for i := range turns {
		turns[i] = []sessiontest.Step{{Say: "ok"}}
	}
	st := sessiontest.NewMemoryStore()
	m, err := session.New(session.Options{Store: st, Workspace: ws, Harness: &sessiontest.FakeHarness{Turns: turns}, StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	return &filesRig{m: m, ws: ws, store: st}
}

func (r *filesRig) open(t *testing.T, p, id string) *session.Chat {
	t.Helper()
	c, err := r.m.Open(context.Background(), workspace.Actor{Principal: p, ChatID: id}, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// turn sends a message and waits for its turn_end. It returns the kinds of the
// events of the turn.
func turn(t *testing.T, c *session.Chat, msg session.Message) []session.EventKind {
	t.Helper()
	if err := c.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	var kinds []session.EventKind
	timeout := time.After(20 * time.Second)
	for {
		select {
		case e := <-c.Events():
			kinds = append(kinds, e.Kind)
			if e.Kind == session.KindTurnEnd {
				return kinds
			}
		case <-timeout:
			t.Fatal("timed out waiting for turn_end")
		}
	}
}

func upload(names ...string) []session.Upload {
	var out []session.Upload
	for _, n := range names {
		out = append(out, session.Upload{Name: n, Body: strings.NewReader("content of " + n)})
	}
	return out
}

func put(t *testing.T, ws workspace.Store, a workspace.Actor, p, body string) workspace.Node {
	t.Helper()
	n, err := ws.Put(context.Background(), a, p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func byName(cf session.ChatFiles) map[string]session.ChatFile {
	out := map[string]session.ChatFile{}
	for _, f := range cf.Files {
		out[f.Name] = f
	}
	return out
}

func TestChatFilesTellUploadsModelAndToolWritesApart(t *testing.T) {
	r := newFilesRig(t)
	ctx := context.Background()
	alice := workspace.Actor{Principal: "alice"}
	chatActor := workspace.Actor{Principal: "alice", ChatID: "c1"}
	home := session.HomePath("alice")
	put(t, r.ws, alice, home+"/old.txt", "there before")

	c := r.open(t, "alice", "c1")
	kinds := turn(t, c, session.Message{Text: "read these", Files: upload("q1.csv", "photo.png")})
	if !containsKind(kinds, session.KindFiles) {
		t.Errorf("an upload emits no files event: %v", kinds)
	}

	// What a sandbox checkpoint commits is the model's work; the transcript
	// names its revision.
	report := put(t, r.ws, chatActor, home+"/report.md", "# report")
	if _, err := r.store.AppendEntry(ctx, "c1", session.Entry{Turn: 2, Role: session.RoleCheckpoint, Rev: report.Rev}); err != nil {
		t.Fatal(err)
	}
	// A host tool's change has no checkpoint.
	put(t, r.ws, chatActor, home+"/notes/tool.txt", "from a tool")
	put(t, r.ws, chatActor, home+"/old.txt", "changed by the chat")
	// A file the chat wrote and then deleted is not listed.
	put(t, r.ws, chatActor, home+"/gone.txt", "x")
	if err := r.ws.Delete(ctx, chatActor, home+"/gone.txt"); err != nil {
		t.Fatal(err)
	}
	// A change of another chat is not this chat's.
	put(t, r.ws, workspace.Actor{Principal: "alice", ChatID: "other"}, home+"/other.txt", "x")

	cf, err := r.m.Files(ctx, "alice", "c1")
	if err != nil {
		t.Fatal(err)
	}
	got := byName(cf)
	want := map[string]struct {
		origin  session.FileOrigin
		changed bool
		turn    int
	}{
		"q1.csv":    {session.FileFromUser, false, 1},
		"photo.png": {session.FileFromUser, false, 1},
		"report.md": {session.FileFromModel, false, 2},
		// Made after the entry of turn 2, so they count as turn 2.
		"tool.txt": {session.FileFromTool, false, 2},
		"old.txt":  {session.FileFromTool, true, 2},
	}
	if len(got) != len(want) {
		t.Fatalf("files = %+v, want %d files", cf.Files, len(want))
	}
	for name, w := range want {
		f, ok := got[name]
		if !ok {
			t.Errorf("%s is missing", name)
			continue
		}
		if f.Origin != w.origin || f.Changed != w.changed || f.Turn != w.turn {
			t.Errorf("%s = origin %q changed %v turn %d, want %q %v %d", name, f.Origin, f.Changed, f.Turn, w.origin, w.changed, w.turn)
		}
	}
	if f := got["q1.csv"]; !strings.HasPrefix(f.GuestPath, session.GuestRoot+"/home/Chats/") || f.Mime != "text/csv" || f.Size == 0 {
		t.Errorf("q1.csv = %+v", f)
	}
	if got["photo.png"].Mime != "image/png" {
		t.Errorf("photo.png mime = %q", got["photo.png"].Mime)
	}
}

func TestChatFilesFollowARenameOfTheChatFolder(t *testing.T) {
	r := newFilesRig(t)
	ctx := context.Background()
	c := r.open(t, "alice", "c1")
	turn(t, c, session.Message{Text: "first", Files: upload("a.txt")})
	before, err := r.m.Files(ctx, "alice", "c1")
	if err != nil || len(before.Files) != 1 {
		t.Fatalf("before the rename: %+v, %v", before, err)
	}
	oldPath := before.Files[0].GuestPath

	if err := c.SetTitle(ctx, "Budget review"); err != nil {
		t.Fatal(err)
	}
	after, err := r.m.Files(ctx, "alice", "c1")
	if err != nil || len(after.Files) != 1 {
		t.Fatalf("after the rename: %+v, %v", after, err)
	}
	f := after.Files[0]
	if f.GuestPath == oldPath || !strings.Contains(f.GuestPath, "Budget review/a.txt") {
		t.Fatalf("path after the rename = %q (was %q)", f.GuestPath, oldPath)
	}
	if f.Origin != session.FileFromUser || f.Turn != 1 {
		t.Errorf("the upload lost its origin or turn: %+v", f)
	}
	if _, err := r.ws.Stat(ctx, workspace.Actor{Principal: "alice"}, f.Path); err != nil {
		t.Errorf("the listed path does not exist: %v", err)
	}
	oldFolder := oldPath[:strings.LastIndex(oldPath, "/")+1]
	if len(after.Stale) != 1 || after.Stale[0] != oldFolder {
		t.Fatalf("stale = %v, want [%s]", after.Stale, oldFolder)
	}
	prompt := session.FilesPrompt(after)
	if !strings.Contains(prompt, after.Folder+"\n- a.txt (") {
		t.Errorf("the prompt lacks the current path:\n%s", prompt)
	}
	if !strings.Contains(prompt, "renamed to "+after.Folder) || !strings.Contains(prompt, oldFolder) {
		t.Errorf("the prompt does not say the folder moved:\n%s", prompt)
	}
	if strings.Contains(strings.Split(prompt, "Earlier messages")[0], oldFolder) {
		t.Errorf("the file list still names the old folder:\n%s", prompt)
	}
}

func TestChatFilesOfAnotherUserAreNotFound(t *testing.T) {
	r := newFilesRig(t)
	c := r.open(t, "alice", "c1")
	turn(t, c, session.Message{Text: "x", Files: upload("secret.txt")})
	if _, err := r.m.Files(context.Background(), "bob", "c1"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("bob reads alice's chat files: %v", err)
	}
}

func TestFilesPromptIsEmptyWithoutFilesAndCappedWithMany(t *testing.T) {
	r := newFilesRig(t)
	ctx := context.Background()
	c := r.open(t, "alice", "c1")
	turn(t, c, session.Message{Text: "no files"})
	cf, err := r.m.Files(ctx, "alice", "c1")
	if err != nil || len(cf.Files) != 0 || session.FilesPrompt(cf) != "" {
		t.Fatalf("a chat without files: %+v, %v, prompt %q", cf, err, session.FilesPrompt(cf))
	}

	names := make([]string, 60)
	for i := range names {
		names[i] = fmt.Sprintf("f%02d.txt", i)
	}
	turn(t, c, session.Message{Text: "many", Files: upload(names...)})
	cf, err = r.m.Files(ctx, "alice", "c1")
	if err != nil || len(cf.Files) != 60 {
		t.Fatalf("index of 60 files: %d, %v", len(cf.Files), err)
	}
	prompt := session.FilesPrompt(cf)
	lines := strings.Split(prompt, "\n")
	if got := len(lines); got != 1+1+session.MaxPromptFiles+1 {
		t.Fatalf("prompt has %d lines, want a head, the folder, %d files and a count:\n%s", got, session.MaxPromptFiles, prompt)
	}
	if !strings.Contains(lines[len(lines)-1], "and 10 older files") {
		t.Errorf("last line = %q", lines[len(lines)-1])
	}
	if strings.Contains(prompt, "f00.txt") || !strings.Contains(prompt, "\n- f59.txt (") || strings.Count(prompt, cf.Folder) != 2 {
		t.Errorf("the cap keeps the oldest files, not the newest:\n%s", prompt)
	}
}

func containsKind(kinds []session.EventKind, k session.EventKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}
