package studio_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

func newFiles(t *testing.T) *files.Service {
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
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return files.New(ws)
}

func newServiceWithFiles(t *testing.T, h *studiotest.GateHarness, f *files.Service) *studio.Service {
	t.Helper()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h, Files: f})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

func TestAChatGivesItsUserAHomeFolderWhenFilesAreOn(t *testing.T) {
	f := newFiles(t)
	h := studiotest.NewGateHarness()
	svc := newServiceWithFiles(t, h, f)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)

	if _, err := f.Store().Stat(context.Background(), workspace.Actor{Principal: alice}, session.HomePath(alice)); err != nil {
		t.Fatalf("the user's home does not exist after a chat: %v", err)
	}
	if _, err := f.Store().Stat(context.Background(), workspace.Actor{Principal: "bob"}, session.HomePath(alice)); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("another user sees the home: %v", err)
	}
}

func TestAChatWorksWithoutFiles(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newServiceWithFiles(t, h, files.Disabled("off for this test"))
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)
	if got := turn(t, svc, alice, id).Last; got != store.TurnDone {
		t.Fatalf("turn ended %q, want done", got)
	}
	if svc.Files().Available() {
		t.Fatal("the service reports files as available")
	}
}

func upload(name, body string) session.Upload {
	return session.Upload{Name: name, Body: strings.NewReader(body)}
}

func TestAnAttachedFileLandsInTheChatFolderAndTheModelGetsItsPath(t *testing.T) {
	f := newFiles(t)
	h := studiotest.NewGateHarness()
	svc := newServiceWithFiles(t, h, f)
	id := create(t, svc, alice)
	res, err := svc.SendWithFiles(context.Background(), alice, id, "summarize this", []session.Upload{upload("notes.txt", "alpha beta")})
	if err != nil || !res.Started {
		t.Fatalf("SendWithFiles = %+v, %v", res, err)
	}
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	sent := h.Sent()[0]
	if !strings.Contains(sent, "summarize this") || !strings.Contains(sent, "/workspace/home/Chats/") || !strings.Contains(sent, "notes.txt") {
		t.Fatalf("the model got %q, want the text and the guest path of notes.txt", sent)
	}
	// The file is in the user's chat folder, as the user.
	kids, err := f.Store().List(context.Background(), workspace.Actor{Principal: alice}, session.ChatsPath(alice))
	if err != nil || len(kids) != 1 {
		t.Fatalf("chat folders = %v, %v, want one", kids, err)
	}
	inner, err := f.Store().List(context.Background(), workspace.Actor{Principal: alice}, kids[0].Path)
	if err != nil || len(inner) != 1 || inner[0].Name != "notes.txt" {
		t.Fatalf("chat folder holds %v, %v, want notes.txt", inner, err)
	}
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)
}

func TestAFileCannotBeAttachedWhileATurnRuns(t *testing.T) {
	f := newFiles(t)
	h := studiotest.NewGateHarness()
	svc := newServiceWithFiles(t, h, f)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "start")
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	_, err := svc.SendWithFiles(context.Background(), alice, id, "and this", []session.Upload{upload("x.txt", "x")})
	if !errors.Is(err, studio.ErrFilesWhileRunning) {
		t.Fatalf("SendWithFiles during a turn = %v, want ErrFilesWhileRunning", err)
	}
	if got := pendingTexts(t, svc, alice, id); len(got) != 0 {
		t.Fatalf("a refused attachment left pending messages %v", got)
	}
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)
}

func TestAFileCannotBeAttachedWhenFilesAreOff(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newServiceWithFiles(t, h, files.Disabled("no data path"))
	id := create(t, svc, alice)
	_, err := svc.SendWithFiles(context.Background(), alice, id, "see", []session.Upload{upload("x.txt", "x")})
	if !errors.Is(err, files.ErrUnavailable) {
		t.Fatalf("SendWithFiles with files off = %v, want ErrUnavailable", err)
	}
	if len(h.Sent()) != 0 {
		t.Fatalf("a turn started: %v", h.Sent())
	}
}

func TestTheModelGetsFileToolsWhenFilesAreOnAndItsSubAgentsDoNot(t *testing.T) {
	f := newFiles(t)
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) { d.Files = f })
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)

	parent := h.ToolNames(id)
	for _, want := range []string{"read_file", "write_file", "list_files", "move_file", "delete_file", "search_files", "spawn_agent"} {
		if !contains(parent, want) {
			t.Errorf("the chat's tools %v lack %s", parent, want)
		}
	}
	kids := childrenOf(t, svc, alice, id)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	for _, name := range h.ToolNames(kids[0].ID) {
		if strings.HasSuffix(name, "_file") || name == "list_files" || name == "search_files" {
			t.Errorf("the sub-agent got the file tool %s", name)
		}
	}
}

func TestTheModelGetsNoFileToolsWhenFilesAreOff(t *testing.T) {
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) { d.Files = files.Disabled("off") })
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	for _, name := range h.ToolNames(id) {
		if strings.HasSuffix(name, "_file") || name == "list_files" || name == "search_files" {
			t.Errorf("a chat with files off got the tool %s", name)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func newMediaService(t *testing.T) (*media.Service, *provider.Provider) {
	t.Helper()
	p := provider.New()
	p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"buy milk and call the dentist"}`))
	}))
	users := func(_ context.Context, id string) (*auth.User, error) {
		return &auth.User{ID: id, Status: auth.StatusActive}, nil
	}
	return media.New(p, users, func(c media.Capability) string { return "any-model" }), p
}

func TestAVoiceNoteBecomesTextInTheMessageTheModelGets(t *testing.T) {
	f := newFiles(t)
	m, p := newMediaService(t)
	h := studiotest.NewGateHarness()
	st, _ := store.NewJSON("")
	svc, err := studio.New(studio.Deps{Store: st, Provider: p, StateDir: t.TempDir(), Harness: h, Files: f, Media: m})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	id := create(t, svc, alice)
	if _, err := svc.SendWithFiles(context.Background(), alice, id, "my day", []session.Upload{upload("memo.m4a", "AUDIO")}); err != nil {
		t.Fatal(err)
	}
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	got := h.Sent()[0]
	if !strings.Contains(got, "my day") || !strings.Contains(got, "buy milk and call the dentist") || !strings.Contains(got, "memo.m4a") {
		t.Fatalf("the model got %q, want the text, the voice note's name and its transcript", got)
	}
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)
}

func TestAnAttachmentWhileATurnRunsIsRefusedBeforeAnyTranscription(t *testing.T) {
	f := newFiles(t)
	m, p := newMediaService(t)
	calls := 0
	p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"text":"x"}`))
	}))
	h := studiotest.NewGateHarness()
	st, _ := store.NewJSON("")
	svc, err := studio.New(studio.Deps{Store: st, Provider: p, StateDir: t.TempDir(), Harness: h, Files: f, Media: m})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	if _, err := svc.SendWithFiles(context.Background(), alice, id, "x", []session.Upload{upload("memo.m4a", "A")}); !errors.Is(err, studio.ErrFilesWhileRunning) {
		t.Fatalf("SendWithFiles = %v, want ErrFilesWhileRunning", err)
	}
	if calls != 0 {
		t.Fatalf("a transcription ran for a refused attachment (%d calls)", calls)
	}
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)
}
