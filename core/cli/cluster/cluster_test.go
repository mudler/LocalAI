// SPDX-License-Identifier: MIT
package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClusterCLI(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Cluster CLI") }

// fakeServer records the requests of a command and answers from a table.
type fakeServer struct {
	mu       sync.Mutex
	requests []recorded
	handler  func(r *http.Request, body map[string]any) (int, any)
	srv      *httptest.Server
}

type recorded struct {
	Method, Path, Auth string
	Body               map[string]any
}

func newFakeServer(h func(r *http.Request, body map[string]any) (int, any)) *fakeServer {
	f := &fakeServer{handler: h}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), body})
		f.mu.Unlock()
		code, resp := f.handler(r, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return f
}

func (f *fakeServer) last() recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *fakeServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func remote(f *fakeServer) Remote {
	return Remote{Endpoint: f.srv.URL, APIKey: "k", Timeout: 5 * time.Second}
}

var _ = Describe("local-ai cluster", func() {
	var out bytes.Buffer
	BeforeEach(func() { out.Reset() })

	Describe("parsing", func() {
		It("has no carrier command", func() {
			var c struct {
				Cluster Command `cmd:""`
			}
			parser, err := kong.New(&c)
			Expect(err).NotTo(HaveOccurred())
			_, err = parser.Parse([]string{"cluster", "carrier"})
			Expect(err).To(HaveOccurred())
			ctx, err := parser.Parse([]string{"cluster", "settings"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ctx.Command()).To(Equal("cluster settings get"))
		})

		It("tells an absent setting from an empty one", func() {
			var c struct {
				Cluster Command `cmd:""`
			}
			parser, err := kong.New(&c)
			Expect(err).NotTo(HaveOccurred())
			_, err = parser.Parse([]string{"cluster", "settings", "set", "--nats-url=", "--nats-worker-url", "nats://w:4222"})
			Expect(err).NotTo(HaveOccurred())
			set := c.Cluster.Settings.Set
			Expect(set.NATSURL).NotTo(BeNil())
			Expect(*set.NATSURL).To(BeEmpty())
			Expect(*set.NATSWorkerURL).To(Equal("nats://w:4222"))
			_, err = parser.Parse([]string{"cluster", "settings", "set", "--max-drain", "20m"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("the remote", func() {
		It("explains a refused key and a frontend that is not distributed", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) { return 401, map[string]any{"error": "no"} })
			err := (&GetSettingsCommand{Remote: remote(f)}).run(context.Background(), &out)
			f.srv.Close()
			Expect(err).To(MatchError(ContainSubstring("--api-key")))

			f = newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 503, map[string]any{"error": "distributed mode is not enabled on this frontend"}
			})
			defer f.srv.Close()
			err = (&GetSettingsCommand{Remote: remote(f)}).run(context.Background(), &out)
			Expect(err).To(MatchError(ContainSubstring("not running in distributed mode")))
		})

		It("refuses an endpoint that is not a plain HTTP URL", func() {
			err := (&GetSettingsCommand{Remote: Remote{Endpoint: "http://u:p@host:1"}}).run(context.Background(), &out)
			Expect(err).To(MatchError(ContainSubstring("without credentials")))
		})

		It("writes the JSON of the server with --json", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"nats_url": "nats://n:4222"}
			})
			defer f.srv.Close()
			r := remote(f)
			r.JSON = true
			Expect((&GetSettingsCommand{Remote: r}).run(context.Background(), &out)).To(Succeed())
			var got map[string]any
			Expect(json.Unmarshal(out.Bytes(), &got)).To(Succeed())
			Expect(got["nats_url"]).To(Equal("nats://n:4222"))
		})
	})

	Describe("settings", func() {
		It("shows the settings and the bus of the frontend that answered, and sends the key", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"nats_url": "nats://n:4222", "bus": map[string]any{"bus": "nats", "nats_configured": true, "nats_connected": true}}
			})
			defer f.srv.Close()
			Expect((&GetSettingsCommand{Remote: remote(f)}).run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Method).To(Equal("GET"))
			Expect(f.last().Path).To(Equal("/api/cluster/settings"))
			Expect(f.last().Auth).To(Equal("Bearer k"))
			Expect(out.String()).To(ContainSubstring("nats_url:        nats://n:4222"))
			Expect(out.String()).To(ContainSubstring("nats_worker_url: (not set)"))
			Expect(out.String()).To(ContainSubstring("bus:             nats"))
		})

		It("sends only the flags that are given, and says when a new address is used", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"saved": true, "nats": map[string]any{"reachable": true}, "settings": map[string]any{"nats_url": "nats://n:4222"}}
			})
			defer f.srv.Close()
			url := "nats://n:4222"
			c := &SetSettingsCommand{NATSURL: &url, Remote: remote(f)}
			Expect(c.run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Method).To(Equal("PUT"))
			Expect(f.last().Body).To(Equal(map[string]any{"nats_url": url}))
			Expect(out.String()).To(ContainSubstring("reaches the NATS server"))
			Expect(out.String()).To(ContainSubstring("after a restart"))
			Expect(out.String()).NotTo(ContainSubstring("bus:"))
		})

		It("sends an empty value to clear a setting", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"saved": true, "settings": map[string]any{}}
			})
			defer f.srv.Close()
			empty := ""
			Expect((&SetSettingsCommand{NATSWorkerURL: &empty, Remote: remote(f)}).run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Body).To(Equal(map[string]any{"nats_worker_url": ""}))
		})

		It("parses --carrier-weight name=value pairs, more than once", func() {
			var c struct {
				Cluster Command `cmd:""`
			}
			parser, err := kong.New(&c)
			Expect(err).NotTo(HaveOccurred())
			_, err = parser.Parse([]string{"cluster", "settings", "set", "--carrier-weight", "nats=10", "--carrier-weight", "p2p=default"})
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Cluster.Settings.Set.CarrierWeight).To(Equal(map[string]string{"nats": "10", "p2p": "default"}))
		})

		It("sends the weights as a partial update, and null for default", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"saved": true, "settings": map[string]any{
					"carrier_weights": map[string]any{"nats": 10, "websocket": 50, "p2p": 25}}}
			})
			defer f.srv.Close()
			c := &SetSettingsCommand{CarrierWeight: map[string]string{"nats": "10", "p2p": "default"}, Remote: remote(f)}
			Expect(c.run(context.Background(), &out)).To(Succeed())
			Expect(f.last().Body).To(Equal(map[string]any{"carrier_weights": map[string]any{"nats": 10.0, "p2p": nil}}))
			Expect(out.String()).To(ContainSubstring("carrier_weights: nats=10 websocket=50 p2p=25"))
		})

		It("refuses a weight that is not a number before it calls the server", func() {
			err := (&SetSettingsCommand{CarrierWeight: map[string]string{"nats": "high"}}).run(context.Background(), io.Discard)
			Expect(err).To(MatchError(ContainSubstring("whole number")))
		})

		It("shows the weights in the settings", func() {
			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 200, map[string]any{"carrier_weights": map[string]any{"nats": 100, "websocket": 50, "p2p": 25}}
			})
			defer f.srv.Close()
			Expect((&GetSettingsCommand{Remote: remote(f)}).run(context.Background(), &out)).To(Succeed())
			Expect(out.String()).To(ContainSubstring("carrier_weights: nats=100 websocket=50 p2p=25"))
		})

		It("refuses a call with nothing to store, and shows a refused address", func() {
			Expect((&SetSettingsCommand{}).run(context.Background(), io.Discard)).To(MatchError(ContainSubstring("at least one setting")))

			f := newFakeServer(func(*http.Request, map[string]any) (int, any) {
				return 422, map[string]any{"error": "nats_url: this replica cannot reach the NATS server at nats://x:1"}
			})
			defer f.srv.Close()
			u := "nats://x:1"
			Expect((&SetSettingsCommand{NATSURL: &u, Remote: remote(f)}).run(context.Background(), &out)).To(MatchError(ContainSubstring("cannot reach the NATS server")))
		})
	})
})
