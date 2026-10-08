package localai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeSwitch struct {
	mu       sync.Mutex
	report   cluster.Report
	row      cluster.CarrierRow
	requests []cluster.Request
	aborts   []string
	requestE error
	abortE   error
	order    *[]string
}

func (f *fakeSwitch) note(s string) {
	if f.order != nil {
		*f.order = append(*f.order, s)
	}
}

func (f *fakeSwitch) Status(context.Context) (cluster.Report, error) { return f.report, nil }
func (f *fakeSwitch) Preflight(_ context.Context, target cluster.Carrier) (cluster.Report, error) {
	f.note("preflight")
	if target != cluster.CarrierNATS && target != cluster.CarrierTunnel {
		return cluster.Report{}, cluster.ErrInvalidCarrier
	}
	r := f.report
	r.Target = target
	return r, nil
}

func (f *fakeSwitch) Request(_ context.Context, req cluster.Request) (cluster.CarrierRow, cluster.Report, error) {
	f.note("request")
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return f.row, f.report, f.requestE
}

func (f *fakeSwitch) Abort(_ context.Context, by string) (cluster.CarrierRow, error) {
	f.mu.Lock()
	f.aborts = append(f.aborts, by)
	f.mu.Unlock()
	return f.row, f.abortE
}

type fakeProber struct{ order *[]string }

func (f fakeProber) ProbeReplicas(context.Context) {
	if f.order != nil {
		*f.order = append(*f.order, "probe")
	}
}

func (f fakeProber) AskReplicas(context.Context) {}

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

var _ = Describe("The admin API of the carrier", func() {
	var (
		sw       *fakeSwitch
		settings *memorySettings
		nats     *fakeNATS
		order    []string
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
		order = nil
		sw = &fakeSwitch{
			order:  &order,
			report: cluster.Report{Active: cluster.CarrierNATS, Epoch: 4, State: cluster.StateStable, OK: true, Replicas: []cluster.ReplicaStatus{{ID: "a", Version: "v1"}}},
			row:    cluster.CarrierRow{Active: cluster.CarrierNATS, Epoch: 5, State: cluster.StatePrepare, Target: cluster.CarrierTunnel},
		}
		settings = &memorySettings{m: map[string]string{cluster.SettingNATSURL: "nats://user:secret@broker:4222?token=abc"}}
		nats = &fakeNATS{}
	})

	Describe("the status", func() {
		It("reports the row, the replicas, the opposite carrier as the target, and a NATS URL without its password", func() {
			code, body := call(GetCarrierEndpoint(sw, settings), http.MethodGet, "")
			Expect(code).To(Equal(http.StatusOK))
			Expect(body["active"]).To(Equal("nats"))
			Expect(body["epoch"]).To(BeEquivalentTo(4))
			Expect(body["target"]).To(Equal("tunnel"), "the report answers for a change to the other carrier")
			Expect(body["replicas"]).To(HaveLen(1))
			shown := body["settings"].(map[string]any)["nats_url"].(string)
			Expect(shown).To(ContainSubstring("broker:4222"))
			Expect(shown).ToNot(ContainSubstring("secret"))
			Expect(shown).ToNot(ContainSubstring("user"))
			Expect(shown).ToNot(ContainSubstring("abc"), "a token in the query is not shown either")
			Expect(settings.m[cluster.SettingNATSURL]).To(ContainSubstring("secret"), "the stored value is not changed")
		})
	})

	Describe("a request", func() {
		It("asks the replicas to look, runs the preflight on a dry run, and changes nothing", func() {
			code, body := call(SwitchCarrierEndpoint(sw, fakeProber{order: &order}), http.MethodPost, `{"target":"tunnel","dry_run":true}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(body["target"]).To(Equal("tunnel"))
			Expect(body["ok"]).To(BeTrue())
			Expect(order).To(Equal([]string{"probe", "preflight"}))
			Expect(sw.requests).To(BeEmpty())
		})

		It("starts the change, and passes force on", func() {
			code, body := call(SwitchCarrierEndpoint(sw, fakeProber{order: &order}), http.MethodPost, `{"target":"tunnel","force":true}`)
			Expect(code).To(Equal(http.StatusAccepted))
			Expect(body["state"]).To(Equal("prepare"))
			Expect(order).To(Equal([]string{"probe", "request"}))
			Expect(sw.requests).To(HaveLen(1))
			Expect(sw.requests[0].Target).To(Equal(cluster.CarrierTunnel))
			Expect(sw.requests[0].Force).To(BeTrue())
			Expect(sw.requests[0].By).ToNot(BeEmpty(), "who asked is recorded")
		})

		It("answers 422 with the report when the preflight blocks", func() {
			sw.requestE = &cluster.BlockedError{Report: cluster.Report{Blockers: []cluster.Blocker{{Kind: cluster.BlockerWorker, ID: "w1", Reason: "the worker predates carrier switching", Forceable: true}}}}
			code, body := call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `{"target":"tunnel"}`)
			Expect(code).To(Equal(http.StatusUnprocessableEntity))
			Expect(body["blockers"]).To(HaveLen(1))
			Expect(body["error"]).To(ContainSubstring("blocked"))
		})

		It("answers 409 when a change is already under way", func() {
			sw.requestE = cluster.ErrBusy
			code, body := call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `{"target":"tunnel"}`)
			Expect(code).To(Equal(http.StatusConflict))
			Expect(body["error"]).To(ContainSubstring("under way"))
		})

		It("answers 400 for a target that is not a carrier, and for a body that is not JSON", func() {
			code, _ := call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `{"target":"pigeon"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `nope`)
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `{"dry_run":true}`)
			Expect(code).To(Equal(http.StatusBadRequest), "a request without a target")
		})

		It("aborts a change in prepare", func() {
			sw.row = cluster.CarrierRow{Active: cluster.CarrierNATS, State: cluster.StateStable, Epoch: 6, Note: "aborted by admin"}
			code, body := call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `{"abort":true}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(body["state"]).To(Equal("stable"))
			Expect(sw.aborts).To(HaveLen(1))
			Expect(sw.requests).To(BeEmpty())
		})

		It("answers 409 to an abort when there is nothing to abort", func() {
			sw.abortE = cluster.ErrNotAbortable
			code, _ := call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `{"abort":true}`)
			Expect(code).To(Equal(http.StatusConflict))
		})

		It("reports an error of the store as a server error", func() {
			sw.requestE = errors.New("the database is down")
			code, _ := call(SwitchCarrierEndpoint(sw, fakeProber{}), http.MethodPost, `{"target":"tunnel"}`)
			Expect(code).To(Equal(http.StatusInternalServerError))
		})
	})

	Describe("the settings", func() {
		It("stores a NATS URL, checks that this replica reaches it, and does not switch", func() {
			code, body := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, `{"nats_url":"nats://broker2:4222"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(settings.m[cluster.SettingNATSURL]).To(Equal("nats://broker2:4222"))
			Expect(nats.seen).To(Equal([]string{"nats://broker2:4222"}))
			Expect(body["nats"].(map[string]any)["reachable"]).To(BeTrue())
			Expect(sw.requests).To(BeEmpty())
		})

		It("refuses a URL that this replica cannot reach, says why, and stores nothing", func() {
			nats.err = errors.New("no answer within 3s")
			code, body := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut,
				`{"nats_url":"nats://broker2:4222","max_drain":"20m"}`)
			Expect(code).To(Equal(http.StatusUnprocessableEntity))
			Expect(body["error"]).To(ContainSubstring("no answer"))
			Expect(settings.m[cluster.SettingNATSURL]).To(Equal("nats://user:secret@broker:4222?token=abc"))
			Expect(settings.m).ToNot(HaveKey(cluster.SettingMaxDrain), "a refused request stores none of its parts")
		})

		It("refuses a NATS URL while a change of carrier is under way, and stores nothing", func() {
			for _, st := range []cluster.State{cluster.StatePrepare, cluster.StateCommit} {
				sw.report.State = st
				code, body := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut,
					`{"nats_url":"nats://broker2:4222"}`)
				Expect(code).To(Equal(http.StatusConflict), string(st))
				Expect(body["error"]).To(ContainSubstring("change of carrier"))
				Expect(settings.m[cluster.SettingNATSURL]).To(ContainSubstring("broker:4222"))
				Expect(nats.seen).To(BeEmpty(), "the server is not even asked")
			}
		})

		It("refuses an address that carries a credential or a query string", func() {
			for _, bad := range []string{
				`{"nats_url":"nats://user:pass@broker2:4222"}`,
				`{"nats_url":"nats://broker2:4222?token=abc"}`,
				`{"nats_worker_url":"nats://user@broker2:4222"}`,
			} {
				code, body := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, bad)
				Expect(code).To(Equal(http.StatusBadRequest), bad)
				Expect(body["error"]).To(ContainSubstring("credentials stay on each replica"), bad)
			}
		})

		It("refuses a URL that is not a NATS address", func() {
			for _, bad := range []string{`{"nats_url":"http://broker:4222"}`, `{"nats_url":"nats://"}`, `{"nats_url":"::"}`} {
				code, _ := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, bad)
				Expect(code).To(Equal(http.StatusBadRequest), bad)
			}
			Expect(settings.m[cluster.SettingNATSURL]).To(ContainSubstring("broker:4222"))
		})

		It("clears a URL with an empty string", func() {
			code, _ := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, `{"nats_url":""}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(settings.m).ToNot(HaveKey(cluster.SettingNATSURL))
		})

		It("stores the waits of a change, and refuses one that is not a positive duration", func() {
			code, _ := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, `{"max_drain":"20m","prepare_timeout":"90s"}`)
			Expect(code).To(Equal(http.StatusOK))
			Expect(settings.m[cluster.SettingMaxDrain]).To(Equal("20m"))
			Expect(settings.m[cluster.SettingPrepareTimeout]).To(Equal("90s"))
			code, _ = call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, `{"max_drain":"soon"}`)
			Expect(code).To(Equal(http.StatusBadRequest))
		})

		It("refuses a wait that is not positive or is too short, with a 400, and stores none of the request", func() {
			for _, bad := range []string{"0s", "-5s", "1ns", "1ms", "4s"} {
				body := `{"nats_url":"nats://broker2:4222","max_drain":"20m","prepare_timeout":"` + bad + `"}`
				code, resp := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, body)
				Expect(code).To(Equal(http.StatusBadRequest), bad)
				Expect(resp["error"]).To(ContainSubstring("prepare_timeout"), bad)
				Expect(settings.m).ToNot(HaveKey(cluster.SettingMaxDrain), "%s: nothing is stored when one part is refused", bad)
				Expect(settings.m[cluster.SettingNATSURL]).To(ContainSubstring("broker:4222"), bad)
			}
			code, _ := call(PutClusterSettingsEndpoint(settings, nats, fakeProber{}, sw), http.MethodPut, `{"prepare_timeout":"5s"}`)
			Expect(code).To(Equal(http.StatusOK))
		})

		It("reads the settings with the password of the URL masked", func() {
			code, body := call(GetClusterSettingsEndpoint(settings), http.MethodGet, "")
			Expect(code).To(Equal(http.StatusOK))
			Expect(body["nats_url"]).To(ContainSubstring("broker:4222"))
			Expect(body["nats_url"]).ToNot(ContainSubstring("secret"))
			Expect(body["nats_url"]).ToNot(ContainSubstring("token"))
		})
	})

	Describe("a frontend without distributed mode", func() {
		It("answers 503 on every route", func() {
			for _, h := range []echo.HandlerFunc{UnavailableCarrierEndpoint()} {
				code, _ := call(h, http.MethodGet, "")
				Expect(code).To(Equal(http.StatusServiceUnavailable))
			}
		})
	})
})
