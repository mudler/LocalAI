// SPDX-License-Identifier: MIT
package musicapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai/musicrequest"
	"github.com/mudler/LocalAI/core/services/music"
	"github.com/mudler/LocalAI/core/services/music/outputstore"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

const exactABC = "X:1\r\nT:Exact score\r\nM:4/4\r\nL:1/4\r\nK:C\r\nC D E F | G4 |\r\n"

type resolverFunc func(context.Context, music.ValidatedRequest) (music.ValidatedRequest, error)

func (f resolverFunc) ResolveMusic(c context.Context, r music.ValidatedRequest) (music.ValidatedRequest, error) {
	return f(c, r)
}

type stageBackend struct {
	store          *outputstore.Store
	mu             sync.Mutex
	requests       []music.StageRequest
	failGeneration bool
	hold           chan struct{}
	entered        chan struct{}
	settlementGate chan struct{}
	ended          chan struct{}
}

func (b *stageBackend) Admit(_ context.Context, r music.StageRequest) (music.StageLease, error) {
	b.mu.Lock()
	b.requests = append(b.requests, r)
	b.mu.Unlock()
	return &stageLease{b}, nil
}
func (b *stageBackend) CheckGeneration(_ context.Context, r music.ValidatedRequest) (music.Selection, error) {
	return r.Generation, nil
}

type stageLease struct{ b *stageBackend }

func (l *stageLease) Release(context.Context) music.CleanupTask { return ack(nil) }

type execution struct {
	out     chan music.StageOutcome
	settled chan error
	once    sync.Once
	stop    chan struct{}
}

func (e *execution) Outcome() <-chan music.StageOutcome { return e.out }
func (e *execution) Settled() <-chan error              { return e.settled }
func (e *execution) Stop()                              { e.once.Do(func() { close(e.stop) }) }
func (l *stageLease) Start(ctx context.Context, r music.StageRequest) (music.StageExecution, error) {
	l.b.mu.Lock()
	l.b.requests[len(l.b.requests)-1] = r
	l.b.mu.Unlock()
	e := &execution{out: make(chan music.StageOutcome, 1), settled: make(chan error, 1), stop: make(chan struct{})}
	go func() {
		if l.b.hold != nil {
			close(l.b.entered)
			select {
			case <-l.b.hold:
			case <-e.stop:
			case <-ctx.Done():
			}
			e.out <- music.StageOutcome{Err: ctx.Err()}
			if l.b.settlementGate != nil {
				<-l.b.settlementGate
			}
			e.settled <- nil
			if l.b.ended != nil {
				close(l.b.ended)
			}
			return
		}
		if r.Stage == music.TranscriptionStage {
			s := exactABC
			e.out <- music.StageOutcome{ABC: &s, Valid: true}
		} else if l.b.failGeneration {
			e.out <- music.StageOutcome{Err: errors.New("native /secret/path worker:9000")}
		} else {
			c, err := l.b.store.ImportCandidate(ctx, r.RequestID, bytes.NewReader(testWAV()))
			e.out <- music.StageOutcome{Candidate: c, Valid: err == nil, Err: err}
		}
		<-e.stop
		e.settled <- nil
	}()
	return e, nil
}
func testWAV() []byte {
	b := new(bytes.Buffer)
	b.WriteString("RIFF")
	_ = binary.Write(b, binary.LittleEndian, uint32(36+1600))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(b, binary.LittleEndian, uint32(16))
	_ = binary.Write(b, binary.LittleEndian, uint16(1))
	_ = binary.Write(b, binary.LittleEndian, uint16(1))
	_ = binary.Write(b, binary.LittleEndian, uint32(8000))
	_ = binary.Write(b, binary.LittleEndian, uint32(16000))
	_ = binary.Write(b, binary.LittleEndian, uint16(2))
	_ = binary.Write(b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(b, binary.LittleEndian, uint32(1600))
	b.Write(make([]byte, 1600))
	return b.Bytes()
}

var _ = Describe("real Runner HTTP lifecycle (state proof, not inference)", func() {
	var dir string
	var uploads *Uploads
	var store *outputstore.Store
	var objects *outputstore.PrivateFilesystem
	var bridge *PrivateBridge
	var cfg Config
	var backend *stageBackend
	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "musicapi-fixture-")
		Expect(err).NotTo(HaveOccurred())
		uploads, err = NewUploads(filepath.Join(dir, "uploads"), time.Now)
		Expect(err).NotTo(HaveOccurred())
		objects, err = outputstore.NewPrivateFilesystem(filepath.Join(dir, "objects"))
		Expect(err).NotTo(HaveOccurred())
		db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "ledger.db")), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sql, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sql.Close)
		store, err = outputstore.New(outputstore.Config{DB: db, Objects: objects, StagingRoot: filepath.Join(dir, "staging"), Clock: time.Now, ValidateWAV: func(ctx context.Context, r io.ReadSeeker, size int64) error {
			data, e := io.ReadAll(r)
			if len(data) != len(testWAV()) || size != int64(len(data)) {
				return errors.New("invalid fixture WAV")
			}
			return e
		}})
		Expect(err).NotTo(HaveOccurred())
		bridge, err = NewPrivateBridge(uploads, store)
		Expect(err).NotTo(HaveOccurred())
		p, err := musicrequest.New(music.ValidateABC, uploads)
		Expect(err).NotTo(HaveOccurred())
		backend = &stageBackend{store: store}
		cfg = Config{Parser: p, Bridge: bridge, Clock: time.Now, Runner: &music.Runner{Backend: backend, Storage: bridge, Publisher: bridge}, Resolver: resolverFunc(func(ctx context.Context, in music.ValidatedRequest) (music.ValidatedRequest, error) {
			Expect(in.Feature).To(BeFalse())
			Expect(in.InputsValid).To(BeFalse())
			Expect(in.StorageReady).To(BeFalse())
			Expect(in.Transcription.Authorized).To(BeFalse())
			Expect(in.Generation.Authorized).To(BeFalse())
			if in.Operation != music.Generation {
				in.Transcription = music.Selection{CanonicalModel: "canonical-transcriber", LoadedIdentity: "canonical-transcriber", Authorized: true, Capable: true, IdentityMatches: true, ContextFits: true}
			}
			if in.Operation != music.Transcription {
				in.Generation = music.Selection{CanonicalModel: "canonical-generator", LoadedIdentity: "canonical-generator", Authorized: true, Capable: true, IdentityMatches: true, ContextFits: true}
			}
			in.Feature = true
			in.Quota = true
			in.InputsValid = true
			in.StorageReady = true
			return in, nil
		})}
	})
	AfterEach(func() {
		Expect(uploads.Close()).To(Succeed())
		Expect(store.Close()).To(Succeed())
		Expect(objects.Close()).To(Succeed())
		Expect(os.RemoveAll(dir)).To(Succeed())
	})
	call := func(op music.Operation, abc *string, ctx context.Context, user *auth.User) (*httptest.ResponseRecorder, error) {
		var body bytes.Buffer
		content := "application/json"
		if op == music.Generation {
			v := map[string]any{"model": "alias", "style": "folk", "lyrics": ""}
			if abc != nil {
				v["abc"] = *abc
			}
			Expect(json.NewEncoder(&body).Encode(v)).To(Succeed())
		} else {
			w := multipart.NewWriter(&body)
			Expect(w.WriteField("model", "alias")).To(Succeed())
			if op == music.Cover {
				Expect(w.WriteField("transcription_model", "transcriber-alias")).To(Succeed())
				Expect(w.WriteField("style", "folk")).To(Succeed())
				Expect(w.WriteField("lyrics", "")).To(Succeed())
			}
			f, err := w.CreateFormFile("audio", "../../not-authority.wav")
			Expect(err).NotTo(HaveOccurred())
			_, err = f.Write(testWAV())
			Expect(err).NotTo(HaveOccurred())
			Expect(w.Close()).To(Succeed())
			content = w.FormDataContentType()
		}
		r := httptest.NewRequest("POST", "/v1/music", &body).WithContext(ctx)
		r.Host = "attacker.example"
		r.Header.Set("Content-Type", content)
		w := httptest.NewRecorder()
		c := echo.New().NewContext(r, w)
		if user != nil {
			c.Set("auth_user", user)
		}
		h := NewHandlers(cfg)
		f := h.Transcriptions
		if op == music.Generation {
			f = h.Generations
		}
		if op == music.Cover {
			f = h.Covers
		}
		err := f(c)
		return w, err
	}
	It("returns exact transcribed ABC only after upload cleanup and uses canonical model", func() {
		w, e := call(music.Transcription, nil, context.Background(), nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Code).To(Equal(200))
		var v map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &v)).To(Succeed())
		Expect(v["abc"]).To(Equal(exactABC))
		Expect(v["model"]).To(Equal("canonical-transcriber"))
		Expect(v).To(HaveLen(3))
		files, e := os.ReadDir(filepath.Join(dir, "uploads"))
		Expect(e).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
	It("generates independently from edited ABC and returns protected origin-relative URL", func() {
		a := strings.Replace(exactABC, "C D E F", "F E D C", 1)
		w, e := call(music.Generation, &a, context.Background(), nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Code).To(Equal(200))
		var v map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &v)).To(Succeed())
		Expect(v["abc"]).To(Equal(a))
		Expect(v["abc_source"]).To(Equal("provided"))
		audio := v["audio"].(map[string]any)
		Expect(audio["url"]).To(HavePrefix("/v1/music/outputs/"))
		Expect(w.Body.String()).NotTo(ContainSubstring("attacker"))
		Expect(backend.requests).To(HaveLen(1))
		Expect(*backend.requests[0].Generation.ABC).To(Equal(a))
	})
	It("preserves cover ABC after real reducer generation failure with no path leak", func() {
		backend.failGeneration = true
		w, e := call(music.Cover, nil, context.Background(), nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Code).To(Equal(502))
		var v struct {
			Error struct {
				ABC    string
				Code   int
				Stage  string
				Reason string
			}
		}
		Expect(json.Unmarshal(w.Body.Bytes(), &v)).To(Succeed())
		Expect(v.Error.ABC).To(Equal(exactABC))
		Expect(v.Error.Code).To(Equal(502))
		Expect(v.Error.Stage).To(Equal("generation"))
		Expect(v.Error.Reason).To(Equal("backend_failed"))
		Expect(w.Body.String()).NotTo(ContainSubstring("secret"))
		Expect(backend.requests).To(HaveLen(2))
		Expect(*backend.requests[1].Generation.ABC).To(Equal(exactABC))
	})
	It("returns cover success using the exact transcribed score", func() {
		w, e := call(music.Cover, nil, context.Background(), nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Code).To(Equal(200))
		var v map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &v)).To(Succeed())
		Expect(v["abc"]).To(Equal(exactABC))
		Expect(v["abc_source"]).To(Equal("transcribed"))
		Expect(v["transcription_model"]).To(Equal("canonical-transcriber"))
	})
	It("uses the actual auth-context principal rather than body identity", func() {
		cfg.AuthEnabled = true
		w, e := call(music.Generation, nil, context.Background(), &auth.User{ID: "alice", Role: auth.RoleAdmin})
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Code).To(Equal(200))
		var v struct{ Audio struct{ URL string } }
		Expect(json.Unmarshal(w.Body.Bytes(), &v)).To(Succeed())
		id := strings.Split(v.Audio.URL, "/")[4]
		m, e := store.Lookup(context.Background(), id)
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Principal).To(Equal("alice"))
	})
	It("joins actual stage settlement on deadline and returns 504 without a URL", func() {
		backend.hold = make(chan struct{})
		backend.entered = make(chan struct{})
		backend.settlementGate = make(chan struct{})
		backend.ended = make(chan struct{})
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		type response struct {
			w *httptest.ResponseRecorder
			e error
		}
		done := make(chan response, 1)
		go func() {
			defer GinkgoRecover()
			w, e := call(music.Transcription, nil, ctx, nil)
			done <- response{w, e}
		}()
		Eventually(backend.entered, time.Second).Should(BeClosed())
		<-ctx.Done()
		Consistently(done, 40*time.Millisecond).ShouldNot(Receive())
		close(backend.settlementGate)
		var out response
		select {
		case out = <-done:
		case <-time.After(3 * time.Second):
			b := make([]byte, 1<<20)
			n := runtime.Stack(b, true)
			Fail(string(b[:n]))
		}
		Expect(out.e).NotTo(HaveOccurred())
		Expect(out.w.Code).To(Equal(504))
		Expect(out.w.Body.String()).NotTo(ContainSubstring(`"audio"`))
		Eventually(backend.ended).Should(BeClosed())
	})

	It("returns cancellation without writing a fake 499 or JSON response", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w, e := call(music.Transcription, nil, ctx, nil)
		Expect(e).To(MatchError(context.Canceled))
		Expect(w.Body.Len()).To(BeZero())
	})
	It("denies either model before any stage when resolver rejects cover", func() {
		cfg.Resolver = resolverFunc(func(context.Context, music.ValidatedRequest) (music.ValidatedRequest, error) {
			return music.ValidatedRequest{}, publicFailure{403, "validation", "access_denied"}
		})
		w, e := call(music.Cover, nil, context.Background(), nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Code).To(Equal(403))
		Expect(backend.requests).To(BeEmpty())
	})
	It("does not authorize an arbitrary upload object with a copied Ref", func() {
		id, _ := randomID()
		ctx := context.WithValue(context.Background(), entryKey{}, entry{id, time.Now().Add(time.Minute)})
		u, e := uploads.Create(ctx)
		Expect(e).NotTo(HaveOccurred())
		_, e = u.Writer.Write(testWAV())
		Expect(e).NotTo(HaveOccurred())
		Expect(u.Writer.Close()).To(Succeed())
		fake := *u
		_, e = bridge.Bind(ctx, music.ValidatedRequest{ID: id, Operation: music.Transcription, Principal: "owner", Deadline: time.Now().Add(time.Minute)}, &fake)
		Expect(e).To(HaveOccurred())
		Expect(u.Cleanup().Wait()).To(Succeed())
	})
	It("rejects repeat Bind without overwriting the original cleanup gate", func() {
		id, _ := randomID()
		ctx := context.WithValue(context.Background(), entryKey{}, entry{id, time.Now().Add(time.Minute)})
		v, e := uploads.Create(ctx)
		Expect(e).NotTo(HaveOccurred())
		_, e = v.Writer.Write(testWAV())
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Writer.Close()).To(Succeed())
		in := music.ValidatedRequest{ID: id, Principal: "owner", Operation: music.Transcription, Deadline: time.Now().Add(time.Minute)}
		source, e := bridge.Bind(ctx, in, v)
		Expect(e).NotTo(HaveOccurred())
		gate := bridge.inputs[id]
		_, e = bridge.Bind(ctx, in, v)
		Expect(e).To(HaveOccurred())
		Expect(bridge.inputs[id]).To(BeIdenticalTo(gate))
		Expect(bridge.CleanInput(context.Background(), id, source).Wait()).To(Succeed())
		Expect(bridge.Finish(context.Background(), id).Wait()).To(Succeed())
		Expect(v.Cleanup().Wait()).To(Succeed())
	})

	It("admits at most one concurrent Bind and retains its exact source and gate", func() {
		id, _ := randomID()
		ctx := context.WithValue(context.Background(), entryKey{}, entry{id, time.Now().Add(time.Minute)})
		v, e := uploads.Create(ctx)
		Expect(e).NotTo(HaveOccurred())
		_, e = v.Writer.Write(testWAV())
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Writer.Close()).To(Succeed())
		in := music.ValidatedRequest{ID: id, Principal: "owner", Operation: music.Transcription, Deadline: time.Now().Add(time.Minute)}
		type binding struct {
			source music.OwnedInput
			err    error
		}
		out := make(chan binding, 2)
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			go func() { <-start; source, err := bridge.Bind(ctx, in, v); out <- binding{source, err} }()
		}
		close(start)
		first, second := <-out, <-out
		var source music.OwnedInput
		if first.err == nil {
			Expect(second.err).To(HaveOccurred())
			source = first.source
		} else {
			Expect(second.err).NotTo(HaveOccurred())
			source = second.source
		}
		Expect(bridge.sources[id]).To(Equal(source))
		gate := bridge.inputs[id]
		Expect(gate).NotTo(BeNil())
		Expect(gate).NotTo(BeClosed())
		Expect(bridge.CleanInput(ctx, id, source).Wait()).To(Succeed())
		Expect(gate).To(BeClosed())
		Expect(bridge.Finish(ctx, id).Wait()).To(Succeed())
		Expect(v.Cleanup().Wait()).To(Succeed())
	})

})

type publicFailure struct {
	status        int
	stage, reason string
}

func (e publicFailure) Error() string       { return fmt.Sprintf("private diagnostic %d", e.status) }
func (e publicFailure) HTTPStatus() int     { return e.status }
func (e publicFailure) MusicStage() string  { return e.stage }
func (e publicFailure) MusicReason() string { return e.reason }
