package localaitools_test

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/httpapi"
)

// mediaForm is a multipart request as the handler would read it.
type mediaForm struct {
	fields map[string]string
	files  map[string][]byte
	names  map[string]string
}

// mediaStandIn answers like the upscale and audio-transform routes and records
// the forms it received, with the field names the real handlers read.
type mediaStandIn struct {
	*httptest.Server
	mu        sync.Mutex
	forms     map[string]mediaForm
	forbid    map[string]bool
	installed map[string][]string
	// stems, when set, is sent as the X-Audio-Stems header.
	stems     string
	bodyAudio []byte
	files     map[string][]byte
}

func newMediaStandIn() *mediaStandIn {
	s := &mediaStandIn{
		forms:     map[string]mediaForm{},
		forbid:    map[string]bool{},
		installed: map[string][]string{},
		bodyAudio: []byte("RIFF\x00\x00\x00\x00WAVEbody"),
		files:     map[string][]byte{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/api/models/capabilities" {
			var data []map[string]any
			for name, caps := range s.installed {
				data = append(data, map[string]any{"id": name, "capabilities": caps})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		if s.forbid[r.URL.Path] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"feature disabled"}`))
			return
		}
		if r.Method == http.MethodGet {
			if b, ok := s.files[r.URL.Path]; ok {
				_, _ = w.Write(b)
				return
			}
			http.NotFound(w, r)
			return
		}
		form := mediaForm{fields: map[string]string{}, files: map[string][]byte{}, names: map[string]string{}}
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			http.Error(w, "bad content type", http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				form.files[p.FormName()] = b
				form.names[p.FormName()] = p.FileName()
			} else {
				form.fields[p.FormName()] = string(b)
			}
		}
		s.forms[r.URL.Path] = form
		switch r.URL.Path {
		case "/v1/images/upscale":
			s.files["/generated-images/up.png"] = pngBytes(16, 8)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"url": "http://somewhere.example/generated-images/up.png"}}})
		case "/audio/transformations":
			w.Header().Set("Content-Type", "audio/wav")
			if s.stems != "" {
				w.Header().Set("X-Audio-Stems", s.stems)
			}
			_, _ = w.Write(s.bodyAudio)
		default:
			http.NotFound(w, r)
		}
	}))
	return s
}

func (s *mediaStandIn) form(route string) mediaForm {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forms[route]
}

var _ = Describe("upscale_image", func() {
	var (
		srv   *mediaStandIn
		store *memStore
		cl    *httpapi.Client
		ctx   = context.Background()
	)
	BeforeEach(func() {
		srv = newMediaStandIn()
		DeferCleanup(srv.Close)
		store = newMemStore()
		cl = httpapi.New(srv.URL, "secret")
		cl.Files = store
		store.files["/files/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(4, 2)}
	})

	It("posts the fields the handler reads and stores the result as a file", func() {
		res, err := cl.UpscaleImage(ctx, localaitools.UpscaleImageRequest{Image: "/files/a.png", Scale: 4, Model: "x4"})
		Expect(err).ToNot(HaveOccurred())
		form := srv.form("/v1/images/upscale")
		Expect(form.fields).To(Equal(map[string]string{"model": "x4", "scale": "4"}))
		Expect(form.files).To(HaveKey("image"))
		Expect(form.names["image"]).To(Equal("a.png"))
		Expect(res.Model).To(Equal("x4"))
		Expect(res.Images).To(HaveLen(1))
		Expect(res.Images[0].Width).To(Equal(16))
		Expect(res.Images[0].Height).To(Equal(8))
		Expect(store.files).To(HaveKey(res.Images[0].Ref))
	})

	It("takes the first upscaler model when none is named", func() {
		srv.installed["up"] = []string{"FLAG_UPSCALE"}
		srv.installed["sd"] = []string{"FLAG_IMAGE"}
		res, err := cl.UpscaleImage(ctx, localaitools.UpscaleImageRequest{Image: "/files/a.png", Scale: 2})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Model).To(Equal("up"))
		Expect(srv.form("/v1/images/upscale").fields["model"]).To(Equal("up"))
	})

	It("says so when no upscaler is installed", func() {
		_, err := cl.UpscaleImage(ctx, localaitools.UpscaleImageRequest{Image: "/files/a.png", Scale: 2})
		Expect(err).To(MatchError(ContainSubstring("no image upscaling model installed")))
	})

	It("refuses a scale outside 1 to 16 and a missing or wrong file before any request", func() {
		for _, sc := range []int{0, -1, 17} {
			_, err := cl.UpscaleImage(ctx, localaitools.UpscaleImageRequest{Image: "/files/a.png", Scale: sc, Model: "x4"})
			Expect(err).To(MatchError(ContainSubstring("scale must be an integer from 1 to 16")), "%d", sc)
		}
		_, err := cl.UpscaleImage(ctx, localaitools.UpscaleImageRequest{Image: "/files/none.png", Scale: 4, Model: "x4"})
		Expect(err).To(HaveOccurred())
		store.files["/files/t.txt"] = localaitools.File{Name: "t.txt", Data: []byte("plain text")}
		_, err = cl.UpscaleImage(ctx, localaitools.UpscaleImageRequest{Image: "/files/t.txt", Scale: 4, Model: "x4"})
		Expect(err).To(MatchError(ContainSubstring("image")))
		Expect(srv.forms).To(BeEmpty())
	})

	It("tells a user without the images permission which capability is refused", func() {
		srv.forbid["/v1/images/upscale"] = true
		user := &auth.User{ID: "u", Role: auth.RoleUser, Status: auth.StatusActive}
		_, err := cl.UpscaleImage(localaitools.WithUser(ctx, user), localaitools.UpscaleImageRequest{Image: "/files/a.png", Scale: 4, Model: "x4"})
		Expect(err).To(MatchError(ContainSubstring("may not use image upscaling")))
	})

	It("is a client tool on the images feature, with no admin-only rights", func() {
		spec := localaitools.ClientTools
		var found bool
		for _, s := range spec {
			if s.Tool == localaitools.ToolUpscaleImage {
				found = true
				Expect(s.Route).To(Equal("/v1/images/upscale"))
				Expect(s.Feature).To(Equal(auth.FeatureImages))
			}
		}
		Expect(found).To(BeTrue())
		Expect(localaitools.ToolGroupOf(localaitools.ToolUpscaleImage)).To(Equal(localaitools.GroupClient))
	})
})

var _ = Describe("list_models capability names of the media tools", func() {
	It("maps the words for upscaling to the capability the route names", func() {
		for _, w := range []string{"upscale", "Upscaling", " upscaler ", "image_upscale", "super-resolution"} {
			Expect(localaitools.NormalizeCapability(w)).To(Equal("upscale"), w)
		}
	})

	It("maps the words for audio transformation to the capability the route names", func() {
		for _, w := range []string{"audio_transform", "Audio_Transform", "audio_transformation", "denoise", "separation"} {
			Expect(localaitools.NormalizeCapability(w)).To(Equal("audio_transform"), w)
		}
	})

	It("lists the models of the media tools under their capability", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"id": "flux", "capabilities": []string{"image"}},
				{"id": "x4", "capabilities": []string{"upscale"}},
			}})
		}))
		DeferCleanup(srv.Close)
		cl := httpapi.New(srv.URL, "t")
		out, err := cl.ListModels(context.Background(), localaitools.ListModelsRequest{Capability: "upscaling"})
		Expect(err).ToNot(HaveOccurred())
		Expect(out.Models).To(HaveLen(1))
		Expect(out.Models[0].Name).To(Equal("x4"))
	})
})

var _ = Describe("transform_audio", func() {
	var (
		srv   *mediaStandIn
		store *memStore
		cl    *httpapi.Client
		ctx   = context.Background()
	)
	wav := []byte("RIFF\x24\x00\x00\x00WAVEfmt input")
	BeforeEach(func() {
		srv = newMediaStandIn()
		DeferCleanup(srv.Close)
		store = newMemStore()
		cl = httpapi.New(srv.URL, "secret")
		cl.Files = store
		store.files["/files/mic.wav"] = localaitools.File{Name: "mic.wav", MIME: "audio/wav", Data: wav}
		store.files["/files/far.wav"] = localaitools.File{Name: "far.wav", MIME: "audio/wav", Data: []byte("RIFF\x24\x00\x00\x00WAVEfmt loop")}
	})

	It("posts the fields the handler reads and stores the audio as a file", func() {
		res, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{
			Audio: "/files/mic.wav", Reference: "/files/far.wav", Model: "vqe", Format: "mp3", SampleRate: 48000,
			Params: map[string]string{"language": "en", "noise_gate": "true", "noise_gate_threshold_dbfs": "-50"},
		})
		Expect(err).ToNot(HaveOccurred())
		form := srv.form("/audio/transformations")
		Expect(form.fields).To(Equal(map[string]string{
			"model": "vqe", "response_format": "mp3", "sample_rate": "48000",
			"params[noise_gate]": "true", "params[noise_gate_threshold_dbfs]": "-50", "params[language]": "en",
		}))
		Expect(form.files["audio"]).To(Equal(wav))
		Expect(form.files).To(HaveKey("reference"))
		Expect(res.Model).To(Equal("vqe"))
		Expect(res.Audio.MIME).To(Equal("audio/wav"))
		Expect(store.files[res.Audio.Ref].Data).To(Equal(srv.bodyAudio))
		Expect(res.Stems).To(BeEmpty())
	})

	It("sends no reference part when none is given, and only the fields that were set", func() {
		_, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "vqe"})
		Expect(err).ToNot(HaveOccurred())
		form := srv.form("/audio/transformations")
		Expect(form.fields).To(Equal(map[string]string{"model": "vqe"}))
		Expect(form.files).ToNot(HaveKey("reference"))
	})

	It("takes the first audio-transform model when none is named", func() {
		srv.installed["sep"] = []string{"FLAG_AUDIO_TRANSFORM"}
		srv.installed["tts"] = []string{"FLAG_TTS"}
		res, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav"})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Model).To(Equal("sep"))
	})

	It("says so when no audio-transform model is installed", func() {
		_, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav"})
		Expect(err).To(MatchError(ContainSubstring("no audio transformation model installed")))
	})

	Describe("stems", func() {
		It("fetches each stem, stores it as a file, and shares the file of the body", func() {
			srv.files["/generated-audio/t.drums.wav"] = []byte("RIFF\x00\x00\x00\x00WAVEdrums")
			srv.files["/generated-audio/t.vocals.wav"] = srv.bodyAudio
			srv.stems = `[{"name":"drums","url":"/generated-audio/t.drums.wav"},{"name":"vocals","url":"/generated-audio/t.vocals.wav"}]`
			res, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "demucs", Params: map[string]string{"stem": "vocals"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(srv.form("/audio/transformations").fields).To(HaveKeyWithValue("params[stem]", "vocals"))
			Expect(res.Stems).To(HaveLen(2))
			Expect(res.Stems[0].Name).To(Equal("drums"))
			Expect(res.Stems[0].Audio.Ref).ToNot(Equal(res.Audio.Ref))
			Expect(store.files[res.Stems[0].Audio.Ref].Data).To(ContainSubstring("drums"))
			Expect(res.Stems[1].Name).To(Equal("vocals"))
			Expect(res.Stems[1].Audio.Ref).To(Equal(res.Audio.Ref))
			Expect(res.Note).To(BeEmpty())
		})

		It("fetches only files of the generated-audio folder", func() {
			srv.files["/etc/passwd"] = []byte("secret")
			srv.files["/generated-audio/ok.wav"] = []byte("RIFF\x00\x00\x00\x00WAVEok")
			srv.stems = `[{"name":"a","url":"/etc/passwd"},{"name":"b","url":"http://other.example/generated-audio/x.wav"},` +
				`{"name":"c","url":"/generated-audio/../etc/passwd"},{"name":"d","url":"/generated-audio/ok.wav?x=1"},{"name":"e","url":"/generated-audio/ok.wav"}]`
			res, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Stems).To(HaveLen(1))
			Expect(res.Stems[0].Name).To(Equal("e"))
		})

		It("keeps the main audio and says so when a stem cannot be fetched", func() {
			srv.stems = `[{"name":"gone","url":"/generated-audio/none.wav"}]`
			res, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Audio.Ref).ToNot(BeEmpty())
			Expect(res.Stems).To(BeEmpty())
			Expect(res.Note).To(ContainSubstring("1 of 1 stems"))
		})

		It("ignores a header that is not the stem list", func() {
			srv.stems = "not json"
			res, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m"})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Stems).To(BeEmpty())
		})
	})

	Describe("params", func() {
		bad := func(p map[string]string, msg string) {
			_, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m", Params: p})
			Expect(err).To(MatchError(ContainSubstring(msg)), "%v", p)
			Expect(srv.forms).To(BeEmpty())
		}
		It("refuses a key outside the allow-list, and names the allowed ones", func() {
			bad(map[string]string{"model_path": "/etc/passwd"}, `"model_path" is not allowed`)
			bad(map[string]string{"reference_path": "x"}, "stem, target_text, text")
			bad(map[string]string{"params[x]": "y"}, "is not allowed")
		})
		It("refuses paths and URLs as values of a name", func() {
			for _, v := range []string{"/etc/passwd", "../x", `a\b`, "C:\\x", "a/b", "http://x", "..", ".hidden", ""} {
				bad(map[string]string{"stem": v}, "stem must be")
			}
			bad(map[string]string{"language": "/tmp/x"}, "language must be")
			bad(map[string]string{"stem": strings.Repeat("a", 65)}, "stem must be")
		})
		It("refuses a value of the wrong kind", func() {
			bad(map[string]string{"noise_gate": "yes"}, "noise_gate must be true or false")
			bad(map[string]string{"noise_gate_threshold_dbfs": "loud"}, "from -120 to 0")
			bad(map[string]string{"noise_gate_threshold_dbfs": "NaN"}, "from -120 to 0")
			bad(map[string]string{"noise_gate_threshold_dbfs": "12"}, "from -120 to 0")
			bad(map[string]string{"text": "line\x00break"}, "text must be plain text")
			bad(map[string]string{"target_text": strings.Repeat("x", 2001)}, "target_text must be plain text")
		})
		It("passes text, a stem name with a space, and a language", func() {
			_, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m",
				Params: map[string]string{"text": "Hello, world. It's 3.5 o'clock!", "stem": "lead vocals", "language": "it"}})
			Expect(err).ToNot(HaveOccurred())
			f := srv.form("/audio/transformations").fields
			Expect(f).To(HaveKeyWithValue("params[text]", "Hello, world. It's 3.5 o'clock!"))
			Expect(f).To(HaveKeyWithValue("params[stem]", "lead vocals"))
			Expect(f).To(HaveKeyWithValue("params[language]", "it"))
		})
	})

	It("refuses a sample rate outside the route's range and an unknown format before any request", func() {
		for _, sr := range []int{1, 7999, 192001, -5} {
			_, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m", SampleRate: sr})
			Expect(err).To(MatchError(ContainSubstring("sample_rate must be from 8000 to 192000")), "%d", sr)
		}
		_, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m", Format: "aac"})
		Expect(err).To(MatchError(ContainSubstring("response_format must be")))
		Expect(srv.forms).To(BeEmpty())
	})

	It("refuses a missing file and a file that is not audio", func() {
		_, err := cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "", Model: "m"})
		Expect(err).To(MatchError(ContainSubstring("audio is required")))
		_, err = cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/none.wav", Model: "m"})
		Expect(err).To(HaveOccurred())
		store.files["/files/a.png"] = localaitools.File{Name: "a.png", Data: pngBytes(2, 2)}
		_, err = cl.TransformAudio(ctx, localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Reference: "/files/a.png", Model: "m"})
		Expect(err).To(MatchError(ContainSubstring("reference")))
		Expect(srv.forms).To(BeEmpty())
	})

	It("tells a user without the audio_transform permission which capability is refused", func() {
		srv.forbid["/audio/transformations"] = true
		user := &auth.User{ID: "u", Role: auth.RoleUser, Status: auth.StatusActive}
		_, err := cl.TransformAudio(localaitools.WithUser(ctx, user), localaitools.TransformAudioRequest{Audio: "/files/mic.wav", Model: "m"})
		Expect(err).To(MatchError(ContainSubstring("may not use audio transformation")))
	})

	It("is a client tool on the audio_transform feature", func() {
		var found bool
		for _, s := range localaitools.ClientTools {
			if s.Tool == localaitools.ToolTransformAudio {
				found = true
				Expect(s.Route).To(Equal("/audio/transformations"))
				Expect(s.Feature).To(Equal(auth.FeatureAudioTransform))
			}
		}
		Expect(found).To(BeTrue())
		Expect(localaitools.ToolGroupOf(localaitools.ToolTransformAudio)).To(Equal(localaitools.GroupClient))
	})
})
