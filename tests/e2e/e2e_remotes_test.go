package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeLocalAIUpstream is a LocalAI as another server sees it: the model list,
// the capabilities list and a chat endpoint. It records the Authorization
// header and the model of every chat request, and refuses a chat request that
// carries another key than the one it expects.
type fakeLocalAIUpstream struct {
	srv *httptest.Server

	mu       sync.Mutex
	models   []string
	wantAuth string
	lastAuth string
	lastBody map[string]any
	chats    int
}

func newFakeLocalAIUpstream(wantKey string, models ...string) *fakeLocalAIUpstream {
	f := &fakeLocalAIUpstream{models: models, wantAuth: "Bearer " + wantKey}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeLocalAIUpstream) URL() string { return f.srv.URL }
func (f *fakeLocalAIUpstream) Close()      { f.srv.Close() }

func (f *fakeLocalAIUpstream) SetModels(ids ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.models = ids
}

// ExpectKey changes the key the fake accepts.
func (f *fakeLocalAIUpstream) ExpectKey(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wantAuth = "Bearer " + key
}

// Last returns the Authorization header and the body of the last chat request.
func (f *fakeLocalAIUpstream) Last() (string, map[string]any, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastAuth, f.lastBody, f.chats
}

func (f *fakeLocalAIUpstream) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	ids := slices.Clone(f.models)
	want := f.wantAuth
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != want {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models/capabilities":
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{"id": id, "capabilities": []string{"FLAG_CHAT"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		f.mu.Lock()
		f.lastAuth, f.lastBody = r.Header.Get("Authorization"), parsed
		f.chats++
		f.mu.Unlock()
		if stream, _ := parsed["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"hello from the fake localai"},"finish_reason":null}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello from the fake localai"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// connAPI calls the remote routes of the test server.
func connAPI(method, path string, body any) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", apiPort, path), rd)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return resp.StatusCode, out
}

type connView struct {
	ID          string `json:"id"`
	KeySet      bool   `json:"key_set"`
	Status      string `json:"status"`
	Entries     []string
	Unavailable []string
	New         []struct {
		ID string `json:"id"`
	} `json:"new"`
}

type connResult struct {
	Remote  connView `json:"remote"`
	Created []string `json:"created"`
	Skipped []struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"skipped"`
}

// listedModels returns the ids of GET /v1/models.
func listedModels() []string {
	resp, err := http.Get(apiURL + "/models")
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	ExpectWithOffset(1, json.NewDecoder(resp.Body).Decode(&out)).To(Succeed())
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids
}

func chatOnce(model string) (int, string) {
	resp := postJSONTo("/chat/completions", map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

var _ = Describe("remotes", Label("remotes"), Ordered, func() {
	const (
		keyOne = "key-one-e2e"
		keyTwo = "key-two-e2e"
		// An upstream id that is not a safe model name: the entry gets a
		// sanitised name and the request must still carry the original id.
		upstreamID    = "vendor/up chat:7b"
		entryName     = "vendor-up-chat-7b"
		newUpstreamID = "up-new"
	)

	BeforeAll(func() {
		if localAIProxyPath == "" || cloudProxyPath == "" {
			Skip("proxy backend binaries not built (make build-cloud-proxy-backend build-localai-proxy-backend)")
		}
	})

	Describe("a localai remote", Ordered, func() {
		var (
			up     *fakeLocalAIUpstream
			connID string
		)
		BeforeAll(func() {
			up = newFakeLocalAIUpstream(keyOne, upstreamID)
			DeferCleanup(up.Close)
		})

		It("tests the remote and lists its models without saving anything", func() {
			code, body := connAPI(http.MethodPost, "/api/remotes/test", map[string]any{"kind": "localai", "url": up.URL(), "api_key": keyOne})
			Expect(code).To(Equal(http.StatusOK), string(body))
			Expect(string(body)).To(ContainSubstring(`"ok":true`))
			Expect(string(body)).To(ContainSubstring(upstreamID))

			code, body = connAPI(http.MethodGet, "/api/remotes", nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(body)).To(MatchJSON(`{"data":[]}`))
		})

		It("reports a wrong key as a failed test, not an error", func() {
			code, body := connAPI(http.MethodPost, "/api/remotes/test", map[string]any{"kind": "localai", "url": up.URL(), "api_key": "wrong"})
			Expect(code).To(Equal(http.StatusOK), string(body))
			Expect(string(body)).To(ContainSubstring(`"ok":false`))
			Expect(string(body)).To(ContainSubstring(`"step":"auth"`))
		})

		It("creates the remote and the entry of the adopted model", func() {
			code, body := connAPI(http.MethodPost, "/api/remotes", map[string]any{
				"kind": "localai", "name": "e2e-remote", "url": up.URL(), "api_key": keyOne,
				"models": []map[string]any{{"id": upstreamID, "usecases": []string{"chat"}}},
			})
			Expect(code).To(Equal(http.StatusCreated), string(body))
			Expect(string(body)).NotTo(ContainSubstring(keyOne))
			var res connResult
			Expect(json.Unmarshal(body, &res)).To(Succeed())
			Expect(res.Created).To(Equal([]string{entryName}))
			Expect(res.Skipped).To(BeEmpty())
			Expect(res.Remote.KeySet).To(BeTrue())
			connID = res.Remote.ID
			Expect(connID).NotTo(BeEmpty())

			Eventually(listedModels, 10*time.Second, 200*time.Millisecond).Should(ContainElement(entryName))
		})

		It("keeps the key out of the entry file", func() {
			data, err := os.ReadFile(filepath.Join(modelsPath, entryName+".yaml"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("remote: " + connID))
			Expect(string(data)).NotTo(ContainSubstring(keyOne))
		})

		It("answers a chat request with the remote's key and the upstream model name", func() {
			code, body := chatOnce(entryName)
			Expect(code).To(Equal(http.StatusOK), body)
			Expect(chatContent(body)).To(Equal("hello from the fake localai"))
			auth, last, _ := up.Last()
			Expect(auth).To(Equal("Bearer " + keyOne))
			Expect(last["model"]).To(Equal(upstreamID))
		})

		It("marks the entry as remote in the capabilities", func() {
			code, body := connAPI(http.MethodGet, "/api/models/capabilities", nil)
			Expect(code).To(Equal(http.StatusOK))
			var out struct {
				Data []map[string]any `json:"data"`
			}
			Expect(json.Unmarshal(body, &out)).To(Succeed())
			var entry map[string]any
			for _, m := range out.Data {
				if m["id"] == entryName {
					entry = m
				}
			}
			Expect(entry).NotTo(BeNil())
			Expect(entry).To(HaveKeyWithValue("remote", true))
			Expect(entry).To(HaveKeyWithValue("remote_id", connID))
			Expect(entry).To(HaveKeyWithValue("upstream_state", "ok"))
			Expect(entry).To(HaveKey("backend"))
		})

		It("shows a new upstream model after a refresh and adopts it", func() {
			up.SetModels(upstreamID, newUpstreamID)
			code, body := connAPI(http.MethodPost, "/api/remotes/"+connID+"/refresh", nil)
			Expect(code).To(Equal(http.StatusOK), string(body))
			var view connView
			Expect(json.Unmarshal(body, &view)).To(Succeed())
			Expect(view.Status).To(Equal("ok"))
			var ids []string
			for _, m := range view.New {
				ids = append(ids, m.ID)
			}
			Expect(ids).To(ConsistOf(newUpstreamID))
			Expect(listedModels()).NotTo(ContainElement(newUpstreamID))

			code, body = connAPI(http.MethodPost, "/api/remotes/"+connID+"/adopt", map[string]any{
				"models": []map[string]any{{"id": newUpstreamID, "usecases": []string{"chat"}}},
			})
			Expect(code).To(Equal(http.StatusOK), string(body))
			var res connResult
			Expect(json.Unmarshal(body, &res)).To(Succeed())
			Expect(res.Created).To(Equal([]string{newUpstreamID}))
			Eventually(listedModels, 10*time.Second, 200*time.Millisecond).Should(ContainElement(newUpstreamID))
		})

		It("uses the rotated key for the next request", func() {
			up.ExpectKey(keyTwo)
			// The old key no longer works upstream, so the loaded entry
			// would fail if the new key did not reach it.
			code, body := connAPI(http.MethodPatch, "/api/remotes/"+connID, map[string]any{"api_key": keyTwo})
			Expect(code).To(Equal(http.StatusOK), string(body))
			Expect(string(body)).NotTo(ContainSubstring(keyTwo))
			// The change refreshed the remote: the status belongs to the new key.
			var view connView
			Expect(json.Unmarshal(body, &view)).To(Succeed())
			Expect(view.Status).To(Equal("ok"))

			Eventually(func() string {
				code, _ := chatOnce(entryName)
				auth, _, _ := up.Last()
				if code != http.StatusOK {
					return fmt.Sprintf("status %d", code)
				}
				return auth
			}, 15*time.Second, 300*time.Millisecond).Should(Equal("Bearer " + keyTwo))
		})

		It("does not send the key of the remote to another server", func() {
			other := newFakeLocalAIUpstream("unused", upstreamID)
			defer other.Close()
			const hostile = "e2e-hostile-entry"
			body := fmt.Sprintf("name: %s\nbackend: localai-proxy\nknown_usecases: [chat]\nproxy:\n  upstream_url: %s\n  upstream_model: %q\n  remote: %s\n",
				hostile, other.URL(), upstreamID, connID)
			path := filepath.Join(modelsPath, hostile+".yaml")
			Expect(os.WriteFile(path, []byte(body), 0o644)).To(Succeed())
			Expect(localAIApp.ModelConfigLoader().LoadModelConfigsFromPath(modelsPath)).To(Succeed())
			DeferCleanup(func() {
				_ = os.Remove(path)
				_ = localAIApp.ModelConfigLoader().LoadModelConfigsFromPath(modelsPath)
			})

			code, reply := chatOnce(hostile)
			Expect(code).NotTo(Equal(http.StatusOK), reply)
			Expect(reply).To(ContainSubstring("not an address of this"))
			Expect(reply).NotTo(ContainSubstring(keyTwo))
			auth, _, chats := other.Last()
			Expect(chats).To(BeZero(), "the other server got no request")
			Expect(auth).To(BeEmpty())
		})

		It("marks an entry unavailable when the upstream stops listing it", func() {
			up.SetModels(upstreamID)
			code, body := connAPI(http.MethodPost, "/api/remotes/"+connID+"/refresh", nil)
			Expect(code).To(Equal(http.StatusOK), string(body))
			var view connView
			Expect(json.Unmarshal(body, &view)).To(Succeed())
			Expect(view.Unavailable).To(ConsistOf(newUpstreamID))

			_, caps := connAPI(http.MethodGet, "/api/models/capabilities", nil)
			var out struct {
				Data []map[string]any `json:"data"`
			}
			Expect(json.Unmarshal(caps, &out)).To(Succeed())
			states := map[string]any{}
			for _, m := range out.Data {
				states[m["id"].(string)] = m["upstream_state"]
			}
			Expect(states).To(HaveKeyWithValue(newUpstreamID, "unavailable"))
			Expect(states).To(HaveKeyWithValue(entryName, "ok"))
		})

		It("refuses an entries value other than keep or remove", func() {
			code, _ := connAPI(http.MethodDelete, "/api/remotes/"+connID+"?entries=everything", nil)
			Expect(code).To(Equal(http.StatusBadRequest))
		})

		It("removes the entries with entries=remove", func() {
			code, body := connAPI(http.MethodDelete, "/api/remotes/"+connID+"?entries=remove", nil)
			Expect(code).To(Equal(http.StatusNoContent), string(body))
			Eventually(listedModels, 10*time.Second, 200*time.Millisecond).ShouldNot(ContainElements(entryName))
			Expect(listedModels()).NotTo(ContainElement(newUpstreamID))
			_, err := os.Stat(filepath.Join(modelsPath, entryName+".yaml"))
			Expect(os.IsNotExist(err)).To(BeTrue())

			code, _ = connAPI(http.MethodGet, "/api/remotes/"+connID, nil)
			Expect(code).To(Equal(http.StatusNotFound))
		})
	})

	Describe("an openai remote", Ordered, func() {
		const cloudKey = "cloud-key-e2e"
		var (
			up     *fakeOpenAIUpstreamServer
			connID string
		)
		BeforeAll(func() {
			up = newFakeOpenAIUpstream()
			up.SetModels("fake-gpt")
			DeferCleanup(up.Close)
		})

		It("creates the remote and answers chat through the cloud proxy", func() {
			code, body := connAPI(http.MethodPost, "/api/remotes", map[string]any{
				"kind": "openai", "name": "e2e-cloud", "url": up.URL() + "/v1", "api_key": cloudKey,
				"models": []map[string]any{{"id": "fake-gpt"}},
			})
			Expect(code).To(Equal(http.StatusCreated), string(body))
			Expect(string(body)).NotTo(ContainSubstring(cloudKey))
			var res connResult
			Expect(json.Unmarshal(body, &res)).To(Succeed())
			Expect(res.Created).To(Equal([]string{"fake-gpt"}))
			connID = res.Remote.ID
			Eventually(listedModels, 10*time.Second, 200*time.Millisecond).Should(ContainElement("fake-gpt"))

			code, reply := chatOnce("fake-gpt")
			Expect(code).To(Equal(http.StatusOK), reply)
			Expect(chatContent(reply)).To(Equal("hello from fake openai"))
			_, path, hdr, _ := up.Snapshot()
			Expect(path).To(Equal("/v1/chat/completions"))
			Expect(hdr.Get("Authorization")).To(Equal("Bearer " + cloudKey))
			Expect(up.DecodedBody()["model"]).To(Equal("fake-gpt"))
		})

		It("keeps the entries as plain models with entries=keep", func() {
			code, body := connAPI(http.MethodDelete, "/api/remotes/"+connID, nil)
			Expect(code).To(Equal(http.StatusNoContent), string(body))
			Expect(listedModels()).To(ContainElement("fake-gpt"))
			data, err := os.ReadFile(filepath.Join(modelsPath, "fake-gpt.yaml"))
			Expect(err).ToNot(HaveOccurred())
			// The entry is detached: proxy.remote is cleared, not the id of
			// the deleted remote.
			Expect(string(data)).To(ContainSubstring(`remote: ""`))
			Expect(string(data)).NotTo(ContainSubstring(connID))
			Expect(string(data)).NotTo(ContainSubstring(cloudKey))
			Expect(strings.Contains(string(data), "fake-gpt")).To(BeTrue())
			// Leave no model behind for the other specs.
			Expect(os.Remove(filepath.Join(modelsPath, "fake-gpt.yaml"))).To(Succeed())
			Expect(localAIApp.ModelConfigLoader().LoadModelConfigsFromPath(modelsPath)).To(Succeed())
		})
	})
})
