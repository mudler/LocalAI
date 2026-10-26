package media_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

const png = "\x89PNG\r\n\x1a\nrest-of-the-image"

type call struct {
	path   string
	user   string
	json   map[string]any
	fields map[string]string
	file   string
}

// fixture is a chat service with a stub model server behind the provider.
type fixture struct {
	t     *testing.T
	f     *files.Service
	svc   *media.Service
	mu    sync.Mutex
	calls []call
	reply func(path string) (int, string)
}

func newFixture(t *testing.T, vision, transcription string) *fixture {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, _ := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	fx := &fixture{t: t, f: files.New(ws)}
	fx.reply = func(path string) (int, string) {
		if strings.HasSuffix(path, "/chat/completions") {
			return 200, `{"choices":[{"message":{"role":"assistant","content":"a red square"}}]}`
		}
		return 200, `{"text":"hello from the recording"}`
	}
	p := provider.New()
	p.Transport().SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{path: r.URL.Path}
		if u, ok := auth.InternalUser(r.Context()); ok {
			c.user = u.ID
		}
		ct, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		switch {
		case ct == "application/json":
			_ = json.NewDecoder(r.Body).Decode(&c.json)
		case strings.HasPrefix(ct, "multipart/"):
			mr := multipart.NewReader(r.Body, params["boundary"])
			c.fields = map[string]string{}
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(part)
				if part.FileName() != "" {
					c.file = part.FileName() + ":" + string(b)
				} else {
					c.fields[part.FormName()] = string(b)
				}
			}
		}
		fx.mu.Lock()
		fx.calls = append(fx.calls, c)
		fx.mu.Unlock()
		status, body := fx.reply(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	users := func(_ context.Context, id string) (*auth.User, error) {
		return &auth.User{ID: id, Status: auth.StatusActive}, nil
	}
	fx.svc = media.New(p, users, func(c media.Capability) string {
		if c == media.Vision {
			return vision
		}
		return transcription
	})
	if err := fx.f.EnsureHome(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	return fx
}

func (fx *fixture) put(rel, content string) {
	fx.t.Helper()
	abs, _ := fx.f.Abs("alice", rel)
	if _, err := fx.f.Store().Put(context.Background(), fx.f.Actor("alice"), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true}); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *fixture) tool(name string) session.Tool {
	for _, tl := range fx.svc.Tools(fx.f, workspace.Actor{Principal: "alice", ChatID: "c1"}) {
		if tl.Name == name {
			return tl
		}
	}
	fx.t.Fatalf("no tool %s", name)
	return session.Tool{}
}

func run(t *testing.T, tl session.Tool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return tl.Run(context.Background(), raw)
}

func TestDescribeImageSendsTheImageToTheVisionModelAsTheUser(t *testing.T) {
	fx := newFixture(t, "vl-model", "")
	fx.put("/Pictures/red.png", png)
	out, err := run(t, fx.tool("describe_image"), map[string]any{"path": "/Pictures/red.png", "question": "what colour?"})
	if err != nil || out != "a red square" {
		t.Fatalf("describe_image = %q, %v", out, err)
	}
	if len(fx.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fx.calls))
	}
	c := fx.calls[0]
	if c.path != "/v1/chat/completions" || c.user != "alice" || c.json["model"] != "vl-model" {
		t.Fatalf("call = %+v, want a chat completion for vl-model as alice", c)
	}
	body, _ := json.Marshal(c.json)
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(png))
	if !strings.Contains(string(body), want) || !strings.Contains(string(body), "what colour?") {
		t.Fatalf("request %s lacks the image data URI or the question", body)
	}
}

func TestTranscribeAudioSendsTheFileToTheTranscriptionModel(t *testing.T) {
	fx := newFixture(t, "", "whisper")
	fx.put("/Voice/note.wav", "RIFFdata")
	out, err := run(t, fx.tool("transcribe_audio"), map[string]any{"path": "/Voice/note.wav"})
	if err != nil || out != "hello from the recording" {
		t.Fatalf("transcribe_audio = %q, %v", out, err)
	}
	c := fx.calls[0]
	if c.path != "/v1/audio/transcriptions" || c.fields["model"] != "whisper" || c.file != "note.wav:RIFFdata" || c.user != "alice" {
		t.Fatalf("call = %+v", c)
	}
}

func TestWithoutAModelTheToolSaysWhatToInstall(t *testing.T) {
	fx := newFixture(t, "", "")
	fx.put("/a.png", png)
	fx.put("/a.wav", "RIFF")
	for name, p := range map[string]string{"describe_image": "/a.png", "transcribe_audio": "/a.wav"} {
		_, err := run(t, fx.tool(name), map[string]any{"path": p})
		if err == nil || !strings.Contains(err.Error(), "Install") {
			t.Errorf("%s with no model = %v, want a hint to install one", name, err)
		}
	}
	if len(fx.calls) != 0 {
		t.Fatalf("a request went out with no model: %+v", fx.calls)
	}
}

func TestTheToolsRefuseWhatIsNotTheRightKindOfFile(t *testing.T) {
	fx := newFixture(t, "vl", "whisper")
	fx.put("/notes.txt", "just words")
	if _, err := run(t, fx.tool("describe_image"), map[string]any{"path": "/notes.txt"}); err == nil || !strings.Contains(err.Error(), "not an image") {
		t.Errorf("describe_image on text = %v, want 'not an image'", err)
	}
	if _, err := run(t, fx.tool("transcribe_audio"), map[string]any{"path": "/notes.txt"}); err == nil || !strings.Contains(err.Error(), "not audio") {
		t.Errorf("transcribe_audio on text = %v, want 'not audio'", err)
	}
	if _, err := run(t, fx.tool("describe_image"), map[string]any{"path": "/../bob/x.png"}); err == nil {
		t.Error("a path outside the home was accepted")
	}
	if len(fx.calls) != 0 {
		t.Fatalf("requests went out for refused files: %+v", fx.calls)
	}
}

func TestARefusedModelRequestSaysWhy(t *testing.T) {
	fx := newFixture(t, "vl", "")
	fx.put("/a.png", png)
	fx.reply = func(string) (int, string) { return 403, `{"error":{"message":"no"}}` }
	_, err := run(t, fx.tool("describe_image"), map[string]any{"path": "/a.png"})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("describe_image on a 403 = %v, want 'not allowed'", err)
	}
}

func TestAVoiceNoteIsTranscribedOnUpload(t *testing.T) {
	fx := newFixture(t, "", "whisper")
	text, up, err := fx.svc.PrepareUploads(context.Background(), "alice", "see this", []session.Upload{
		{Name: "memo.m4a", Body: strings.NewReader("AUDIOBYTES")},
		{Name: "notes.txt", Body: strings.NewReader("plain")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "see this") || !strings.Contains(text, "memo.m4a") || !strings.Contains(text, "hello from the recording") {
		t.Fatalf("message = %q, want the text and the transcript of memo.m4a", text)
	}
	if strings.Contains(text, "notes.txt") {
		t.Fatalf("message = %q mentions a file that is not audio", text)
	}
	if len(up) != 2 {
		t.Fatalf("uploads = %d, want both kept", len(up))
	}
	b, _ := io.ReadAll(up[0].Body)
	if string(b) != "AUDIOBYTES" {
		t.Fatalf("the voice note's bytes changed: %q", b)
	}
	if fx.calls[0].file != "memo.m4a:AUDIOBYTES" {
		t.Fatalf("transcription call = %+v", fx.calls[0])
	}
}

func TestAVoiceNoteWithNoTranscriptionModelIsStillKept(t *testing.T) {
	fx := newFixture(t, "", "")
	text, up, err := fx.svc.PrepareUploads(context.Background(), "alice", "hi", []session.Upload{{Name: "memo.m4a", Body: strings.NewReader("AUDIO")}})
	if err != nil || text != "hi" || len(up) != 1 {
		t.Fatalf("PrepareUploads = %q, %d, %v, want the message and file untouched", text, len(up), err)
	}
	if len(fx.calls) != 0 {
		t.Fatalf("a request went out: %+v", fx.calls)
	}
}
