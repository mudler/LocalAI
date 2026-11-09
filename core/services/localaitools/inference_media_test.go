package localaitools_test

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
