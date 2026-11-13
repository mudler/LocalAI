package localaitools_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/httpapi"
	"github.com/mudler/LocalAI/core/services/localaitools/inproc"
	"github.com/mudler/LocalAI/core/services/studio/provider"
)

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.White)
	var b bytes.Buffer
	Expect(png.Encode(&b, img)).To(Succeed())
	return b.Bytes()
}

// memStore is a FileStore in memory that records what was written.
type memStore struct {
	mu      sync.Mutex
	files   map[string]localaitools.File
	written []string
}

func newMemStore() *memStore { return &memStore{files: map[string]localaitools.File{}} }

func (m *memStore) Read(_ context.Context, ref string, max int64) (localaitools.File, error) {
	if strings.HasPrefix(ref, "data:") {
		return localaitools.ReadDataURL(ref, max)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[ref]
	if !ok {
		return localaitools.File{}, localaitools.ErrFileNotFound
	}
	if int64(len(f.Data)) > max {
		return localaitools.File{}, localaitools.ErrFileTooLarge
	}
	return f, nil
}

func (m *memStore) Write(_ context.Context, name string, data []byte, mime string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ref := "/files/" + name
	m.files[ref] = localaitools.File{Name: name, MIME: mime, Data: data}
	m.written = append(m.written, ref)
	return ref, nil
}

// localAIStandIn is an httptest server that answers like the inference routes
// of LocalAI and records the requests.
type localAIStandIn struct {
	*httptest.Server
	mu        sync.Mutex
	installed map[string][]string // model -> capability labels
	forbid    map[string]bool     // route -> answer 403
	seen      map[string][]byte   // route -> last body
	headers   map[string]http.Header
	imageOut  []byte
	audioOut  []byte
	// verifyExtra is JSON members added to the verify answers.
	verifyExtra string
	// listed is what GET /v1/models/capabilities answers: the models the
	// caller may use.
	listed []map[string]any
	// wellknown is the data of the well-known files, by name, that
	// GET /api/chats/wellknown/{name} answers.
	wellknown map[string]map[string]any
	// disabled lists installed models that the capability list marks disabled.
	disabled map[string]bool
}

func newStandIn() *localAIStandIn {
	s := &localAIStandIn{
		installed: map[string][]string{},
		wellknown: map[string]map[string]any{},
		forbid:    map[string]bool{},
		seen:      map[string][]byte{},
		headers:   map[string]http.Header{},
		imageOut:  pngBytes(8, 4),
		audioOut:  []byte("RIFF....WAVEfmt "),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var data []map[string]any
		for name, caps := range s.installed {
			data = append(data, map[string]any{"id": name, "capabilities": caps, "disabled": s.disabled[name]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/api/chats/wellknown/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		data := s.wellknown[strings.TrimPrefix(r.URL.Path, "/api/chats/wellknown/")]
		if data == nil {
			data = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	mux.HandleFunc("/v1/models/capabilities", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.headers[r.URL.Path] = r.Header.Clone()
		if s.forbid[r.URL.Path] {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": s.listed})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.seen[r.URL.Path] = body
		s.headers[r.URL.Path] = r.Header.Clone()
		forbid := s.forbid[r.URL.Path]
		s.mu.Unlock()
		if forbid {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"feature disabled"}`))
			return
		}
		switch r.URL.Path {
		case "/v1/images/generations":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(s.imageOut)}}})
		case "/v1/images/inpainting":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"url": "http://somewhere.example/generated-images/out.png"}}})
		case "/generated-images/out.png":
			_, _ = w.Write(s.imageOut)
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(s.audioOut)
		case "/v1/audio/transcriptions":
			_, _ = w.Write([]byte(`{"text":" hello there "}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"a red square"}}]}`))
		case "/v1/embeddings":
			_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]},{"embedding":[0.4,0.5,0.6]}]}`))
		case "/v1/detection":
			_, _ = w.Write([]byte(`{"detections":[{"x":1,"y":2,"width":3,"height":4,"class_name":"cat","confidence":0.9,"mask":"AAAA"}]}`))
		case "/v1/rerank":
			_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.8,"document":{"text":"b"}}]}`))
		case "/video":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"))}}})
		case "/v1/sound-generation":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3...."))
		case "/v1/voice/verify", "/v1/face/verify":
			s.mu.Lock()
			extra := s.verifyExtra
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{"verified":true,"distance":0.1,"threshold":0.4,"confidence":0.9` + extra + `}`))
		case "/v1/voice/identify", "/v1/face/identify":
			_, _ = w.Write([]byte(`{"matches":[{"id":"1","name":"Ada","distance":0.1,"confidence":0.9,"match":true}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *localAIStandIn) body(route string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out map[string]any
	_ = json.Unmarshal(s.seen[route], &out)
	return out
}

var _ = Describe("client tools over the REST client", func() {
	var (
		srv   *localAIStandIn
		store *memStore
		cl    *httpapi.Client
		ctx   context.Context
	)

	BeforeEach(func() {
		srv = newStandIn()
		DeferCleanup(srv.Close)
		store = newMemStore()
		cl = httpapi.New(srv.URL, "secret")
		cl.Files = store
		ctx = context.Background()
	})

	It("chooses the first installed model with the use case, by name", func() {
		srv.installed["zeta-img"] = []string{"FLAG_IMAGE"}
		srv.installed["alpha-img"] = []string{"FLAG_IMAGE"}
		srv.installed["voice"] = []string{"FLAG_TTS"}
		res, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Model).To(Equal("alpha-img"))
		Expect(srv.body("/v1/images/generations")["model"]).To(Equal("alpha-img"))
	})

	It("uses the model the caller names without asking the server", func() {
		res, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox", Model: "mine"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Model).To(Equal("mine"))
	})

	It("says which capability has no installed model", func() {
		srv.installed["voice"] = []string{"FLAG_TTS"}
		_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).To(MatchError(ContainSubstring("no image generation model installed")))
		var nm *localaitools.NoModelError
		Expect(err).To(BeAssignableToTypeOf(nm))

		_, err = cl.Rerank(ctx, localaitools.RerankRequest{Query: "q", Documents: []string{"d"}})
		Expect(err).To(MatchError(ContainSubstring("no rerank model installed")))
	})

	It("writes the image to the file store and reports its size and dimensions, never the bytes", func() {
		res, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox", Model: "m", Size: "8x4", N: 1})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Images).To(HaveLen(1))
		img := res.Images[0]
		Expect(img.Width).To(Equal(8))
		Expect(img.Height).To(Equal(4))
		Expect(img.MIME).To(Equal("image/png"))
		Expect(img.Size).To(Equal(len(srv.imageOut)))
		Expect(store.files).To(HaveKey(img.Ref))
		Expect(store.files[img.Ref].Data).To(Equal(srv.imageOut))
		Expect(img.Ref).To(HaveSuffix(".png"))

		raw, _ := json.Marshal(res)
		Expect(string(raw)).ToNot(ContainSubstring(base64.StdEncoding.EncodeToString(srv.imageOut)))
		Expect(srv.body("/v1/images/generations")).To(HaveKeyWithValue("response_format", "b64_json"))
		Expect(srv.headers["/v1/images/generations"].Get("Authorization")).To(Equal("Bearer secret"))
	})

	It("maps a 403 to a message that names the capability", func() {
		srv.forbid["/v1/images/generations"] = true
		_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox", Model: "m"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("may not use image generation"))
		Expect(err.Error()).To(ContainSubstring("403"))
		var se *localaitools.StatusError
		Expect(err).To(BeAssignableToTypeOf(se))
	})

	It("reads an input file by reference and sends it as a multipart upload", func() {
		store.files["/files/talk.wav"] = localaitools.File{Name: "talk.wav", Data: []byte("RIFFxxxxWAVE")}
		res, err := cl.TranscribeAudio(ctx, localaitools.TranscribeAudioRequest{Audio: "/files/talk.wav", Model: "whisper", Language: "en"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Text).To(Equal("hello there"))
		body := string(srv.seen["/v1/audio/transcriptions"])
		Expect(body).To(ContainSubstring(`name="model"`))
		Expect(body).To(ContainSubstring("filename=talk.wav"))
		Expect(body).To(ContainSubstring("RIFFxxxxWAVE"))
		Expect(srv.headers["/v1/audio/transcriptions"].Get("Content-Type")).To(HavePrefix("multipart/form-data"))
	})

	It("reports a file reference that does not exist, and a file over the limit", func() {
		_, err := cl.TranscribeAudio(ctx, localaitools.TranscribeAudioRequest{Audio: "/files/none.wav", Model: "w"})
		Expect(err).To(MatchError(ContainSubstring("file not found")))

		store.files["/files/big.png"] = localaitools.File{Name: "big.png", Data: make([]byte, localaitools.MaxImageBytes+1)}
		_, err = cl.DescribeImage(ctx, localaitools.DescribeImageRequest{Image: "/files/big.png", Model: "v"})
		Expect(err).To(MatchError(ContainSubstring("file too large")))
		Expect(srv.seen).ToNot(HaveKey("/v1/chat/completions"))
	})

	It("refuses a file that is not an image", func() {
		store.files["/files/note.txt"] = localaitools.File{Name: "note.txt", Data: []byte("just text")}
		_, err := cl.DescribeImage(ctx, localaitools.DescribeImageRequest{Image: "/files/note.txt", Model: "v"})
		Expect(err).To(MatchError(ContainSubstring("not an image")))
	})

	It("reads the file from the store of the context when the caller sets one, and types a wrong kind", func() {
		other := newMemStore()
		other.files["/mine/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(2, 2)}
		other.files["/mine/n.txt"] = localaitools.File{Name: "n.txt", Data: []byte("words")}
		octx := localaitools.WithFiles(ctx, other)
		res, err := cl.DescribeImage(octx, localaitools.DescribeImageRequest{Image: "/mine/a.png", Model: "vl"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Description).To(Equal("a red square"))
		_, err = cl.DescribeImage(octx, localaitools.DescribeImageRequest{Image: "/mine/n.txt", Model: "vl"})
		var kind *localaitools.WrongKindError
		Expect(errors.As(err, &kind)).To(BeTrue())
		Expect(err).To(MatchError("image is not an image"))
		Expect(localaitools.WithFiles(ctx, nil)).To(Equal(ctx))
	})

	It("describes an image with a chat completion that carries the image as a data URL", func() {
		store.files["/files/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(2, 2)}
		res, err := cl.DescribeImage(ctx, localaitools.DescribeImageRequest{Image: "/files/a.png", Model: "vl", Question: "what is it?"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Description).To(Equal("a red square"))
		Expect(string(srv.seen["/v1/chat/completions"])).To(ContainSubstring("data:image/png;base64,"))
	})

	It("fetches the result of an inpainting call from the address it returns", func() {
		store.files["/files/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(4, 4)}
		store.files["/files/m.png"] = localaitools.File{Name: "m.png", Data: pngBytes(4, 4)}
		res, err := cl.EditImage(ctx, localaitools.EditImageRequest{Prompt: "a hat", Image: "/files/a.png", Mask: "/files/m.png", Model: "sd"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Images).To(HaveLen(1))
		Expect(res.Images[0].Width).To(Equal(8))
		body := string(srv.seen["/v1/images/inpainting"])
		Expect(body).To(ContainSubstring("name=mask"))
		Expect(body).To(ContainSubstring("name=image"))
	})

	It("keeps the vectors out of the result", func() {
		res, err := cl.Embed(ctx, localaitools.EmbedRequest{Input: []string{"a", "b"}, Model: "e"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Count).To(Equal(2))
		Expect(res.Dimensions).To(Equal(3))
		Expect(res.Preview).To(HaveLen(2))
		Expect(store.files[res.Vectors.Ref].MIME).To(Equal("application/json"))
	})

	It("runs the remaining text and recognition tools", func() {
		store.files["/files/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(2, 2)}
		store.files["/files/a.wav"] = localaitools.File{Name: "a.wav", Data: []byte("RIFFxxxxWAVE")}

		d, err := cl.Detect(ctx, localaitools.DetectRequest{Image: "/files/a.png", Model: "d"})
		Expect(err).ToNot(HaveOccurred())
		Expect(d.Detections).To(HaveLen(1))
		Expect(d.Detections[0].ClassName).To(Equal("cat"))
		Expect(d.Detections[0].HasMask).To(BeTrue())

		r, err := cl.Rerank(ctx, localaitools.RerankRequest{Query: "q", Documents: []string{"a", "b"}, TopN: 1, Model: "r"})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Results).To(Equal([]localaitools.RerankItem{{Index: 1, Score: 0.8, Text: "b"}}))

		v, err := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "waves", Model: "vid"})
		Expect(err).ToNot(HaveOccurred())
		Expect(v.Video.Ref).To(HaveSuffix(".mp4"))

		snd, err := cl.GenerateSound(ctx, localaitools.GenerateSoundRequest{Text: "rain", Model: "snd"})
		Expect(err).ToNot(HaveOccurred())
		Expect(snd.Audio.MIME).To(Equal("audio/mpeg"))
		Expect(srv.body("/v1/sound-generation")).To(HaveKeyWithValue("model_id", "snd"))

		vv, err := cl.VerifyVoice(ctx, localaitools.VoiceVerifyRequest{Audio1: "/files/a.wav", Audio2: "/files/a.wav", Model: "s"})
		Expect(err).ToNot(HaveOccurred())
		Expect(vv.Verified).To(BeTrue())
		fv, err := cl.VerifyFace(ctx, localaitools.VerifyFaceRequest{Image1: "/files/a.png", Image2: "/files/a.png", Model: "f"})
		Expect(err).ToNot(HaveOccurred())
		Expect(fv.Verified).To(BeTrue())
		Expect(srv.body("/v1/face/verify")).To(HaveKey("img1"))

		iv, err := cl.IdentifyVoice(ctx, localaitools.IdentifyVoiceRequest{Audio: "/files/a.wav", Model: "s", TopK: 3})
		Expect(err).ToNot(HaveOccurred())
		Expect(iv.Matches).To(HaveLen(1))
		ifc, err := cl.IdentifyFace(ctx, localaitools.IdentifyFaceRequest{Image: "/files/a.png", Model: "f"})
		Expect(err).ToNot(HaveOccurred())
		Expect(ifc.Matches[0].Name).To(Equal("Ada"))
	})

	It("reports that no file store is configured when a tool needs one", func() {
		cl.Files = nil
		_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox", Model: "m"})
		Expect(err).To(MatchError(ContainSubstring("no file store")))
	})

	It("validates arguments before it calls the server", func() {
		_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{})
		Expect(err).To(MatchError(ContainSubstring("prompt is required")))
		_, err = cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "x", N: 9})
		Expect(err).To(MatchError(ContainSubstring("n must be")))
		_, err = cl.Speak(ctx, localaitools.SpeakRequest{})
		Expect(err).To(MatchError(ContainSubstring("text is required")))
		Expect(srv.seen).To(BeEmpty())
	})

	It("refuses a file path as the voice of a user who is not an administrator", func() {
		srv.installed["tts"] = []string{"FLAG_TTS"}
		user := &auth.User{ID: "u", Role: auth.RoleUser, Status: auth.StatusActive}
		for _, v := range []string{"/etc/passwd", "../clip.wav", `a\b.wav`} {
			_, err := cl.Speak(localaitools.WithUser(ctx, user), localaitools.SpeakRequest{Text: "hi", Voice: v})
			Expect(err).To(MatchError(ContainSubstring("not a file path")), v)
		}
		Expect(srv.seen).To(BeEmpty())
		_, err := cl.Speak(localaitools.WithUser(ctx, user), localaitools.SpeakRequest{Text: "hi", Voice: "alloy"})
		Expect(err).ToNot(HaveOccurred())
		_, err = cl.Speak(localaitools.WithUser(ctx, &auth.User{ID: "a", Role: auth.RoleAdmin}), localaitools.SpeakRequest{Text: "hi", Voice: "/srv/clip.wav"})
		Expect(err).ToNot(HaveOccurred())
	})

	// The end-to-end case: the MCP tools, the REST client, and a stand-in for
	// the server. generate_image writes a file, speak uses a saved voice.
	It("serves generate_image and speak through the MCP server", func() {
		srv.installed["sd"] = []string{"FLAG_IMAGE"}
		srv.installed["tts"] = []string{"FLAG_TTS"}

		mcpSrv := localaitools.NewServer(cl, localaitools.Options{})
		t1, t2 := mcp.NewInMemoryTransports()
		sctx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		ss, err := mcpSrv.Connect(sctx, t1, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = ss.Wait() })
		sess, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "v0"}, nil).Connect(sctx, t2, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = sess.Close() })

		text := func(r *mcp.CallToolResult) string {
			var b strings.Builder
			for _, c := range r.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					b.WriteString(tc.Text)
				}
			}
			return b.String()
		}

		res, err := sess.CallTool(sctx, &mcp.CallToolParams{Name: localaitools.ToolGenerateImage, Arguments: map[string]any{"prompt": "a fox", "size": "8x4"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.IsError).To(BeFalse(), text(res))
		var img localaitools.ImageResult
		Expect(json.Unmarshal([]byte(text(res)), &img)).To(Succeed())
		Expect(img.Model).To(Equal("sd"))
		Expect(store.files).To(HaveKey(img.Images[0].Ref))

		voice := "voice-profile://00000000-0000-0000-0000-000000000001"
		res, err = sess.CallTool(sctx, &mcp.CallToolParams{Name: localaitools.ToolSpeak, Arguments: map[string]any{"text": "hello", "voice": voice}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.IsError).To(BeFalse(), text(res))
		var sp localaitools.AudioResult
		Expect(json.Unmarshal([]byte(text(res)), &sp)).To(Succeed())
		Expect(sp.Model).To(Equal("tts"))
		Expect(sp.Audio.MIME).To(Equal("audio/wav"))
		Expect(store.files[sp.Audio.Ref].Data).To(Equal(srv.audioOut))
		Expect(srv.body("/v1/audio/speech")).To(HaveKeyWithValue("voice", voice))

		// A refusal reaches the model as a tool error it can tell the person.
		srv.forbid["/v1/audio/speech"] = true
		res, err = sess.CallTool(sctx, &mcp.CallToolParams{Name: localaitools.ToolSpeak, Arguments: map[string]any{"text": "hello"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.IsError).To(BeTrue())
		Expect(text(res)).To(ContainSubstring("may not use text-to-speech"))
	})
})

var _ = Describe("DirFileStore", func() {
	var dir string
	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("writes a new file for every result and reads it back by its reference", func() {
		fs := localaitools.DirFileStore{Dir: dir}
		a, err := fs.Write(context.Background(), "image.png", []byte("one"), "image/png")
		Expect(err).ToNot(HaveOccurred())
		b, err := fs.Write(context.Background(), "image.png", []byte("two"), "image/png")
		Expect(err).ToNot(HaveOccurred())
		Expect(a).ToNot(Equal(b))
		f, err := fs.Read(context.Background(), a, 100)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(f.Data)).To(Equal("one"))
		rel, err := fs.Read(context.Background(), filepath.Base(b), 100)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(rel.Data)).To(Equal("two"))
	})

	It("does not read outside its folder", func() {
		outside := filepath.Join(GinkgoT().TempDir(), "secret.txt")
		Expect(os.WriteFile(outside, []byte("secret"), 0o600)).To(Succeed())
		fs := localaitools.DirFileStore{Dir: dir}
		for _, ref := range []string{outside, "../secret.txt", filepath.Join("..", filepath.Base(filepath.Dir(outside)), "secret.txt")} {
			_, err := fs.Read(context.Background(), ref, 100)
			Expect(err).To(MatchError(localaitools.ErrFileNotFound), ref)
		}
		Expect(os.Symlink(outside, filepath.Join(dir, "link.txt"))).To(Succeed())
		_, err := fs.Read(context.Background(), "link.txt", 100)
		Expect(err).To(MatchError(localaitools.ErrFileNotFound))
	})

	It("reads a data URL and enforces the limit", func() {
		fs := localaitools.DirFileStore{Dir: dir}
		url := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("hello"))
		f, err := fs.Read(context.Background(), url, 100)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(f.Data)).To(Equal("hello"))
		_, err = fs.Read(context.Background(), url, 2)
		Expect(err).To(MatchError(localaitools.ErrFileTooLarge))
	})
})

// chooserFunc is a ModelChooser from a function.
type chooserFunc func(config.ModelConfigUsecase) string

func (f chooserFunc) FirstModel(_ context.Context, use config.ModelConfigUsecase) (string, error) {
	return f(use), nil
}

// The two implementations send the same requests and read the same answers.
// The in-process one reaches the stand-in through the in-memory transport,
// as the acting user; the REST one through HTTP.
var _ = Describe("client tools parity and acting user", func() {
	var (
		srv   *localAIStandIn
		store *memStore
		rest  localaitools.LocalAIClient
		local *inproc.Client
		ctx   context.Context
	)

	BeforeEach(func() {
		srv = newStandIn()
		DeferCleanup(srv.Close)
		store = newMemStore()
		ctx = context.Background()
		srv.installed["img"] = []string{"FLAG_IMAGE"}
		srv.installed["tts"] = []string{"FLAG_TTS"}

		h := httpapi.New(srv.URL, "")
		h.Files = store
		rest = h

		tr := provider.NewTransport()
		tr.SetHandler(srv.Config.Handler)
		local = &inproc.Client{
			SingleUser: true,
			Transport:  func() *provider.Transport { return tr },
			Files:      store,
			Models: chooserFunc(func(use config.ModelConfigUsecase) string {
				switch use {
				case config.FLAG_IMAGE:
					return "img"
				case config.FLAG_TTS:
					return "tts"
				}
				return ""
			}),
		}
	})

	It("GenerateImage and Speak produce the same result", func() {
		for name, c := range map[string]localaitools.LocalAIClient{"rest": rest, "inproc": local} {
			img, err := c.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
			Expect(err).ToNot(HaveOccurred(), name)
			Expect(img.Model).To(Equal("img"), name)
			Expect(img.Images[0].Width).To(Equal(8), name)
			Expect(img.Images[0].MIME).To(Equal("image/png"), name)

			sp, err := c.Speak(ctx, localaitools.SpeakRequest{Text: "hello"})
			Expect(err).ToNot(HaveOccurred(), name)
			Expect(sp.Model).To(Equal("tts"), name)
			Expect(sp.Audio.MIME).To(Equal("audio/wav"), name)

			emb, err := c.Embed(ctx, localaitools.EmbedRequest{Input: []string{"a", "b"}, Model: "e"})
			Expect(err).ToNot(HaveOccurred(), name)
			Expect(emb.Dimensions).To(Equal(3), name)

			rr, err := c.Rerank(ctx, localaitools.RerankRequest{Query: "q", Documents: []string{"a", "b"}, Model: "r"})
			Expect(err).ToNot(HaveOccurred(), name)
			Expect(rr.Results).To(HaveLen(1), name)
		}
	})

	It("reports the same error when no model is installed", func() {
		for name, c := range map[string]localaitools.LocalAIClient{"rest": rest, "inproc": local} {
			_, err := c.Detect(ctx, localaitools.DetectRequest{Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(2, 2))})
			Expect(err).To(MatchError(ContainSubstring("no detection model installed")), name)
		}
	})

	It("does not tell apart a refusal by the route from one by the model allow-list", func() {
		srv.forbid["/v1/images/generations"] = true
		for name, c := range map[string]localaitools.LocalAIClient{"rest": rest, "inproc": local} {
			_, err := c.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
			Expect(err).To(MatchError(ContainSubstring("may not use image generation")), name)
		}
	})

	It("acts as the user of the context: two users, one client, different answers", func() {
		// The stand-in refuses what the user may not use, as the API does.
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, _ := auth.InternalUser(r.Context())
			if u == nil || u.ID != "alice" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			srv.Config.Handler.ServeHTTP(w, r)
		}))
		local.Transport = func() *provider.Transport { return tr }

		alice := localaitools.WithUser(ctx, &auth.User{ID: "alice", Status: auth.StatusActive})
		bob := localaitools.WithUser(ctx, &auth.User{ID: "bob", Status: auth.StatusActive})
		_, err := local.GenerateImage(alice, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).ToNot(HaveOccurred())
		_, err = local.GenerateImage(bob, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).To(MatchError(ContainSubstring("may not use image generation")))
		// Single-user mode, no fallback user: refused, and nothing carried
		// over from the calls above.
		_, err = local.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).To(MatchError(ContainSubstring("may not use image generation")))
		// The fallback user is the identity of a call with no context user,
		// in single-user mode only.
		local.FallbackUser = &auth.User{ID: "alice", Status: auth.StatusActive}
		_, err = local.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).ToNot(HaveOccurred())
		// With authentication on, a call without a user fails before it
		// reaches the API, whatever the fallback user is.
		local.SingleUser = false
		_, err = local.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).To(MatchError(ContainSubstring("no acting user")))
		local.SingleUser = true
		_, err = local.GenerateImage(bob, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).To(HaveOccurred())
	})

	It("reports that inference is unavailable when no transport is set", func() {
		local.Transport = nil
		_, err := local.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "a fox"})
		Expect(err).To(MatchError(ContainSubstring("not available")))
	})
})

var _ = Describe("limits of the client tools", func() {
	var (
		srv   *localAIStandIn
		cl    *httpapi.Client
		ctx   = context.Background()
		model = "m"
	)
	BeforeEach(func() {
		srv = newStandIn()
		DeferCleanup(srv.Close)
		cl = httpapi.New(srv.URL, "secret")
		cl.Files = newMemStore()
	})

	It("refuses numbers past the caps with a plain error and sends nothing", func() {
		f32 := func(v float32) *float32 { return &v }
		calls := map[string]func() error{
			"steps": func() error {
				_, e := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "p", Model: model, Steps: 100000})
				return e
			},
			"n": func() error {
				_, e := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "p", Model: model, N: 9})
				return e
			},
			"seconds": func() error {
				_, e := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "p", Model: model, Seconds: "3600"})
				return e
			},
			"fps": func() error {
				_, e := cl.GenerateVideo(ctx, localaitools.GenerateVideoRequest{Prompt: "p", Model: model, FPS: 1000})
				return e
			},
			"duration": func() error {
				_, e := cl.GenerateSound(ctx, localaitools.GenerateSoundRequest{Text: "p", Model: model, Duration: f32(99999)})
				return e
			},
			"top_k": func() error {
				_, e := cl.IdentifyVoice(ctx, localaitools.IdentifyVoiceRequest{Audio: "data:audio/wav;base64,AAAA", Model: model, TopK: 100000})
				return e
			},
			"documents": func() error {
				_, e := cl.Rerank(ctx, localaitools.RerankRequest{Query: "q", Model: model, Documents: make([]string, 5000)})
				return e
			},
			"input": func() error {
				_, e := cl.Embed(ctx, localaitools.EmbedRequest{Model: model, Input: make([]string, 5000)})
				return e
			},
			"weigh": func() error {
				_, e := cl.Embed(ctx, localaitools.EmbedRequest{Model: model, Input: []string{strings.Repeat("x", 2<<20)}})
				return e
			},
		}
		for name, call := range calls {
			Expect(call()).To(MatchError(ContainSubstring(name)), name)
		}
		for _, route := range []string{"/v1/images/generations", "/video", "/v1/sound-generation", "/v1/rerank", "/v1/embeddings", "/v1/voice/identify"} {
			Expect(srv.body(route)).To(BeNil(), route)
		}
	})

	It("accepts values inside the caps", func() {
		_, err := cl.GenerateImage(ctx, localaitools.GenerateImageRequest{Prompt: "p", Model: model, Steps: 30, N: 2})
		Expect(err).ToNot(HaveOccurred())
	})

	It("lets a user have two media calls running and answers busy to a third", func() {
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		blocked := httpapi.New("http://unused.invalid", "k")
		blocked.Files = newMemStore()
		blocked.InferenceHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			started <- struct{}{}
			<-release
			return nil, errors.New("stop")
		})}
		user := &auth.User{ID: "busy-user"}
		uctx := localaitools.WithUser(ctx, user)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = blocked.GenerateImage(uctx, localaitools.GenerateImageRequest{Prompt: "p", Model: model})
			}()
		}
		Eventually(started).Should(Receive())
		Eventually(started).Should(Receive())
		_, err := blocked.GenerateImage(uctx, localaitools.GenerateImageRequest{Prompt: "p", Model: model})
		Expect(err).To(MatchError(ContainSubstring("busy")))
		// Another user is not held up by it.
		_, err = blocked.GenerateImage(localaitools.WithUser(ctx, &auth.User{ID: "other"}), localaitools.GenerateImageRequest{Prompt: "p", Model: model, N: 9})
		Expect(err).To(MatchError(ContainSubstring("n must")))
		close(release)
		wg.Wait()
		// The places are free again.
		Expect(func() error {
			_, e := cl.GenerateImage(uctx, localaitools.GenerateImageRequest{Prompt: "p", Model: model})
			return e
		}()).To(Succeed())
	})
})

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var _ = Describe("IsAudioName", func() {
	It("knows the recordings by extension, whatever the case", func() {
		for _, n := range []string{"a.wav", "B.MP3", "c.m4a", "d.ogg", "e.opus", "f.flac", "g.webm", "h.aac", "i.weba", "j.oga"} {
			Expect(localaitools.IsAudioName(n)).To(BeTrue(), n)
		}
		for _, n := range []string{"a.png", "b.txt", "wav", ""} {
			Expect(localaitools.IsAudioName(n)).To(BeFalse(), n)
		}
	})
})

// list_models runs through the API route as the acting user, so it works for
// an account that has no admin tool, and shows only what the route shows it.
var _ = Describe("list_models", func() {
	ctx := context.Background()
	entry := func(id, desc string, caps ...string) map[string]any {
		return map[string]any{"id": id, "object": "model", "capabilities": caps, "description": desc}
	}

	Describe("over the REST client", func() {
		var (
			srv *localAIStandIn
			cl  *httpapi.Client
		)
		BeforeEach(func() {
			srv = newStandIn()
			DeferCleanup(srv.Close)
			cl = httpapi.New(srv.URL, "secret")
			srv.listed = []map[string]any{
				entry("zeta-voice", "", "tts"),
				entry("flux", "Fast image model.", "image"),
				entry("asr", "", "transcript"),
				entry("qwen", "", "chat", "tools"),
			}
		})

		It("lists every model by name with its capabilities and description", func() {
			out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(out.Hint).To(BeEmpty())
			var names []string
			for _, m := range out.Models {
				names = append(names, m.Name)
			}
			Expect(names).To(Equal([]string{"asr", "flux", "qwen", "zeta-voice"}))
			Expect(out.Models[1]).To(Equal(localaitools.ModelInfo{Name: "flux", Capabilities: []string{"image"}, Description: "Fast image model."}))
		})

		It("filters by capability, and understands the common synonyms", func() {
			for _, c := range []string{"tts", "TTS", "speech", " text-to-speech "} {
				out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Capability: c})
				Expect(err).ToNot(HaveOccurred(), c)
				Expect(out.Models).To(HaveLen(1), c)
				Expect(out.Models[0].Name).To(Equal("zeta-voice"), c)
			}
			out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Capability: "transcription"})
			Expect(err).ToNot(HaveOccurred())
			Expect(out.Models[0].Name).To(Equal("asr"))
		})

		It("says what to do when nothing matches", func() {
			out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Capability: "video"})
			Expect(err).ToNot(HaveOccurred())
			Expect(out.Models).To(BeEmpty())
			Expect(out.Hint).To(ContainSubstring(`"video"`))
			Expect(out.Hint).To(ContainSubstring("without capability"))
			srv.listed = nil
			out, err = cl.ListModels(ctx, localaitools.ListModelsRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(out.Hint).To(ContainSubstring("no model is available"))
		})

		It("keeps the answer small: a cap on the count and on a description", func() {
			srv.listed = nil
			for i := 0; i < 70; i++ {
				srv.listed = append(srv.listed, entry(fmt.Sprintf("m%02d", i), strings.Repeat("long text ", 40), "chat"))
			}
			out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(out.Models).To(HaveLen(60))
			Expect(out.More).To(Equal(10))
			Expect(out.Models[0].Name).To(Equal("m00"))
			Expect(len(out.Models[0].Description)).To(BeNumerically("<=", 123))
			Expect(out.Models[0].Description).To(HaveSuffix("..."))
		})

		Describe("paging, order and search", func() {
			names := func(out *localaitools.ModelList) []string {
				var n []string
				for _, m := range out.Models {
					n = append(n, m.Name)
				}
				return n
			}
			BeforeEach(func() {
				// 100 lower case chat models and 46 capitalised image
				// models: byte order would put every capital first.
				srv.listed = nil
				for i := 0; i < 100; i++ {
					srv.listed = append(srv.listed, entry(fmt.Sprintf("m%03d", i), "", "chat"))
				}
				for i := 0; i < 46; i++ {
					srv.listed = append(srv.listed, entry(fmt.Sprintf("Img-%02d", i), "", "image"))
				}
			})

			DescribeTable("pages",
				func(offset, limit, wantFirstN int, wantReturned, wantMore int, wantHint string) {
					out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Offset: offset, Limit: limit})
					Expect(err).ToNot(HaveOccurred())
					Expect(out.Total).To(Equal(146))
					Expect(out.Offset).To(Equal(offset))
					Expect(out.Returned).To(Equal(wantReturned))
					Expect(out.Models).To(HaveLen(wantReturned))
					Expect(out.More).To(Equal(wantMore))
					if wantHint == "" {
						Expect(out.Hint).To(BeEmpty())
					} else {
						Expect(out.Hint).To(ContainSubstring(wantHint))
					}
					_ = wantFirstN
				},
				Entry("first page", 0, 0, 0, 60, 86, "showing 1-60 of 146; call list_models again with offset=60 to continue, or pass capability or query to narrow"),
				Entry("middle page", 60, 0, 0, 60, 26, "showing 61-120 of 146; call list_models again with offset=120"),
				Entry("last page", 120, 0, 0, 26, 0, ""),
				Entry("past the end", 500, 0, 0, 0, 0, "offset 500 is past the end: 146 models match"),
				Entry("small limit", 10, 5, 0, 5, 131, "showing 11-15 of 146; call list_models again with offset=15"),
				Entry("limit above the maximum", 0, 1000, 0, 60, 86, "offset=60"),
			)

			It("sorts without regard to case, so the same name family stays together", func() {
				srv.listed = []map[string]any{
					entry("zed", "", "chat"), entry("Qwen-Image-2.1-Turbo", "", "image"),
					entry("qwen-image-2.1", "", "image"), entry("Alpha", "", "chat"), entry("beta", "", "chat"),
					entry("QWEN-image-2.1", "", "image"),
				}
				out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{})
				Expect(err).ToNot(HaveOccurred())
				Expect(names(out)).To(Equal([]string{"Alpha", "beta", "QWEN-image-2.1", "qwen-image-2.1", "Qwen-Image-2.1-Turbo", "zed"}))
			})

			It("reaches a lower case name that byte order would hide", func() {
				out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Offset: 0, Limit: 60})
				Expect(err).ToNot(HaveOccurred())
				// Img-00 sorts with the "i" names, after m-less names do not exist,
				// so both kinds appear on the first page only by name order.
				Expect(names(out)[0]).To(Equal("Img-00"))
				out, err = cl.ListModels(ctx, localaitools.ListModelsRequest{Offset: 120})
				Expect(err).ToNot(HaveOccurred())
				Expect(names(out)[len(out.Models)-1]).To(Equal("m099"))
			})

			It("filters by query on the name or the description, in any case", func() {
				srv.listed = []map[string]any{
					entry("Qwen-Image", "", "image"), entry("flux", "Draws with QWEN style.", "image"), entry("asr", "", "transcript"),
				}
				out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Query: " qwen "})
				Expect(err).ToNot(HaveOccurred())
				Expect(names(out)).To(Equal([]string{"flux", "Qwen-Image"}))
				Expect(out.Total).To(Equal(2))
				out, err = cl.ListModels(ctx, localaitools.ListModelsRequest{Query: "nothing"})
				Expect(err).ToNot(HaveOccurred())
				Expect(out.Models).To(BeEmpty())
				Expect(out.Hint).To(ContainSubstring(`no model matches query "nothing"`))
			})

			It("pages inside a capability filter", func() {
				out, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Capability: "image", Offset: 40})
				Expect(err).ToNot(HaveOccurred())
				Expect(out.Total).To(Equal(46))
				Expect(out.Returned).To(Equal(6))
				Expect(out.Hint).To(BeEmpty())
				out, err = cl.ListModels(ctx, localaitools.ListModelsRequest{Capability: "chat", Limit: 30})
				Expect(err).ToNot(HaveOccurred())
				Expect(out.Total).To(Equal(100))
				Expect(out.Hint).To(ContainSubstring("showing 1-30 of 100; call list_models again with offset=30"))
			})

			It("refuses a negative offset or limit", func() {
				_, err := cl.ListModels(ctx, localaitools.ListModelsRequest{Offset: -1})
				Expect(err).To(HaveOccurred())
				_, err = cl.ListModels(ctx, localaitools.ListModelsRequest{Limit: -1})
				Expect(err).To(HaveOccurred())
			})
		})

		It("sends the key, and reports a refusal of the server", func() {
			_, err := cl.ListModels(ctx, localaitools.ListModelsRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.headers["/v1/models/capabilities"].Get("Authorization")).To(Equal("Bearer secret"))
			srv.forbid["/v1/models/capabilities"] = true
			_, err = cl.ListModels(ctx, localaitools.ListModelsRequest{})
			Expect(err).To(MatchError(ContainSubstring("403")))
		})
	})

	It("lists as the acting user in process: a user without admin sees only the models the route lets them use", func() {
		allowed := map[string]bool{"flux": true}
		var seen []string
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := auth.InternalUser(r.Context())
			if !ok || r.URL.Path != "/v1/models/capabilities" {
				http.Error(w, "no user", http.StatusUnauthorized)
				return
			}
			seen = append(seen, u.ID)
			var data []map[string]any
			for _, m := range []map[string]any{entry("flux", "", "image"), entry("secret-model", "", "image")} {
				// What the route does: an admin sees all, another user the allow-list.
				if u.Role == auth.RoleAdmin || allowed[m["id"].(string)] {
					data = append(data, m)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		})
		tr := provider.NewTransport()
		tr.SetHandler(handler)
		c := &inproc.Client{Transport: func() *provider.Transport { return tr }}

		bob := &auth.User{ID: "bob", Role: auth.RoleUser, Status: auth.StatusActive}
		out, err := c.ListModels(localaitools.WithUser(ctx, bob), localaitools.ListModelsRequest{Capability: "image"})
		Expect(err).ToNot(HaveOccurred())
		Expect(out.Models).To(HaveLen(1))
		Expect(out.Models[0].Name).To(Equal("flux"))

		root := &auth.User{ID: "root", Role: auth.RoleAdmin, Status: auth.StatusActive}
		out, err = c.ListModels(localaitools.WithUser(ctx, root), localaitools.ListModelsRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(out.Models).To(HaveLen(2))
		Expect(seen).To(Equal([]string{"bob", "root"}))
	})

	It("refuses an in-process call that has no acting user, as the other client tools do", func() {
		tr := provider.NewTransport()
		tr.SetHandler(http.NotFoundHandler())
		c := &inproc.Client{Transport: func() *provider.Transport { return tr }}
		_, err := c.ListModels(ctx, localaitools.ListModelsRequest{})
		Expect(err).To(MatchError(ContainSubstring("no acting user")))
	})

	It("points a 404 for a wrong model name at the tool a user can call", func() {
		err := (&localaitools.StatusError{Capability: "image generation", Model: "nope", Status: http.StatusNotFound}).Error()
		Expect(err).To(ContainSubstring("list_models"))
		Expect(err).NotTo(ContainSubstring("list_installed_models"))
	})
})
