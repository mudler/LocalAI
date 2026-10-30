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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
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
	f     *files.Service
	svc   *media.Service
	mu    sync.Mutex
	calls []call
	reply func(path string) (int, string)
}

func newFixture(vision, transcription string) *fixture {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).ToNot(HaveOccurred())
	blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	fx := &fixture{f: files.New(ws)}
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
	Expect(fx.f.EnsureHome(context.Background(), "alice")).To(Succeed())
	return fx
}

func (fx *fixture) put(rel, content string) {
	abs, _ := fx.f.Abs("alice", rel)
	_, err := fx.f.Store().Put(context.Background(), fx.f.Actor("alice"), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).ToNot(HaveOccurred())
}

func (fx *fixture) tool(name string) session.Tool {
	return fx.toolIn(name, "chat-model")
}

// toolIn is a tool of a chat whose own model is chatModel.
func (fx *fixture) toolIn(name, chatModel string) session.Tool {
	for _, tl := range fx.svc.Tools(fx.f, workspace.Actor{Principal: "alice", ChatID: "c1"}, chatModel) {
		if tl.Name == name {
			return tl
		}
	}
	Fail("no tool " + name)
	return session.Tool{}
}

func run(tl session.Tool, args map[string]any) (string, error) {
	raw, _ := json.Marshal(args)
	return tl.Run(context.Background(), raw)
}

type countingReader struct {
	left int64
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	for i := range p {
		p[i] = 'a'
	}
	c.left -= int64(len(p))
	c.read += int64(len(p))
	return len(p), nil
}

var _ = Describe("media tools", func() {
	It("describe_image sends the image to the vision model as the user", func() {
		fx := newFixture("vl-model", "")
		fx.put("/Pictures/red.png", png)
		out, err := run(fx.tool("describe_image"), map[string]any{"path": "/Pictures/red.png", "question": "what colour?"})
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal("a red square"))
		Expect(fx.calls).To(HaveLen(1))
		c := fx.calls[0]
		Expect(c.path).To(Equal("/v1/chat/completions"))
		Expect(c.user).To(Equal("alice"))
		Expect(c.json["model"]).To(Equal("vl-model"))
		body, _ := json.Marshal(c.json)
		want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(png))
		Expect(string(body)).To(ContainSubstring(want))
		Expect(string(body)).To(ContainSubstring("what colour?"))
	})

	It("transcribe_audio sends the file to the transcription model", func() {
		fx := newFixture("", "whisper")
		fx.put("/Voice/note.wav", "RIFFdata")
		out, err := run(fx.tool("transcribe_audio"), map[string]any{"path": "/Voice/note.wav"})
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal("hello from the recording"))
		c := fx.calls[0]
		Expect(c.path).To(Equal("/v1/audio/transcriptions"))
		Expect(c.fields["model"]).To(Equal("whisper"))
		Expect(c.file).To(Equal("note.wav:RIFFdata"))
		Expect(c.user).To(Equal("alice"))
	})

	It("without a model the tool says what to install", func() {
		fx := newFixture("", "")
		fx.put("/a.wav", "RIFF")
		_, err := run(fx.tool("transcribe_audio"), map[string]any{"path": "/a.wav"})
		Expect(err).To(HaveOccurred(), "transcribe_audio with no model")
		Expect(err.Error()).To(ContainSubstring("Install"), "transcribe_audio with no model")
		Expect(fx.calls).To(BeEmpty(), "a request went out with no model")
	})

	It("without a vision model describe_image says what the user and an administrator can do", func() {
		fx := newFixture("", "")
		fx.put("/a.png", png)
		_, err := run(fx.tool("describe_image"), map[string]any{"path": "/a.png"})
		Expect(err).To(MatchError("No vision model: choose a model that can look at images for this chat, " +
			"or an administrator can install one from the model gallery and choose it in Settings under Chat image model."))
		Expect(fx.calls).To(BeEmpty(), "a request went out with no model")
	})

	It("describe_image uses the model the chooser picks for the chat's own model", func() {
		fx := newFixture("", "")
		var asked []string
		fx.svc.WithVisionChooser(func(chatModel string) string {
			asked = append(asked, chatModel)
			if chatModel == "cloud-vl" {
				return chatModel
			}
			return ""
		})
		fx.put("/a.png", png)
		out, err := run(fx.toolIn("describe_image", "cloud-vl"), map[string]any{"path": "/a.png"})
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal("a red square"))
		Expect(fx.calls).To(HaveLen(1))
		Expect(fx.calls[0].json["model"]).To(Equal("cloud-vl"))
		Expect(asked).To(ContainElement("cloud-vl"))
		Expect(fx.svc.VisionModel("cloud-vl")).To(Equal("cloud-vl"))
		Expect(fx.svc.VisionModel("text-only")).To(BeEmpty())

		_, err = run(fx.toolIn("describe_image", "text-only"), map[string]any{"path": "/a.png"})
		Expect(err).To(MatchError(ContainSubstring("No vision model")))
		Expect(fx.calls).To(HaveLen(1), "a request went out with no model")
	})

	It("without a chooser describe_image uses the resolver's vision model", func() {
		fx := newFixture("vl-model", "")
		Expect(fx.svc.VisionModel("anything")).To(Equal("vl-model"))
		Expect(newFixture("", "").svc.VisionModel("anything")).To(BeEmpty())
	})

	It("the tools refuse what is not the right kind of file", func() {
		fx := newFixture("vl", "whisper")
		fx.put("/notes.txt", "just words")
		_, err := run(fx.tool("describe_image"), map[string]any{"path": "/notes.txt"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not an image"))
		_, err = run(fx.tool("transcribe_audio"), map[string]any{"path": "/notes.txt"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not audio"))
		_, err = run(fx.tool("describe_image"), map[string]any{"path": "/../bob/x.png"})
		Expect(err).To(HaveOccurred(), "a path outside the home was accepted")
		Expect(fx.calls).To(BeEmpty(), "requests went out for refused files")
	})

	It("a refused model request says why", func() {
		fx := newFixture("vl", "")
		fx.put("/a.png", png)
		fx.reply = func(string) (int, string) { return 403, `{"error":{"message":"no"}}` }
		_, err := run(fx.tool("describe_image"), map[string]any{"path": "/a.png"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not allowed"))
	})

	It("a voice note is transcribed on upload", func() {
		fx := newFixture("", "whisper")
		text, up, err := fx.svc.PrepareUploads(context.Background(), "alice", "see this", []session.Upload{
			{Name: "memo.m4a", Body: strings.NewReader("AUDIOBYTES")},
			{Name: "notes.txt", Body: strings.NewReader("plain")},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(text).To(ContainSubstring("see this"))
		Expect(text).To(ContainSubstring("memo.m4a"))
		Expect(text).To(ContainSubstring("hello from the recording"))
		Expect(text).ToNot(ContainSubstring("notes.txt"), "the message mentions a file that is not audio")
		Expect(up).To(HaveLen(2), "both uploads are kept")
		b, _ := io.ReadAll(up[0].Body)
		Expect(string(b)).To(Equal("AUDIOBYTES"), "the voice note's bytes changed")
		Expect(fx.calls[0].file).To(Equal("memo.m4a:AUDIOBYTES"))
	})

	It("says whether a recording can be transcribed, and which files are recordings", func() {
		Expect(newFixture("", "").svc.CanTranscribe()).To(BeFalse())
		Expect(newFixture("", "whisper").svc.CanTranscribe()).To(BeTrue())
		var none *media.Service
		Expect(none.CanTranscribe()).To(BeFalse(), "a chat without media")
		Expect(media.IsAudio("voice-1.ogg")).To(BeTrue())
		Expect(media.IsAudio("photo-1.JPG")).To(BeFalse())
	})

	It("a voice note with no transcription model is still kept", func() {
		fx := newFixture("", "")
		text, up, err := fx.svc.PrepareUploads(context.Background(), "alice", "hi", []session.Upload{{Name: "memo.m4a", Body: strings.NewReader("AUDIO")}})
		Expect(err).ToNot(HaveOccurred())
		Expect(text).To(Equal("hi"))
		Expect(up).To(HaveLen(1))
		Expect(fx.calls).To(BeEmpty(), "a request went out")
	})

	// A voice note over the limit is kept whole, but PrepareUploads must not pull
	// it all into memory to say so: it reads one byte past the limit and leaves
	// the rest of the body streaming.
	It("an oversized voice note is not read into memory", func() {
		const limit = 25 << 20
		const total = 40 << 20
		fx := newFixture("", "whisper")
		src := &countingReader{left: total}
		_, up, err := fx.svc.PrepareUploads(context.Background(), "alice", "hi", []session.Upload{{Name: "long.wav", Body: src}})
		Expect(err).ToNot(HaveOccurred())
		Expect(src.read).To(BeNumerically("<=", limit+1), "PrepareUploads consumed too many bytes")
		n, err := io.Copy(io.Discard, up[0].Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(total), "the kept body must have all the bytes")
		Expect(fx.calls).To(BeEmpty(), "an oversized note was sent to the model")
	})
})
