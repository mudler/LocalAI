package localai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeState is the broadcast bus and the replicas of a frontend.
type fakeState struct {
	bus      carrier.BusState
	replicas []cluster.LiveInstance
	err      error
}

func (f fakeState) Bus() carrier.BusState { return f.bus }
func (f fakeState) Replicas(context.Context) ([]cluster.LiveInstance, error) {
	return f.replicas, f.err
}

type fakeNATS struct {
	err  error
	seen []string
}

func (f *fakeNATS) CheckNATS(_ context.Context, url string) error {
	f.seen = append(f.seen, url)
	return f.err
}

type memorySettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memorySettings) Get(_ context.Context, k string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok, nil
}

func (s *memorySettings) Set(_ context.Context, k, v, _ string) error {
	if k == "nats.password" {
		return cluster.ErrUnknownSetting
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

func (s *memorySettings) Apply(ctx context.Context, changes []cluster.SettingChange, by string) error {
	// All or nothing, as the store: a change that fails undoes the others.
	s.mu.Lock()
	before := map[string]string{}
	for k, v := range s.m {
		before[k] = v
	}
	s.mu.Unlock()
	for _, ch := range changes {
		var err error
		if ch.Clear {
			err = s.Clear(ctx, ch.Key)
		} else {
			err = s.Set(ctx, ch.Key, ch.Value, by)
		}
		if err != nil {
			s.mu.Lock()
			s.m = before
			s.mu.Unlock()
			return err
		}
	}
	return nil
}

func (s *memorySettings) Clear(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}

func (s *memorySettings) All(context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.m {
		out[k] = v
	}
	return out, nil
}

var _ = Describe("The admin API of the cluster settings", func() {
	var (
		settings *memorySettings
		nats     *fakeNATS
		state    fakeState
	)

	call := func(h echo.HandlerFunc, method, body string) (int, map[string]any) {
		GinkgoHelper()
		e := echo.New()
		req := httptest.NewRequest(method, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		Expect(h(e.NewContext(req, rec))).To(Succeed())
		var out map[string]any
		if rec.Body.Len() > 0 {
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed(), rec.Body.String())
		}
		return rec.Code, out
	}

	BeforeEach(func() {
		settings = &memorySettings{m: map[string]string{cluster.SettingNATSURL: "nats://user:secret@broker:4222?token=abc"}}
		nats = &fakeNATS{}
		seen := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
		state = fakeState{
			bus:      carrier.BusState{Bus: carrier.BusNATS, NATSConfigured: true, NATSConnected: true},
			replicas: []cluster.LiveInstance{{Instance: cluster.Instance{ID: "a", Version: "v1", LastSeen: seen}}, {Instance: cluster.Instance{ID: "b", Version: "v1", LastSeen: seen}}},
		}
	})

	Describe("a read", func() {
		It("reports the bus of this frontend, read from its own state, and the live replicas", func() {
			code, body := call(GetClusterSettingsEndpoint(settings, state), http.MethodGet, "")
			Expect(code).To(Equal(http.StatusOK))
			Expect(body["bus"]).To(Equal(map[string]any{"bus": "nats", "nats_configured": true, "nats_connected": true}))
			Expect(body["replicas"]).To(HaveLen(2))
			Expect(body["replicas"].([]any)[0].(map[string]any)["id"]).To(Equal("a"))
			Expect(body).ToNot(HaveKey("active"))
			Expect(body).ToNot(HaveKey("epoch"))

			state.bus = carrier.BusState{Bus: carrier.BusPostgres, NATSConfigured: true}
			_, body = call(GetClusterSettingsEndpoint(settings, state), http.MethodGet, "")
			Expect(body["bus"].(map[string]any)["bus"]).To(Equal("postgres"))
		})

		It("reports an error of the replica list as a server error", func() {
			state.err = errors.New("the database is down")
			code, _ := call(GetClusterSettingsEndpoint(settings, state), http.MethodGet, "")
			Expect(code).To(Equal(http.StatusInternalServerError))
		})
	})

	Describe("the settings", func() {
		It("stores a NATS URL and checks that this replica reaches it", func() {
			code, body := call(PutClusterSettingsEndpoint(settings, nats), http.MethodPut, `{"nats_url":"nats://broker2:4222"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(settings.m[cluster.SettingNATSURL]).To(Equal("nats://broker2:4222"))
			Expect(nats.seen).To(Equal([]string{"nats://broker2:4222"}))
			Expect(body["nats"].(map[string]any)["reachable"]).To(BeTrue())
		})

		It("refuses a URL that this replica cannot reach, says why, and stores nothing", func() {
			nats.err = errors.New("no answer within 3s")
			code, body := call(PutClusterSettingsEndpoint(settings, nats), http.MethodPut,
				`{"nats_url":"nats://broker2:4222","nats_worker_url":"nats://workers:4222"}`)
			Expect(code).To(Equal(http.StatusUnprocessableEntity))
			Expect(body["error"]).To(ContainSubstring("no answer"))
			Expect(settings.m[cluster.SettingNATSURL]).To(Equal("nats://user:secret@broker:4222?token=abc"))
			Expect(settings.m).ToNot(HaveKey(cluster.SettingNATSWorkerURL), "a refused request stores none of its parts")
		})

		It("refuses an address that carries a credential or a query string", func() {
			for _, bad := range []string{
				`{"nats_url":"nats://user:pass@broker2:4222"}`,
				`{"nats_url":"nats://broker2:4222?token=abc"}`,
				`{"nats_worker_url":"nats://user@broker2:4222"}`,
			} {
				code, body := call(PutClusterSettingsEndpoint(settings, nats), http.MethodPut, bad)
				Expect(code).To(Equal(http.StatusBadRequest), bad)
				Expect(body["error"]).To(ContainSubstring("credentials stay on each replica"), bad)
			}
		})

		It("refuses a URL that is not a NATS address", func() {
			for _, bad := range []string{`{"nats_url":"http://broker:4222"}`, `{"nats_url":"nats://"}`, `{"nats_url":"::"}`} {
				code, _ := call(PutClusterSettingsEndpoint(settings, nats), http.MethodPut, bad)
				Expect(code).To(Equal(http.StatusBadRequest), bad)
			}
			Expect(settings.m[cluster.SettingNATSURL]).To(ContainSubstring("broker:4222"))
		})

		It("clears a URL with an empty string", func() {
			code, _ := call(PutClusterSettingsEndpoint(settings, nats), http.MethodPut, `{"nats_url":""}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(settings.m).ToNot(HaveKey(cluster.SettingNATSURL))
		})

		It("has no waits of a change of carrier any more", func() {
			code, _ := call(PutClusterSettingsEndpoint(settings, nats), http.MethodPut, `{"max_drain":"20m","prepare_timeout":"90s"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(settings.m).To(Equal(map[string]string{cluster.SettingNATSURL: "nats://user:secret@broker:4222?token=abc"}), "nothing is stored")
			_, body := call(GetClusterSettingsEndpoint(settings, state), http.MethodGet, "")
			Expect(body).ToNot(HaveKey("max_drain"))
			Expect(body).ToNot(HaveKey("prepare_timeout"))
		})

		Describe("carrier weights", func() {
			put := func(body string) (int, map[string]any) {
				GinkgoHelper()
				return call(PutClusterSettingsEndpoint(settings, nats), http.MethodPut, body)
			}

			It("reads the defaults when nothing is stored", func() {
				code, body := call(GetClusterSettingsEndpoint(settings, state), http.MethodGet, "")
				Expect(code).To(Equal(http.StatusOK))
				Expect(body["carrier_weights"]).To(Equal(map[string]any{"nats": 100.0, "websocket": 50.0, "p2p": 25.0}))
				Expect(body["carrier_weights_default"]).To(Equal(map[string]any{"nats": 100.0, "websocket": 50.0, "p2p": 25.0}))
			})

			It("stores one weight and leaves the others as they are", func() {
				code, resp := put(`{"carrier_weights":{"p2p":300}}`)
				Expect(code).To(Equal(http.StatusOK))
				Expect(settings.m).To(Equal(map[string]string{
					cluster.SettingNATSURL:          "nats://user:secret@broker:4222?token=abc",
					cluster.SettingCarrierWeightP2P: "300",
				}))
				Expect(resp["settings"].(map[string]any)["carrier_weights"]).To(Equal(map[string]any{"nats": 100.0, "websocket": 50.0, "p2p": 300.0}))
			})

			It("accepts 0 and 1000 and refuses a value outside the range, with a 400", func() {
				code, _ := put(`{"carrier_weights":{"nats":0,"websocket":1000}}`)
				Expect(code).To(Equal(http.StatusOK))
				Expect(settings.m[cluster.SettingCarrierWeightNATS]).To(Equal("0"))
				for _, bad := range []string{`1001`, `-1`} {
					code, resp := put(`{"carrier_weights":{"p2p":` + bad + `}}`)
					Expect(code).To(Equal(http.StatusBadRequest), bad)
					Expect(resp["error"]).To(ContainSubstring("p2p"), bad)
				}
				Expect(settings.m).ToNot(HaveKey(cluster.SettingCarrierWeightP2P))
			})

			It("refuses a weight that is not a whole number", func() {
				for _, bad := range []string{`1.5`, `"high"`, `true`} {
					code, _ := put(`{"carrier_weights":{"nats":` + bad + `}}`)
					Expect(code).To(Equal(http.StatusBadRequest), bad)
				}
			})

			It("refuses a carrier that is not one of the three, and stores none of the request", func() {
				code, resp := put(`{"nats_worker_url":"nats://workers:4222","carrier_weights":{"nats":5,"carrier-pigeon":9}}`)
				Expect(code).To(Equal(http.StatusBadRequest))
				Expect(resp["error"]).To(ContainSubstring("carrier-pigeon"))
				Expect(settings.m).ToNot(HaveKey(cluster.SettingNATSWorkerURL))
				Expect(settings.m).ToNot(HaveKey(cluster.SettingCarrierWeightNATS))
			})

			It("puts the default back for a weight sent as null", func() {
				settings.m[cluster.SettingCarrierWeightWebSocket] = "7"
				code, resp := put(`{"carrier_weights":{"websocket":null}}`)
				Expect(code).To(Equal(http.StatusOK))
				Expect(settings.m).ToNot(HaveKey(cluster.SettingCarrierWeightWebSocket))
				Expect(resp["settings"].(map[string]any)["carrier_weights"].(map[string]any)["websocket"]).To(Equal(50.0))
			})

			It("keeps a weight when a request does not mention carrier_weights", func() {
				settings.m[cluster.SettingCarrierWeightNATS] = "3"
				code, _ := put(`{"nats_worker_url":"nats://workers:4222"}`)
				Expect(code).To(Equal(http.StatusOK))
				Expect(settings.m[cluster.SettingCarrierWeightNATS]).To(Equal("3"))
			})
		})

		It("reads the settings with the password of the URL masked", func() {
			code, body := call(GetClusterSettingsEndpoint(settings, state), http.MethodGet, "")
			Expect(code).To(Equal(http.StatusOK))
			Expect(body["nats_url"]).To(ContainSubstring("broker:4222"))
			Expect(body["nats_url"]).ToNot(ContainSubstring("secret"))
			Expect(body["nats_url"]).ToNot(ContainSubstring("token"))
		})
	})

	Describe("a frontend without distributed mode", func() {
		It("answers 503 on every route", func() {
			for _, h := range []echo.HandlerFunc{UnavailableClusterEndpoint()} {
				code, _ := call(h, http.MethodGet, "")
				Expect(code).To(Equal(http.StatusServiceUnavailable))
			}
		})
	})
})
