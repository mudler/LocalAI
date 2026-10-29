package studio_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// pngBytes is the start of a PNG file: enough for the type sniffer.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func newFilesChat(t *testing.T) (*studio.Service, *studiotest.ToolHarness) {
	t.Helper()
	f := newFiles(t)
	return newToolService(t, func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "write" {
			return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/notes/out.md","content":"# out"}`}, {Say: "written"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) {
		d.Files = f
		d.Modalities = func(string) studio.Modalities { return studio.Modalities{Vision: true} }
	})
}

func chatFiles(t *testing.T, svc *studio.Service, user, id string) map[string]studio.ChatFile {
	t.Helper()
	cf, err := svc.ChatFiles(context.Background(), user, id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]studio.ChatFile{}
	for _, f := range cf.Files {
		out[f.Name] = f
	}
	return out
}

// waitKind reads events until one of kind k arrives.
func waitKind(t *testing.T, ch <-chan session.Event, k session.EventKind) {
	t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == k {
				return
			}
		case <-timeout:
			t.Fatalf("no %s event", k)
		}
	}
}

func TestChatFilesListUploadsAndToolWritesAndReachThePrompt(t *testing.T) {
	svc, h := newFilesChat(t)
	ctx := context.Background()
	id := create(t, svc, alice)
	evs, cancel, err := svc.Events(ctx, alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	up := []session.Upload{
		{Name: "pic.png", Body: bytes.NewReader(pngBytes)},
		{Name: "data.csv", Body: strings.NewReader("a,b\n1,2\n")},
	}
	if _, err := svc.SendWithFiles(ctx, alice, id, "look", up); err != nil {
		t.Fatal(err)
	}
	waitKind(t, evs, session.KindFiles)
	idle(t, svc, alice, id)

	files := chatFiles(t, svc, alice, id)
	if f := files["pic.png"]; f.Origin != session.FileFromUser || !f.Media {
		t.Errorf("pic.png = %+v, want an upload sent as media", f)
	}
	if f := files["data.csv"]; f.Origin != session.FileFromUser || f.Media {
		t.Errorf("data.csv = %+v, want an upload that is not media", f)
	}
	prompt := h.TurnPrompt(id)
	if !strings.Contains(prompt, "Files in this conversation") || !strings.Contains(prompt, "\n- data.csv (8 B, text/csv) uploaded by the user, turn 1") {
		t.Fatalf("the turn prompt does not list the uploads:\n%s", prompt)
	}

	sent(t, svc, alice, id, "write")
	waitKind(t, evs, session.KindFiles)
	idle(t, svc, alice, id)
	files = chatFiles(t, svc, alice, id)
	if f := files["out.md"]; f.Origin != session.FileFromTool || f.Changed || f.GuestPath != "/workspace/home/notes/out.md" {
		t.Errorf("out.md = %+v, want a file created by a tool", f)
	}
	sent(t, svc, alice, id, "again")
	idle(t, svc, alice, id)
	if p := h.TurnPrompt(id); !strings.Contains(p, "/workspace/home/notes/\n- out.md (5 B, text/markdown) created by you") {
		t.Errorf("the next turn's prompt does not list the written file:\n%s", p)
	}

	if _, err := svc.ChatFiles(ctx, "bob", id); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("bob lists alice's chat files: %v", err)
	}
}

func TestChatFilesFollowARenameAndTellTheModel(t *testing.T) {
	svc, h := newFilesChat(t)
	ctx := context.Background()
	id := create(t, svc, alice)
	if _, err := svc.SendWithFiles(ctx, alice, id, "first", []session.Upload{{Name: "a.txt", Body: strings.NewReader("a")}}); err != nil {
		t.Fatal(err)
	}
	idle(t, svc, alice, id)
	old := chatFiles(t, svc, alice, id)["a.txt"].GuestPath

	evs, cancel, err := svc.Events(ctx, alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	title := "Quarterly numbers"
	if _, err := svc.Update(ctx, alice, id, studio.UpdateInput{Title: &title}); err != nil {
		t.Fatal(err)
	}
	waitKind(t, evs, session.KindFiles)
	cur := chatFiles(t, svc, alice, id)["a.txt"].GuestPath
	if cur == old || !strings.Contains(cur, "Quarterly numbers/a.txt") {
		t.Fatalf("path after the rename = %q (was %q)", cur, old)
	}
	sent(t, svc, alice, id, "next")
	idle(t, svc, alice, id)
	p := h.TurnPrompt(id)
	if !strings.Contains(p, strings.TrimSuffix(cur, "a.txt")+"\n- a.txt (") || !strings.Contains(p, "was renamed to") {
		t.Fatalf("the prompt after the rename lacks the new path or the note:\n%s", p)
	}
}

func TestChatFilesAreEmptyAndThePromptHasNoBlockWithoutFiles(t *testing.T) {
	svc, h := newFilesChat(t)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hello")
	idle(t, svc, alice, id)
	if files := chatFiles(t, svc, alice, id); len(files) != 0 {
		t.Fatalf("files = %v", files)
	}
	if p := h.TurnPrompt(id); strings.Contains(p, "Files in this conversation") {
		t.Fatalf("a chat without files has a files block:\n%s", p)
	}
}
