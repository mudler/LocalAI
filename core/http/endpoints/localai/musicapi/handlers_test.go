// SPDX-License-Identifier: MIT
package musicapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai/musicrequest"
	"github.com/mudler/LocalAI/core/services/music"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMusicAPI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Music HTTP orchestration") }

var _ = Describe("POST boundary", func() {
	var p *musicrequest.Parser
	BeforeEach(func() {
		var err error
		p, err = musicrequest.New(music.ValidateABC, nil)
		Expect(err).NotTo(HaveOccurred())
	})
	invoke := func(cfg Config, body string, ctx context.Context) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/music/generations", strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Request-ID", "client-controlled")
		w := httptest.NewRecorder()
		e := echo.New()
		c := e.NewContext(r, w)
		err := NewHandlers(cfg).Generations(c)
		if err != nil {
			e.HTTPErrorHandler(err, c)
		}
		return w
	}
	It("rejects malformed input with numeric validation error and server request ID", func() {
		w := invoke(Config{Parser: p, Clock: time.Now}, `{"model":1}`, context.Background())
		Expect(w.Code).To(Equal(400))
		Expect(w.Body.String()).To(ContainSubstring(`"stage":"validation"`))
		Expect(w.Body.String()).To(ContainSubstring(`"code":400`))
		Expect(w.Body.String()).NotTo(ContainSubstring("client-controlled"))
	})
	It("fails closed when production resolver is absent", func() {
		w := invoke(Config{Parser: p, Clock: time.Now}, `{"model":"alias","style":"folk","lyrics":""}`, context.Background())
		Expect(w.Code).To(Equal(503))
		Expect(w.Body.String()).To(ContainSubstring("unavailable"))
	})
	It("denies anonymous access in configured auth mode before parsing", func() {
		w := invoke(Config{Parser: p, Clock: time.Now, AuthEnabled: true}, `broken`, context.Background())
		Expect(w.Code).To(Equal(401))
	})
	It("honors an expired parent deadline before parsing", func() {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		w := invoke(Config{Parser: p, Clock: time.Now}, `broken`, ctx)
		Expect(w.Code).To(Equal(504))
	})
})

var _ = Describe("exact public error mapping", func() {
	DescribeTable("maps reducer domain codes without native string heuristics", func(code string, status int, reason string) {
		w := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), w)
		a := exactABC
		Expect(emitError(c, "server", context.Background(), music.Result{StageError: code, ABC: &a}, errors.New("native /secret token=hidden"), "generation")).To(Succeed())
		Expect(w.Code).To(Equal(status))
		var v struct {
			Error struct {
				Code        int
				Reason, ABC string
			}
		}
		Expect(json.Unmarshal(w.Body.Bytes(), &v)).To(Succeed())
		Expect(v.Error.Code).To(Equal(status))
		Expect(v.Error.Reason).To(Equal(reason))
		Expect(v.Error.ABC).To(Equal(a))
		Expect(w.Body.String()).NotTo(ContainSubstring("secret"))
	}, Entry("capacity", "capacity_429", 429, "capacity_unavailable"), Entry("backend", "backend", 502, "backend_failed"), Entry("fetch", "fetch_validation", 502, "output_invalid"), Entry("prepare", "publication_prepare", 500, "publication_failed"), Entry("commit", "publication_commit", 500, "publication_failed"), Entry("effect", "effect", 500, "effect_failed"), Entry("unknown", "unrecognized", 500, "internal_error"))
	It("uses errors.As for an approved typed public error", func() {
		w := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), w)
		Expect(emitError(c, "server", context.Background(), music.Result{}, errors.Join(errors.New("private"), publicFailure{422, "validation", "context_limit"}), "generation")).To(Succeed())
		Expect(w.Code).To(Equal(422))
		Expect(w.Body.String()).To(ContainSubstring("context_limit"))
		Expect(w.Body.String()).NotTo(ContainSubstring("private"))
	})
	It("does not expose arbitrary collaborator reason text", func() {
		w := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest("POST", "/", nil), w)
		Expect(emitError(c, "server", context.Background(), music.Result{}, publicFailure{422, "validation", "/private/path"}, "generation")).To(Succeed())
		Expect(w.Code).To(Equal(500))
		Expect(w.Body.String()).NotTo(ContainSubstring("private"))
	})
})
