// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	studioendpoint "github.com/mudler/LocalAI/core/http/endpoints/studio"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

var _ = Describe("the public hook of an event trigger", func() {
	const secret = "s3cret-value-for-the-hook"
	const notFound = `{"error":"not found"}`
	ctx := context.Background()
	var s *studioServer

	var clock time.Time
	BeforeEach(func() {
		clock = time.Now()
		studioRouteOptions = func(*studioServer) []studioendpoint.HandlerOption {
			return []studioendpoint.HandlerOption{studioendpoint.WithClock(func() time.Time { return clock })}
		}
		DeferCleanup(func() { studioRouteOptions = nil })
		s = newStudioServer()
	})

	put := func(id string, mod func(*triggers.Trigger)) {
		t := triggers.Trigger{ID: id, Owner: "alice", Name: "Hook " + id, Prompt: "Look at the event.", Kind: triggers.KindEvent, Enabled: true,
			Source: "example-hook", EventType: "push", Secret: secret, Model: "test-model"}
		if mod != nil {
			mod(&t)
		}
		Expect(s.trigStore.PutTrigger(ctx, t)).To(Succeed())
	}
	runs := func(id string) int {
		rs, err := s.trigStore.ListRuns(ctx, id, 0)
		Expect(err).NotTo(HaveOccurred())
		return len(rs)
	}
	// call sends an unauthenticated-by-session POST; set adds the headers.
	call := func(id, query, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/chats/triggers/"+id+"/hook"+query, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if set != nil {
			set(req)
		}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		return rec
	}
	bearer := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+v) }
	}
	push := `{"type":"push","ref":"main"}`

	It("starts one run for the right bearer secret and answers with the run", func() {
		put("t1", nil)
		rec := call("t1", "?source=example-hook", push, bearer(secret))
		Expect(rec.Code).To(Equal(http.StatusAccepted), rec.Body.String())
		Expect(rec.Body.String()).To(MatchRegexp(`^\{"run":"[^"]+"\}`))
		Expect(runs("t1")).To(Equal(1))
	})

	It("stops a confirmed proposal's secret when it is undone, and a new confirm makes a new one", func() {
		put("p1", func(t *triggers.Trigger) { t.Proposed, t.Enabled, t.Secret = true, false, "" })
		var out struct {
			Secret   string `json:"secret"`
			HookPath string `json:"hook_path"`
		}
		confirm := func() string {
			rec := s.do("alice", http.MethodPost, "/api/chats/triggers/proposals/p1/confirm", map[string]any{})
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out.Secret).NotTo(BeEmpty())
			return out.Secret
		}
		first := confirm()
		Expect(call("p1", "?source=example-hook", push, bearer(first)).Code).To(Equal(http.StatusAccepted))

		// A run blocks the undo, so end the run of that call and prune it: the
		// person undid the answer before anything used the trigger.
		rs, err := s.trigStore.ListRuns(ctx, "p1", 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(rs).To(HaveLen(1))
		r := rs[0]
		r.Status, r.Ended = triggers.StatusDone, time.Now()
		Expect(s.trigStore.SetRunStatus(ctx, r)).To(Succeed())
		_, err = s.trigStore.PruneRuns(ctx, "p1", triggers.Retention{Expire: time.Now().Add(time.Hour)})
		Expect(err).NotTo(HaveOccurred())

		rec := s.do("alice", http.MethodPost, "/api/chats/triggers/proposals/p1/undo", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		stored, err := s.trigStore.GetTrigger(ctx, "p1")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Proposed).To(BeTrue())
		Expect(stored.Enabled).To(BeFalse())
		Expect(stored.Secret).To(BeEmpty(), "the stored proposal still holds the secret of the sender")
		old := call("p1", "?source=example-hook", push, bearer(first))
		Expect(old.Code).To(Equal(http.StatusNotFound))
		Expect(strings.TrimSpace(old.Body.String())).To(Equal(notFound))

		rec = s.do("alice", http.MethodPost, "/api/chats/triggers/proposals/p1/reopen", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		second := confirm()
		Expect(second).NotTo(Equal(first))
		Expect(call("p1", "?source=example-hook", push, bearer(first)).Code).To(Equal(http.StatusNotFound))
		Expect(call("p1", "?source=example-hook", `{"type":"push","ref":"dev"}`, bearer(second)).Code).To(Equal(http.StatusAccepted))
	})

	It("answers every refusal with the same 404 and starts nothing", func() {
		put("t1", nil)
		put("off", func(t *triggers.Trigger) { t.Enabled = false })
		put("sched", func(t *triggers.Trigger) {
			*t = triggers.Trigger{ID: "sched", Owner: "alice", Name: "S", Prompt: "p", Kind: triggers.KindSchedule, Schedule: "daily 09:00", Enabled: true, Model: "test-model"}
		})
		put("nosecret", func(t *triggers.Trigger) { t.Secret = "" })
		old := time.Now().Add(-10 * time.Minute)
		signedAt := func(ts time.Time, sig string) func(*http.Request) {
			return func(r *http.Request) {
				r.Header.Set("X-Webhook-Timestamp", strconv.FormatInt(ts.Unix(), 10))
				r.Header.Set("X-Webhook-Signature-V2", sig)
			}
		}
		cases := map[string]*httptest.ResponseRecorder{
			"wrong secret":  call("t1", "?source=example-hook", push, bearer("nope")),
			"no credential": call("t1", "?source=example-hook", push, nil),
			"unknown id":    call("missing", "?source=example-hook", push, bearer(secret)),
			"disabled":      call("off", "?source=example-hook", push, bearer(secret)),
			"schedule kind": call("sched", "?source=example-hook", push, bearer(secret)),
			"stale signed":  call("t1", "", push, signedAt(old, triggers.Sign(secret, old, []byte(push)))),
			"bad signature": call("t1", "", push, signedAt(time.Now(), "sha256=00")),
			"bad timestamp": call("t1", "", push, func(r *http.Request) {
				r.Header.Set("X-Webhook-Signature-V2", "sha256=00")
				r.Header.Set("X-Webhook-Timestamp", "x")
			}),
			"empty secret":   call("nosecret", "", push, bearer("")),
			"empty bearer":   call("nosecret", "", push, func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }),
			"bad github sig": call("t1", "", push, func(r *http.Request) { r.Header.Set("X-Hub-Signature-256", "sha256=00") }),
		}
		for name, rec := range cases {
			Expect(rec.Code).To(Equal(http.StatusNotFound), name)
			Expect(strings.TrimSpace(rec.Body.String())).To(Equal(notFound), name)
			Expect(rec.Header().Get("Content-Type")).To(Equal(cases["wrong secret"].Header().Get("Content-Type")), name)
			Expect(rec.Header().Get("Retry-After")).To(BeEmpty(), name)
		}
		for _, id := range []string{"t1", "off", "sched", "nosecret"} {
			Expect(runs(id)).To(BeZero(), id)
		}
	})

	It("takes a signed call with a fresh timestamp", func() {
		put("t1", nil)
		now := time.Now()
		rec := call("t1", "?source=example-hook", push, func(r *http.Request) {
			r.Header.Set("X-Webhook-Timestamp", strconv.FormatInt(now.Unix(), 10))
			r.Header.Set("X-Webhook-Signature-V2", triggers.Sign(secret, now, []byte(push)))
		})
		Expect(rec.Code).To(Equal(http.StatusAccepted), rec.Body.String())
		Expect(runs("t1")).To(Equal(1))
	})

	Context("a signed call that is sent again", func() {
		sendSigned := func(id, body string, at time.Time, webhookID string) *httptest.ResponseRecorder {
			return call(id, "?source=example-hook", body, func(r *http.Request) {
				r.Header.Set("X-Webhook-Timestamp", strconv.FormatInt(at.Unix(), 10))
				r.Header.Set("X-Webhook-Signature-V2", triggers.Sign(secret, at, []byte(body)))
				if webhookID != "" {
					r.Header.Set("X-Webhook-Id", webhookID)
				}
			})
		}

		It("is one run when it comes again in the next minute", func() {
			put("t1", nil)
			at := clock
			Expect(sendSigned("t1", push, at, "").Code).To(Equal(http.StatusAccepted))
			clock = clock.Add(61 * time.Second)
			Expect(sendSigned("t1", push, at, "").Code).To(Equal(http.StatusAccepted))
			Expect(runs("t1")).To(Equal(1))
		})

		It("is one run when the unsigned delivery id changes", func() {
			put("t1", nil)
			at := clock
			Expect(sendSigned("t1", push, at, "id-a").Code).To(Equal(http.StatusAccepted))
			Expect(sendSigned("t1", push, at, "id-b").Code).To(Equal(http.StatusAccepted))
			Expect(runs("t1")).To(Equal(1))
		})

		It("is two runs for two different signed calls", func() {
			put("t1", nil)
			at := clock
			Expect(sendSigned("t1", push, at, "same-id").Code).To(Equal(http.StatusAccepted))
			Expect(sendSigned("t1", `{"type":"push","ref":"dev"}`, at, "same-id").Code).To(Equal(http.StatusAccepted))
			Expect(sendSigned("t1", push, at.Add(time.Second), "same-id").Code).To(Equal(http.StatusAccepted))
			Expect(runs("t1")).To(BeNumerically(">=", 3))
		})
	})

	It("takes a GitHub signed call, with the event and the delivery from its headers", func() {
		put("gh", func(t *triggers.Trigger) { t.Source, t.EventType = "github", "pull_request" })
		body := `{"action":"opened"}`
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		send := func() *httptest.ResponseRecorder {
			return call("gh", "", body, func(r *http.Request) {
				r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				r.Header.Set("X-GitHub-Event", "pull_request")
				r.Header.Set("X-GitHub-Delivery", "d-1")
			})
		}
		Expect(send().Code).To(Equal(http.StatusAccepted))
		Expect(send().Code).To(Equal(http.StatusAccepted))
		Expect(runs("gh")).To(Equal(1), "the same delivery id is one run")
	})

	It("keeps one run for the same body in the same minute when no id is sent", func() {
		put("t1", nil)
		a := call("t1", "?source=example-hook", push, bearer(secret))
		b := call("t1", "?source=example-hook", push, bearer(secret))
		Expect(a.Code).To(Equal(http.StatusAccepted))
		Expect(b.Code).To(Equal(http.StatusAccepted))
		Expect(runs("t1")).To(Equal(1))
	})

	It("takes the type from X-Webhook-Event, and the source from the query", func() {
		put("t1", func(t *triggers.Trigger) { t.Source, t.EventType = "billing", "paid" })
		miss := call("t1", "", `{"x":1}`, func(r *http.Request) { bearer(secret)(r); r.Header.Set("X-Webhook-Event", "paid") })
		Expect(strings.TrimSpace(miss.Body.String())).To(Equal(`{"run":""}`), "no source query means source webhook")
		ok := call("t1", "?source=billing", `{"x":1}`, func(r *http.Request) { bearer(secret)(r); r.Header.Set("X-Webhook-Event", "paid") })
		Expect(ok.Code).To(Equal(http.StatusAccepted))
		Expect(ok.Body.String()).To(MatchRegexp(`"run":"[^"]+"`))
	})

	It("answers 202 with no run for an event that does not match, and starts nothing", func() {
		put("t1", func(t *triggers.Trigger) { t.Matches = []string{"action=opened"} })
		rec := call("t1", "?source=example-hook", `{"type":"push","action":"closed"}`, bearer(secret))
		Expect(rec.Code).To(Equal(http.StatusAccepted))
		Expect(strings.TrimSpace(rec.Body.String())).To(Equal(`{"run":""}`))
		other := call("t1", "?source=elsewhere", `{"type":"push","action":"opened"}`, bearer(secret))
		Expect(strings.TrimSpace(other.Body.String())).To(Equal(`{"run":""}`))
		Expect(runs("t1")).To(BeZero())
	})

	It("refuses a 300 KB body with 413 and reads no more than the cap and a block", func() {
		put("t1", nil)
		big := &countingBody{r: bytes.NewReader(bytes.Repeat([]byte("a"), 300*1024))}
		req := httptest.NewRequest(http.MethodPost, "/api/chats/triggers/t1/hook", big)
		req.ContentLength = -1
		bearer(secret)(req)
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(big.n).To(BeNumerically("<=", triggers.MaxEventBody+64*1024), "the handler stopped reading near the cap")
		unknown := httptest.NewRequest(http.MethodPost, "/api/chats/triggers/missing/hook", bytes.NewReader(bytes.Repeat([]byte("a"), 300*1024)))
		Expect(func() int { r := httptest.NewRecorder(); s.e.ServeHTTP(r, unknown); return r.Code }()).To(Equal(http.StatusRequestEntityTooLarge), "a 413 does not tell which ids exist")
		Expect(runs("t1")).To(BeZero())
	})

	It("limits an authenticated trigger to 60 events a minute, and bad secrets do not spend the budget", func() {
		put("t1", nil)
		for i := 0; i < 100; i++ {
			Expect(call("t1", "", push, bearer("wrong")).Code).To(Equal(http.StatusNotFound))
		}
		for i := 0; i < 60; i++ {
			rec := call("t1", "?source=example-hook", push, func(r *http.Request) { bearer(secret)(r); r.Header.Set("X-Webhook-Id", fmt.Sprintf("id-%d", i)) })
			Expect(rec.Code).To(Equal(http.StatusAccepted), "call %d: %s", i+1, rec.Body.String())
		}
		rec := call("t1", "?source=example-hook", push, func(r *http.Request) { bearer(secret)(r); r.Header.Set("X-Webhook-Id", "id-61") })
		Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
		put("t2", nil)
		Expect(call("t2", "?source=example-hook", push, bearer(secret)).Code).To(Equal(http.StatusAccepted), "the budget is for each trigger")
	})

	It("is reached with no session, and a session does not stand for the secret", func() {
		put("t1", nil)
		req := s.request("alice", http.MethodPost, "/api/chats/triggers/t1/hook?source=example-hook", push)
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
		Expect(strings.TrimSpace(rec.Body.String())).To(Equal(notFound))
	})

	It("keeps an escaped form of the hook path behind the login", func() {
		put("t1", nil)
		want := s.do("", http.MethodPost, "/api/chats/triggers/t1/run", nil).Code
		Expect(want).To(Equal(http.StatusUnauthorized))
		for _, p := range []string{"/api/chats/triggers/t1/%68ook", "/api/chats/triggers%2Ft1/hook", "/api/chats/triggers/t1%2Fhook"} {
			req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(push))
			bearer(secret)(req)
			rec := httptest.NewRecorder()
			s.e.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(want), p)
		}
		Expect(runs("t1")).To(BeZero())
	})

	It("leaves the other trigger routes behind the login", func() {
		put("t1", nil)
		Expect(s.do("", http.MethodPost, "/api/chats/triggers/t1/run", nil).Code).To(Equal(http.StatusUnauthorized))
		Expect(s.do("", http.MethodGet, "/api/chats/triggers/t1/hook", nil).Code).To(Equal(http.StatusUnauthorized))
	})
})

// countingBody counts the bytes the handler takes from it.
type countingBody struct {
	r io.Reader
	n int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += n
	return n, err
}
func (b *countingBody) Close() error { return nil }
