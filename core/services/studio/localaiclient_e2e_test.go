package studio_test

// End-to-end specs for the LocalAI client tools of the chats.
//
// The other specs of this package drive the chats with fake tool executors.
// These use the real chain from the chat to the API:
//
//	scripted model -> chat tools -> MCP server (localaitools) -> inproc client
//	-> in-memory transport -> stand-in API (echo) -> result file in the
//	workspace of the user
//
// Only the model (a script) and the inference backends are replaced. The
// stand-in API is a small echo app that
//   - reads the user the way the auth middleware does (auth.InternalUser),
//   - answers 403 for a feature that the account lacks, as RequireFeature does,
//   - serves the real voice profile endpoints over a real voiceprofile.Store,
//   - serves the real TTSEndpoint over a recording backend, and
//   - answers the image route with a small PNG.
// Every request it gets is recorded with the user, so a spec can say what
// reached the API and for whom.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/inproc"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/adminskills"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpcLib "google.golang.org/grpc"
)

// apiHit is one request that reached the stand-in API.
type apiHit struct {
	User, Method, Path string
	// Voice, Model and Prompt are the fields of a JSON body, when it has them.
	Voice, Model, Prompt string
	Status               int
}

// recordingTTS is the speech backend of the stand-in API: it keeps the
// requests it got and answers with a short WAV.
type recordingTTS struct {
	grpc.Backend
	mu       sync.Mutex
	requests []*proto.TTSRequest
}

func (b *recordingTTS) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *recordingTTS) IsBusy() bool                              { return false }
func (b *recordingTTS) TTS(_ context.Context, req *proto.TTSRequest, _ ...grpcLib.CallOption) (*proto.Result, error) {
	b.mu.Lock()
	b.requests = append(b.requests, req)
	b.mu.Unlock()
	if err := os.WriteFile(req.Dst, e2eWAV(time.Second), 0o600); err != nil {
		return nil, err
	}
	return &proto.Result{Success: true}, nil
}
func (b *recordingTTS) count() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.requests) }

// e2eWAV returns a silent 16-bit PCM mono WAV of the given length.
func e2eWAV(d time.Duration) []byte {
	const rate = 16000
	size := int(d.Seconds()*rate) * 2
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	le(uint32(36 + size))
	b.WriteString("WAVEfmt ")
	le(uint32(16))
	le(uint16(1))
	le(uint16(1))
	le(uint32(rate))
	le(uint32(rate * 2))
	le(uint16(2))
	le(uint16(16))
	b.WriteString("data")
	le(uint32(size))
	b.Write(make([]byte, size))
	return b.Bytes()
}

func e2ePNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		img.Set(x, x, color.RGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	Expect(png.Encode(&b, img)).To(Succeed())
	return b.Bytes()
}

// standInAPI is the stand-in for LocalAI's inference API.
type standInAPI struct {
	echo    *echo.Echo
	store   *voiceprofile.Store
	backend *recordingTTS
	png     []byte

	mu     sync.Mutex
	hits   []apiHit
	denied map[string][]string // user -> features the API refuses
}

func newStandInAPI() *standInAPI {
	GinkgoHelper()
	a := &standInAPI{backend: &recordingTTS{}, png: e2ePNG(), denied: map[string][]string{}}
	a.store = voiceprofile.NewStore(GinkgoT().TempDir())
	DeferCleanup(func() { _ = a.store.Close() })

	state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
	Expect(err).NotTo(HaveOccurred())
	appConfig := config.NewApplicationConfig(config.WithSystemState(state))
	appConfig.GeneratedContentDir = GinkgoT().TempDir()
	ml := model.NewModelLoader(state)
	ttsCfg := &config.ModelConfig{Name: "clone-tts", Backend: "qwen3-tts-cpp", TTSConfig: config.TTSConfig{VoiceCloning: new(bool)}}
	*ttsCfg.TTSConfig.VoiceCloning = true
	ttsCfg.Model = "weights"
	loaded := model.NewModelWithClient(ttsCfg.ModelID(), "in-process", a.backend)
	loaded.MarkHealthy()
	_, err = ml.LoadModel(ttsCfg.ModelID(), ttsCfg.Model, func(_, _, _ string) (*model.Model, error) { return loaded, nil })
	Expect(err).NotTo(HaveOccurred())

	e := echo.New()
	e.Use(a.gate)
	e.POST("/v1/images/generations", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(a.png)}}})
	})
	e.GET("/api/voice-profiles", localai.ListVoiceProfilesEndpoint(a.store))
	e.POST("/api/voice-profiles", localai.CreateVoiceProfileEndpoint(a.store))
	e.DELETE("/api/voice-profiles/:id", localai.DeleteVoiceProfileEndpoint(a.store))
	e.POST("/v1/audio/speech", localai.TTSEndpoint(nil, ml, appConfig, a.store), func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			var req schema.TTSRequest
			if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
				return echo.ErrBadRequest
			}
			c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &req)
			cfg := *ttsCfg
			c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &cfg)
			return next(c)
		}
	})
	a.echo = e
	return a
}

// gate acts like the auth middleware for an in-process caller, applies the
// feature check of the route, and records the request.
func (a *standInAPI) gate(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		u, _ := auth.InternalUser(req.Context())
		hit := apiHit{Method: req.Method, Path: req.URL.Path}
		if u != nil {
			c.Set("auth_user", u)
			hit.User = u.ID
		}
		if strings.HasPrefix(req.Header.Get(echo.HeaderContentType), echo.MIMEApplicationJSON) && req.Body != nil {
			raw, _ := io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var body struct {
				Voice, Model, Prompt string
				Input                string
			}
			_ = json.Unmarshal(raw, &struct {
				Voice  *string `json:"voice"`
				Model  *string `json:"model"`
				Prompt *string `json:"prompt"`
			}{&body.Voice, &body.Model, &body.Prompt})
			hit.Voice, hit.Model, hit.Prompt = body.Voice, body.Model, body.Prompt
		}
		refused := false
		for _, spec := range localaitools.ClientTools {
			if spec.Method == req.Method && spec.Route == c.Path() && u != nil && a.isDenied(u.ID, spec.Feature) {
				refused = true
			}
		}
		var err error
		if refused {
			hit.Status = http.StatusForbidden
			err = c.JSON(http.StatusForbidden, map[string]any{"error": map[string]any{"message": "feature refused", "code": 403}})
		} else {
			err = next(c)
			hit.Status = c.Response().Status
		}
		a.mu.Lock()
		a.hits = append(a.hits, hit)
		a.mu.Unlock()
		return err
	}
}

func (a *standInAPI) isDenied(user, feature string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.denied[user], feature)
}

func (a *standInAPI) deny(user string, features ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.denied[user] = append(a.denied[user], features...)
}

func (a *standInAPI) Hits(method, path string) []apiHit {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []apiHit
	for _, h := range a.hits {
		if h.Method == method && h.Path == path {
			out = append(out, h)
		}
	}
	return out
}

func (a *standInAPI) total() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.hits) }

// profileOf stores a voice profile owned by the user and returns it.
func (a *standInAPI) profileOf(user string) voiceprofile.Profile {
	GinkgoHelper()
	p, err := a.store.CreateFor(context.Background(), voiceprofile.Viewer{UserID: user}, voiceprofile.CreateInput{
		Name: "Narrator of " + user, ConsentConfirmed: true,
	}, []voiceprofile.ReferenceInput{{Transcript: "Hello there.", Audio: bytes.NewReader(e2eWAV(2 * time.Second))}})
	Expect(err).NotTo(HaveOccurred())
	return p
}

// realAssistant is the assistant and the client tools of the application
// (core/application/studio_assistant.go) built over the real holder.
type realAssistant struct {
	h      *mcpendpoint.LocalAIAssistantHolder
	models localaitools.ModelChooser
}

func (a realAssistant) Enabled() bool                            { return a.h.HasTools() }
func (a realAssistant) Executor() mcpendpoint.ToolExecutor       { return a.h.Executor() }
func (a realAssistant) SystemPrompt() string                     { return a.h.SystemPrompt() }
func (a realAssistant) CompactSystemPrompt() string              { return a.h.CompactSystemPrompt() }
func (a realAssistant) ClientExecutor() mcpendpoint.ToolExecutor { return a.h.ClientExecutor() }
func (a realAssistant) FirstModel(ctx context.Context, use config.ModelConfigUsecase) (string, error) {
	return a.models.FirstModel(ctx, use)
}

// installedModels names a model for every use case, as a server with all
// the models installed does.
type installedModels map[config.ModelConfigUsecase]string

func (m installedModels) FirstModel(_ context.Context, use config.ModelConfigUsecase) (string, error) {
	return m[use], nil
}

// e2eRig is the studio service with the real client tools over a stand-in API.
type e2eRig struct {
	svc    *studio.Service
	h      *studiotest.ToolHarness
	api    *standInAPI
	files  *files.Service
	holder *mcpendpoint.LocalAIAssistantHolder
	agents agents.Store

	mu       sync.Mutex
	clientOn bool
	adminOn  bool
	search   string
	denied   map[string][]string // the account lacks these features (the chat hides the tool)
}

func newE2ERig(script func(workspace.Actor, string) []studiotest.ToolStep, opts ...func(*studio.Deps)) *e2eRig {
	GinkgoHelper()
	ctx := context.Background()
	r := &e2eRig{api: newStandInAPI(), clientOn: true, adminOn: true, search: config.ToolSearchOff, denied: map[string][]string{}}
	r.files = newFiles()
	Expect(r.files.SyncBuiltin(ctx, adminskills.With(builtinskills.FS()))).To(Succeed())
	as, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.agents = as

	models := installedModels{}
	for _, s := range localaitools.ClientTools {
		models[s.Flag] = "m-" + s.Capability
	}
	models[config.FLAG_TTS] = "clone-tts"

	tr := provider.NewTransport()
	tr.SetHandler(r.api.echo)
	var svc *studio.Service
	client := &inproc.Client{
		Chats:     func() *studio.Service { return svc },
		Transport: func() *provider.Transport { return tr },
		Models:    models,
	}
	r.holder = mcpendpoint.NewLocalAIAssistantHolder()
	Expect(r.holder.Initialize(ctx, client, localaitools.Options{})).To(Succeed())
	DeferCleanup(func() { _ = r.holder.Close() })
	assistant := realAssistant{h: r.holder, models: models}

	svc, r.h = newToolService(script, func(d *studio.Deps) {
		d.Assistant, d.ClientTools, d.Users, d.Agents, d.Files = assistant, assistant, assistantUsers, as, r.files
		d.LocalAIClientOn = func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.clientOn }
		d.LocalAIAdminOn = func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.adminOn }
		d.ToolSearchMode = func() string { r.mu.Lock(); defer r.mu.Unlock(); return r.search }
		d.Allowed = func(u *auth.User, feature string) bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return !slices.Contains(r.denied[u.ID], feature)
		}
		for _, o := range opts {
			o(d)
		}
	})
	r.svc = svc
	return r
}

func (r *e2eRig) set(f func()) { r.mu.Lock(); defer r.mu.Unlock(); f() }

// scriptCall is the script of the rig: a message "tool|{json}" makes the model
// call that tool once and end its turn. "spawn|{task json}" starts a
// sub-agent that waits for its answer.
func scriptCall(_ workspace.Actor, text string) []studiotest.ToolStep {
	tool, args, ok := strings.Cut(text, "|")
	if !ok {
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	return []studiotest.ToolStep{{Tool: tool, Args: args}, {Say: "done"}}
}

func msg(tool, args string) string { return tool + "|" + args }

// spawnMsg is the message that makes a chat start a sub-agent whose task is
// the given message.
func spawnMsg(task, agent string) string {
	raw, _ := json.Marshal(map[string]any{"task": task, "wait": true, "agent": agent})
	return msg("spawn_agent", string(raw))
}

// say sends one message in a new chat of the user and waits for the turn.
func (r *e2eRig) say(user, text string, prep ...func(id string)) string {
	GinkgoHelper()
	id := create(r.svc, user)
	for _, p := range prep {
		p(id)
	}
	r.sayIn(user, id, text)
	return id
}

func (r *e2eRig) sayIn(user, id, text string) {
	GinkgoHelper()
	sent(r.svc, user, id, text)
	idle(r.svc, user, id)
}

// result returns the one result of a tool in a chat.
func (r *e2eRig) result(chat, tool string) studiotest.ToolResult {
	GinkgoHelper()
	var out []studiotest.ToolResult
	for _, res := range r.h.ResultsFor(tool) {
		if res.Chat == chat {
			out = append(out, res)
		}
	}
	Expect(out).To(HaveLen(1), "calls of %s in the chat", tool)
	return out[0]
}

// call has the model of a new chat call a tool and returns the result.
func (r *e2eRig) call(user, tool, args string, prep ...func(id string)) studiotest.ToolResult {
	GinkgoHelper()
	id := r.say(user, msg(tool, args), prep...)
	return r.result(id, tool)
}

// said is what the model saw of a tool result: its text and its error.
func said(res studiotest.ToolResult) string {
	if res.Err != nil {
		return res.Out + " ERROR: " + res.Err.Error()
	}
	return res.Out
}

// adminNames lists the admin tools of the server.
func (r *e2eRig) adminNames() []string {
	GinkgoHelper()
	fns, err := r.holder.Executor().DiscoverTools(context.Background())
	Expect(err).NotTo(HaveOccurred())
	var out []functions.Function
	out = append(out, fns...)
	names := make([]string, 0, len(out))
	for _, f := range out {
		names = append(names, f.Name)
	}
	Expect(names).To(ContainElement("install_model"))
	return names
}

func (r *e2eRig) toolsOf(chat string) []string { return r.h.ToolNames(chat) }

// put writes a file into the home folder of a user.
func (r *e2eRig) put(user, rel string, data []byte) {
	GinkgoHelper()
	ctx := context.Background()
	p := r.svc.Principal(user)
	Expect(r.files.EnsureHome(ctx, p)).To(Succeed())
	abs, err := r.files.Abs(p, rel)
	Expect(err).NotTo(HaveOccurred())
	_, err = r.files.Store().Put(ctx, r.files.Actor(p), abs, bytes.NewReader(data), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

// read returns the file a tool result names, as that user sees their workspace.
func (r *e2eRig) read(user, ref string) ([]byte, error) {
	ctx := context.Background()
	p := r.svc.Principal(user)
	rel := strings.TrimPrefix(ref, session.GuestRoot+"/"+session.GuestHome)
	abs, err := r.files.Abs(p, rel)
	if err != nil {
		return nil, err
	}
	rc, _, err := r.files.Store().Open(ctx, r.files.Actor(p), abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func decode[T any](out string) T {
	GinkgoHelper()
	var v T
	Expect(json.Unmarshal([]byte(out), &v)).To(Succeed(), out)
	return v
}

const e2eCreateArgs = `{"action":"create","name":"Narrator","audio":"/workspace/home/clip.wav","transcript":"Hello there.","language":"en"}`

var _ = Describe("the LocalAI client tools of the chats, end to end", func() {
	ctx := context.Background()

	pending := func(r *e2eRig, user string) int {
		GinkgoHelper()
		list, err := r.svc.Changes().List(ctx, user, "pending")
		Expect(err).NotTo(HaveOccurred())
		return len(list)
	}

	Describe("a user without the admin role", func() {
		It("sees the client tools and no admin tool, makes an image into a file of their own, and speaks with their own voice profile", func() {
			r := newE2ERig(scriptCall)
			profile := r.api.profileOf(bob)

			// The admin group is opt-in; bob switches it on and still gets none of it.
			id := r.say(bob, "hi", func(id string) {
				Expect(r.svc.SetBuiltinsOn(ctx, bob, id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
			})
			names := r.toolsOf(id)
			Expect(names).To(ContainElements(clientNames()))
			for _, n := range r.adminNames() {
				Expect(names).NotTo(ContainElement(n), "an admin tool reached a user")
			}

			img := r.call(bob, "generate_image", `{"prompt":"a red fox"}`)
			Expect(img.Err).NotTo(HaveOccurred())
			made := decode[localaitools.ImageResult](img.Out)
			Expect(made.Images).To(HaveLen(1))
			ref := made.Images[0].Ref
			Expect(ref).To(HavePrefix("/workspace/home/generated/"))
			Expect(len(img.Out)).To(BeNumerically("<", 600), "the result carries a reference, not bytes")
			Expect(img.Out).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString(r.api.png)[:24]))
			data, err := r.read(bob, ref)
			Expect(err).NotTo(HaveOccurred(), "the file is in the workspace of the user")
			Expect(data).To(Equal(r.api.png))
			_, err = r.read(alice, ref)
			Expect(err).To(HaveOccurred(), "another user does not have the file")
			hits := r.api.Hits(http.MethodPost, "/v1/images/generations")
			Expect(hits).To(HaveLen(1))
			Expect(hits[0].User).To(Equal(bob))
			Expect(hits[0].Prompt).To(Equal("a red fox"))

			speech := r.call(bob, "speak", `{"text":"hello","voice":"`+profile.Voice+`"}`)
			Expect(speech.Err).NotTo(HaveOccurred())
			said := decode[localaitools.AudioResult](speech.Out)
			Expect(said.Audio.Ref).To(HavePrefix("/workspace/home/generated/"))
			spoken := r.api.Hits(http.MethodPost, "/v1/audio/speech")
			Expect(spoken).To(HaveLen(1))
			Expect(spoken[0].User).To(Equal(bob))
			Expect(spoken[0].Voice).To(Equal(profile.Voice), "the API got the exact voice string")
			Expect(r.api.backend.count()).To(Equal(1), "the profile resolved and the backend ran")
		})
	})

	Describe("the folder of an agent", func() {
		var r *e2eRig
		BeforeEach(func() {
			r = newE2ERig(scriptCall)
			r.put(bob, "/Proj/in.wav", e2eWAV(2*time.Second))
			r.put(bob, "/Outside/secret.wav", e2eWAV(2*time.Second))
		})
		// chatInFolder opens a chat of an agent that works in /Proj.
		chatInFolder := func() string {
			GinkgoHelper()
			ag, err := r.svc.SaveAgentWithFolder(ctx, bob, agents.Agent{Name: "Scoped", Model: "m"}, &studio.AgentFolderWrite{Path: "/Proj", Mode: agents.FolderWrite})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: ag.ID})
			Expect(err).NotTo(HaveOccurred())
			return rec.ID
		}
		clipArgs := func(ref string) string {
			return `{"action":"create","name":"N","audio":"` + ref + `","transcript":"Hello there."}`
		}

		It("refuses a reference outside the folder, whatever way it is spelled", func() {
			id := chatInFolder()
			for _, ref := range []string{"/workspace/home/Outside/secret.wav", "Outside/secret.wav", "/Outside/secret.wav"} {
				r.sayIn(bob, id, msg("voice_profile", clipArgs(ref)))
			}
			res := r.h.ResultsFor("voice_profile")
			Expect(res).To(HaveLen(3))
			for _, x := range res {
				Expect(x.Err).To(HaveOccurred(), "a file outside the folder was read: %s", x.Out)
			}
			Expect(pending(r, bob)).To(BeZero(), "nothing outside the folder may be proposed")
		})

		It("reads a file inside the folder", func() {
			id := chatInFolder()
			r.sayIn(bob, id, msg("voice_profile", clipArgs("in.wav")))
			res := r.result(id, "voice_profile")
			Expect(res.Err).NotTo(HaveOccurred())
			Expect(res.Out).To(ContainSubstring("Nothing is created yet"))
		})

		It("clones a voice from a clip in the folder: the confirmation reads it through the same fence as the proposal", func() {
			id := chatInFolder()
			r.sayIn(bob, id, msg("voice_profile", clipArgs("in.wav")))
			Expect(r.result(id, "voice_profile").Err).NotTo(HaveOccurred())
			list, err := r.svc.Changes().List(ctx, bob, "pending")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			done, err := r.svc.Changes().Confirm(ctx, bob, list[0].ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(done.Status)).To(Equal("applied"))
			ps, err := r.api.store.List(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(ps).To(HaveLen(1))
			Expect(ps[0].OwnerID).To(Equal(bob))
		})

		It("creates nothing at the confirmation when the chat that proposed the profile is gone", func() {
			id := chatInFolder()
			r.sayIn(bob, id, msg("voice_profile", clipArgs("in.wav")))
			Expect(r.result(id, "voice_profile").Err).NotTo(HaveOccurred())
			list, err := r.svc.Changes().List(ctx, bob, "pending")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(r.svc.Delete(ctx, bob, id)).To(Succeed())
			_, err = r.svc.Changes().Confirm(ctx, bob, list[0].ID)
			Expect(err).To(HaveOccurred())
			ps, perr := r.api.store.List(ctx)
			Expect(perr).NotTo(HaveOccurred())
			Expect(ps).To(BeEmpty())
		})

		It("saves a result inside the folder, not in the rest of the home", func() {
			id := chatInFolder()
			r.sayIn(bob, id, msg("generate_image", `{"prompt":"a red fox"}`))
			res := r.result(id, "generate_image")
			Expect(res.Err).NotTo(HaveOccurred())
			made := decode[localaitools.ImageResult](res.Out)
			Expect(made.Images).To(HaveLen(1))
			name := path.Base(made.Images[0].Ref)
			data, err := r.read(bob, session.GuestRoot+"/"+session.GuestHome+"/Proj/generated/"+name)
			Expect(err).NotTo(HaveOccurred(), "the result is in the agent's folder")
			Expect(data).To(Equal(r.api.png))
			_, err = r.read(bob, session.GuestRoot+"/"+session.GuestHome+"/generated/"+name)
			Expect(err).To(HaveOccurred(), "nothing landed in the rest of the home")
		})

		It("leaves a chat without a folder reading and writing the home as before", func() {
			r.sayIn(bob, create(r.svc, bob), msg("voice_profile", clipArgs("/workspace/home/Outside/secret.wav")))
			Expect(r.h.ResultsFor("voice_profile")[0].Err).NotTo(HaveOccurred())
			res := r.call(bob, "generate_image", `{"prompt":"a red fox"}`)
			Expect(res.Err).NotTo(HaveOccurred())
			made := decode[localaitools.ImageResult](res.Out)
			Expect(made.Images[0].Ref).To(HavePrefix("/workspace/home/generated/"))
			_, err := r.read(bob, made.Images[0].Ref)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("voice cloning", func() {
		var r *e2eRig
		BeforeEach(func() {
			r = newE2ERig(scriptCall)
			r.put(bob, "/clip.wav", e2eWAV(2*time.Second))
			r.put("root", "/clip.wav", e2eWAV(2*time.Second))
		})

		posts := func() []apiHit { return r.api.Hits(http.MethodPost, "/api/voice-profiles") }
		stored := func() []voiceprofile.Profile {
			GinkgoHelper()
			ps, err := r.api.store.List(ctx)
			Expect(err).NotTo(HaveOccurred())
			return ps
		}

		It("creates nothing when the model asks, proposes a card, and creates the profile as the owner only after the person confirms", func() {
			res := r.call(bob, "voice_profile", e2eCreateArgs)
			Expect(res.Err).NotTo(HaveOccurred())
			Expect(res.Out).To(ContainSubstring("Nothing is created yet"))
			Expect(stored()).To(BeEmpty(), "no profile exists before the confirmation")
			Expect(posts()).To(BeEmpty())
			Expect(pending(r, bob)).To(Equal(1))
			list, err := r.svc.Changes().List(ctx, bob, "pending")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(list[0].Kind)).To(Equal("voice_profile"))

			// The person confirms, through the call the confirm endpoint makes.
			done, err := r.svc.Changes().Confirm(ctx, bob, list[0].ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(done.Status)).To(Equal("applied"))
			Expect(posts()).To(HaveLen(1))
			Expect(posts()[0].User).To(Equal(bob))
			made := stored()
			Expect(made).To(HaveLen(1))
			Expect(made[0].OwnerID).To(Equal(bob), "the profile belongs to the user")
			Expect(made[0].Name).To(Equal("Narrator"))

			// list shows the exact voice string.
			listed := decode[localaitools.VoiceProfileList](r.call(bob, "voice_profile", `{"action":"list"}`).Out).Profiles
			Expect(listed).To(HaveLen(1))
			Expect(listed[0].Voice).To(Equal(voiceprofile.Reference(made[0].ID)))

			// The owner speaks with it.
			ok := r.call(bob, "speak", `{"text":"hello","voice":"`+listed[0].Voice+`"}`)
			Expect(ok.Err).NotTo(HaveOccurred())
			Expect(r.api.backend.count()).To(Equal(1))

			// Another user gets "not found" for the same string and nothing runs.
			other := r.call(alice, "speak", `{"text":"hello","voice":"`+listed[0].Voice+`"}`)
			Expect(said(other)).To(ContainSubstring("no such voice profile (404)"))
			Expect(r.api.backend.count()).To(Equal(1), "the backend did not run for the other user")
			theirs := decode[localaitools.VoiceProfileList](r.call(alice, "voice_profile", `{"action":"list"}`).Out).Profiles
			Expect(theirs).To(BeEmpty())

			// The other user cannot delete it either: the same 404.
			gone := r.call(alice, "voice_profile", `{"action":"delete","id":"`+made[0].ID+`"}`)
			Expect(said(gone)).To(ContainSubstring("no such profile (404)"))
			Expect(stored()).To(HaveLen(1))

			// An administrator can.
			del := r.call("root", "voice_profile", `{"action":"delete","id":"`+made[0].ID+`"}`)
			Expect(del.Err).NotTo(HaveOccurred())
			Expect(del.Out).To(MatchRegexp(`"deleted":\s*true`))
			Expect(stored()).To(BeEmpty())
		})

		It("creates nothing when the person dismisses the card", func() {
			r.call(bob, "voice_profile", e2eCreateArgs)
			list, err := r.svc.Changes().List(ctx, bob, "pending")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			_, err = r.svc.Changes().Dismiss(ctx, bob, list[0].ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(stored()).To(BeEmpty())
			Expect(posts()).To(BeEmpty())
		})

		It("refuses the same call in a run that nobody watches, in a sub-agent and in a messaging chat, and proposes nothing", func() {
			// A job with act.spend reaches the tool, which refuses; a trigger
			// without a policy is stopped by the act gate first.
			for _, origin := range []string{session.OriginJob, session.OriginTrigger} {
				saved, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Auto-" + origin, Model: "m", Builtins: []string{builtins.GrantActSpend}})
				Expect(err).NotTo(HaveOccurred())
				rec, err := r.svc.CreateAs(ctx, bob, studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: origin, ID: "x"})
				Expect(err).NotTo(HaveOccurred())
				r.sayIn(bob, rec.ID, msg("voice_profile", e2eCreateArgs))
				Expect(said(r.result(rec.ID, "voice_profile"))).To(Or(ContainSubstring("nobody can confirm"), ContainSubstring("I did not run")), origin)
			}

			// A plain sub-agent has only the tools its parent chose, so the
			// sub-agent that can reach the tool is one started from a saved agent.
			_, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "worker", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			parent := r.say(bob, spawnMsg(msg("voice_profile", e2eCreateArgs), "worker"), func(id string) {
				Expect(r.svc.SetBuiltinsOn(ctx, bob, id, []string{builtins.GroupSavedAgents})).To(Succeed())
			})
			kids := childrenOf(r.svc, bob, parent)
			Expect(kids).To(HaveLen(1))
			Expect(r.result(kids[0].ID, "voice_profile").Out).To(ContainSubstring("sub-agent"))

			saved, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Bot", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.CreateAs(ctx, bob, studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: session.OriginConnector, ID: "x"})
			Expect(err).NotTo(HaveOccurred())
			r.sayIn(bob, rec.ID, msg("voice_profile", e2eCreateArgs))
			Expect(r.result(rec.ID, "voice_profile").Out).To(ContainSubstring("not in a messaging chat"))

			Expect(pending(r, bob)).To(BeZero(), "no card was proposed")
			Expect(stored()).To(BeEmpty())
			Expect(posts()).To(BeEmpty())
		})

		It("rejects a clip that is not a WAV file before it asks the person", func() {
			r.put(bob, "/clip.mp3", []byte("ID3 not a wav file at all"))
			res := r.call(bob, "voice_profile", strings.Replace(e2eCreateArgs, "clip.wav", "clip.mp3", 1))
			Expect(said(res)).To(ContainSubstring("must be a WAV file"))
			Expect(pending(r, bob)).To(BeZero())
		})
	})

	Describe("which chat has which group", func() {
		saveAgent := func(r *e2eRig, owner string, a agents.Agent) agents.Agent {
			GinkgoHelper()
			a.Model = "m"
			saved, err := r.svc.SaveAgent(ctx, owner, a)
			Expect(err).NotTo(HaveOccurred())
			return saved
		}
		agentChat := func(r *e2eRig, owner string, a agents.Agent, origin string) string {
			GinkgoHelper()
			var rec session.ChatRecord
			var err error
			if origin == "" {
				rec, err = r.svc.Create(ctx, owner, studio.CreateInput{Model: "m", AgentID: a.ID})
			} else {
				rec, err = r.svc.CreateAs(ctx, owner, studio.CreateInput{Model: "m", AgentID: a.ID}, studio.Origin{Kind: origin, ID: "x"})
			}
			Expect(err).NotTo(HaveOccurred())
			r.sayIn(owner, rec.ID, "hi")
			return rec.ID
		}

		It("gives an administrator both groups in an opt-in agent and the client group alone in a plain one", func() {
			r := newE2ERig(scriptCall)
			admin := r.adminNames()
			on := saveAgent(r, "root", agents.Agent{Name: "On", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			plain := saveAgent(r, "root", agents.Agent{Name: "Plain"})

			names := r.toolsOf(agentChat(r, "root", on, ""))
			Expect(names).To(ContainElements(clientNames()))
			Expect(names).To(ContainElements(admin))

			names = r.toolsOf(agentChat(r, "root", plain, ""))
			Expect(names).To(ContainElements(clientNames()))
			for _, n := range admin {
				Expect(names).NotTo(ContainElement(n))
			}
		})

		It("never gives a sub-agent the admin group, and gives a messaging chat the client group but not the admin group", func() {
			r := newE2ERig(scriptCall)
			admin := r.adminNames()
			rec, err := r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", Assistant: true})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "worker", Model: "m", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.svc.SetBuiltinsOn(ctx, "root", rec.ID, []string{builtins.GroupLocalAIAdmin, builtins.GroupSavedAgents})).To(Succeed())
			// One sub-agent from a plain task, one from a saved agent that
			// switched the admin group on.
			r.sayIn("root", rec.ID, spawnMsg("hello", ""))
			r.sayIn("root", rec.ID, spawnMsg("hello again", "worker"))
			Expect(r.toolsOf(rec.ID)).To(ContainElements(admin), "the parent has the admin group")
			kids := childrenOf(r.svc, "root", rec.ID)
			Expect(kids).To(HaveLen(2))
			for _, k := range kids {
				names := r.toolsOf(k.ID)
				if k.AgentID != "" {
					Expect(names).To(ContainElement("generate_image"), "a saved agent follows the client switches")
				}
				for _, n := range admin {
					Expect(names).NotTo(ContainElement(n), "a sub-agent got an admin tool")
				}
			}

			on := saveAgent(r, "root", agents.Agent{Name: "OnBot", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			names := r.toolsOf(agentChat(r, "root", on, session.OriginConnector))
			Expect(names).To(ContainElements(clientNames()))
			for _, n := range admin {
				Expect(names).NotTo(ContainElement(n), "a messaging chat got an admin tool")
			}
		})

		It("removes the client group with builtins_off, and for everyone with the instance setting off", func() {
			r := newE2ERig(scriptCall)
			off := saveAgent(r, "root", agents.Agent{Name: "Off", BuiltinsOff: []string{builtins.GroupLocalAIClient}})
			Expect(r.toolsOf(agentChat(r, "root", off, ""))).NotTo(ContainElement("generate_image"))

			r.set(func() { r.clientOn = false })
			for _, u := range []string{"root", bob} {
				id := r.say(u, "hi")
				for _, n := range clientNames() {
					Expect(r.toolsOf(id)).NotTo(ContainElement(n), u)
				}
			}
		})
	})

	Describe("the tool search", func() {
		It("shows only the three bridge tools for the deferred groups, and the typed tools when it is off", func() {
			r := newE2ERig(scriptCall)
			r.set(func() { r.search = config.ToolSearchOn })
			admin := r.adminNames()
			id := r.say("root", "hi", func(id string) {
				Expect(r.svc.SetBuiltinsOn(ctx, "root", id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
			})
			names := r.toolsOf(id)
			Expect(names).To(ContainElements(bridgeNames))
			Expect(names).To(ContainElement("search"), "a tool of another source stays typed")
			for _, n := range append(clientNames(), admin...) {
				Expect(names).NotTo(ContainElement(n), n)
			}

			r.set(func() { r.search = config.ToolSearchOff })
			names = r.toolsOf(r.say(bob, "hi"))
			Expect(names).To(ContainElements(clientNames()))
			Expect(names).NotTo(ContainElement("tool_search"))
		})

		It("finds generate_image, describes it, and runs it as the user of the chat", func() {
			r := newE2ERig(scriptCall)
			r.set(func() { r.search = config.ToolSearchOn })

			found := r.call(bob, "tool_search", `{"query":"make a picture"}`)
			Expect(found.Err).NotTo(HaveOccurred())
			Expect(found.Out).To(ContainSubstring("generate_image:"))
			Expect(found.Out).NotTo(ContainSubstring("install_model"))

			described := r.call(bob, "tool_describe", `{"name":"generate_image"}`)
			Expect(described.Err).NotTo(HaveOccurred())
			Expect(described.Out).To(ContainSubstring(`"name":"generate_image"`))
			Expect(described.Out).To(ContainSubstring(`"prompt"`))

			ran := r.call(bob, "tool_call", `{"name":"generate_image","arguments":{"prompt":"a lighthouse"}}`)
			Expect(ran.Err).NotTo(HaveOccurred())
			made := decode[localaitools.ImageResult](ran.Out)
			Expect(made.Images).To(HaveLen(1))
			data, err := r.read(bob, made.Images[0].Ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(r.api.png))
			hits := r.api.Hits(http.MethodPost, "/v1/images/generations")
			Expect(hits).To(HaveLen(1))
			Expect(hits[0].User).To(Equal(bob))
		})

		It("answers a call through the bridge as it answers a direct call when the API refuses the account", func() {
			r := newE2ERig(scriptCall)
			r.api.deny(bob, auth.FeatureImages) // the chat offers the tool; the API refuses it

			direct := r.call(bob, "generate_image", `{"prompt":"x"}`)
			r.set(func() { r.search = config.ToolSearchOn })
			bridged := r.call(bob, "tool_call", `{"name":"generate_image","arguments":{"prompt":"x"}}`)

			Expect(said(direct)).To(ContainSubstring("403"))
			Expect(said(bridged)).To(Equal(said(direct)))
			hits := r.api.Hits(http.MethodPost, "/v1/images/generations")
			Expect(hits).To(HaveLen(2))
			for _, h := range hits {
				Expect(h.User).To(Equal(bob))
				Expect(h.Status).To(Equal(http.StatusForbidden))
			}
		})

		It("answers a call of an admin tool by a user as it answers an unknown name, and nothing reaches the API", func() {
			r := newE2ERig(scriptCall)
			r.set(func() { r.search = config.ToolSearchOn })
			optIn := func(id string) {
				Expect(r.svc.SetBuiltinsOn(ctx, bob, id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
			}
			before := r.api.total()
			admin := r.call(bob, "tool_call", `{"name":"install_model","arguments":{"id":"x"}}`, optIn)
			unknown := r.call(bob, "tool_call", `{"name":"no_such_tool","arguments":{"id":"x"}}`, optIn)
			Expect(admin.Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(strings.ReplaceAll(said(admin), "install_model", "X")).To(Equal(strings.ReplaceAll(said(unknown), "no_such_tool", "X")))
			Expect(r.api.total()).To(Equal(before), "nothing reached the API")
			found := r.call(bob, "tool_search", `{"query":"install model"}`, optIn)
			Expect(found.Out).NotTo(ContainSubstring("install_model"))
			desc := r.call(bob, "tool_describe", `{"name":"install_model"}`, optIn)
			Expect(desc.Err).To(MatchError(ContainSubstring("no such tool")))
		})
	})

	Describe("a run that nobody watches", func() {
		for _, mode := range []string{config.ToolSearchOff, config.ToolSearchOn} {
			mode := mode
			It("gates generate_image by act.spend, with the tool search "+mode, func() {
				r := newE2ERig(scriptCall)
				r.set(func() { r.search = mode })
				args := `{"prompt":"a cat"}`
				tool, send := "generate_image", msg("generate_image", args)
				if mode == config.ToolSearchOn {
					tool, send = "tool_call", msg("tool_call", `{"name":"generate_image","arguments":`+args+`}`)
				}
				run := func(a agents.Agent) string {
					GinkgoHelper()
					saved, err := r.svc.SaveAgent(ctx, "root", a)
					Expect(err).NotTo(HaveOccurred())
					rec, err := r.svc.CreateAs(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: session.OriginJob, ID: "x"})
					Expect(err).NotTo(HaveOccurred())
					r.sayIn("root", rec.ID, send)
					return said(r.result(rec.ID, tool))
				}

				refused := run(agents.Agent{Name: "Poor", Model: "m"})
				Expect(refused).To(ContainSubstring("act.spend"))
				Expect(r.api.Hits(http.MethodPost, "/v1/images/generations")).To(BeEmpty(), "the gate stops it before the API")

				allowed := run(agents.Agent{Name: "Rich", Model: "m", Builtins: []string{builtins.GrantActSpend}})
				made := decode[localaitools.ImageResult](allowed)
				Expect(made.Images).To(HaveLen(1))
				hits := r.api.Hits(http.MethodPost, "/v1/images/generations")
				Expect(hits).To(HaveLen(1))
				Expect(hits[0].User).To(Equal("root"))
				_, err := r.read("root", made.Images[0].Ref)
				Expect(err).NotTo(HaveOccurred())
			})
		}
	})

	Describe("the size of the prompt and the tools", func() {
		// These mirror the budgets that the localaitools specs enforce on the
		// MCP server (compactBudgetBytes in prompts_test.go and
		// clientBudgetBytes in clienttools_merge_test.go). They are
		// unexported there; keep the values the same.
		const compactBudget, clientBudget = 4700, 6600

		It("keeps an administrator's turn under the admin budgets and the client group under its own", func() {
			r := newE2ERig(scriptCall)
			id := r.say("root", "hi", func(id string) {
				Expect(r.svc.SetBuiltinsOn(ctx, "root", id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
			})
			Expect(r.toolsOf(id)).To(ContainElement("install_model"))
			compact := r.holder.CompactSystemPrompt()
			Expect(compact).NotTo(BeEmpty())
			Expect(len(compact)).To(BeNumerically("<=", compactBudget))
			prompt := r.h.SystemPrompt(id) + r.h.TurnPrompt(id)
			Expect(prompt).To(ContainSubstring("LocalAI Assistant"), "the admin prompt is part of the turn")
			Expect(len(prompt)).To(BeNumerically("<=", adminTurnPromptBudget),
				"every turn of an admin chat pays this prompt")

			// The client tools as the model gets them: name, description, schema.
			total := 0
			for _, n := range clientNames() {
				total += len(n) + len(r.h.LastToolDescription(n)) + len(r.h.ToolSchema(id, n))
			}
			GinkgoWriter.Printf("admin turn prompt %d bytes, client tools %d bytes\n", len(prompt), total)
			Expect(total).To(BeNumerically("<=", clientBudget))
			Expect(total).To(BeNumerically(">", 1000), "the measure saw the tools")
		})
	})
})
