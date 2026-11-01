// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeFT struct {
	startUser string
	started   []schema.FineTuneJobRequest
	jobs      map[string]*schema.FineTuneJob
}

func (f *fakeFT) Start(_ context.Context, user string, r schema.FineTuneJobRequest) (*schema.FineTuneJobResponse, error) {
	f.startUser = user
	f.started = append(f.started, r)
	return &schema.FineTuneJobResponse{ID: "j1", Status: "queued"}, nil
}
func (f *fakeFT) Get(user, id string) (*schema.FineTuneJob, error) {
	j, ok := f.jobs[id]
	if !ok || (user != "" && j.UserID != user) {
		return nil, ErrNotFound
	}
	return j, nil
}
func (f *fakeFT) List(user string) []*schema.FineTuneJob {
	var out []*schema.FineTuneJob
	for _, j := range f.jobs {
		if user == "" || j.UserID == user {
			out = append(out, j)
		}
	}
	return out
}
func (f *fakeFT) Checkpoints(context.Context, string, string) ([]Checkpoint, error) { return nil, nil }

type fakeQ struct {
	startUser string
	started   []schema.QuantizationJobRequest
	jobs      map[string]*schema.QuantizationJob
}

func (q *fakeQ) Start(_ context.Context, user string, r schema.QuantizationJobRequest) (*schema.QuantizationJobResponse, error) {
	q.startUser = user
	q.started = append(q.started, r)
	return &schema.QuantizationJobResponse{ID: "q1", Status: "queued"}, nil
}
func (q *fakeQ) Get(user, id string) (*schema.QuantizationJob, error) {
	j, ok := q.jobs[id]
	if !ok || (user != "" && j.UserID != user) {
		return nil, ErrNotFound
	}
	return j, nil
}
func (q *fakeQ) List(user string) []*schema.QuantizationJob {
	var out []*schema.QuantizationJob
	for _, j := range q.jobs {
		if user == "" || j.UserID == user {
			out = append(out, j)
		}
	}
	return out
}

type backends struct{}

func (backends) FineTune() []string     { return []string{"trl"} }
func (backends) Quantization() []string { return []string{"llama-cpp-quantization"} }

func call(spec Spec, mode, user, args string) (json.RawMessage, error) {
	return spec.Handler(context.Background(), Call{AddonID: "fine-tuning", OperationID: "op1", Actor: contract.Actor{UserID: user, Mode: mode}, Arguments: json.RawMessage(args)})
}

func register(ft FineTuner, q Quantizer) map[string]Spec {
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	Expect(RegisterEngineJobs(b, ft, q, backends{})).To(Succeed())
	return b.specs
}

// registerWithout is register with a host that has no engine installed.
func registerWithout(ft FineTuner, q Quantizer) map[string]Spec {
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	Expect(RegisterEngineJobs(b, ft, q, noBackends{})).To(Succeed())
	return b.specs
}

type noBackends struct{}

func (noBackends) FineTune() []string     { return nil }
func (noBackends) Quantization() []string { return nil }

func specs(ft FineTuner) map[string]Spec {
	return register(ft, nil)
}

// serveEngine runs one engine operation through the broker's HTTP channel, as
// the add-on process would call it, for a named user holding the features
// that has allows. The jobs the fakes hold belong to u1 so that a call with
// valid arguments answers 200 for an actor who holds the feature.
func serveEngine(op, args string, has func(user, feature string) bool) (int, contract.HostResponse) {
	ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"j1": {ID: "j1", UserID: "u1"}}}
	q := &fakeQ{jobs: map[string]*schema.QuantizationJob{"q1": {ID: "q1", UserID: "u1"}}}
	b := New(NewInFlight(), has, contract.DefaultLimits())
	Expect(RegisterEngineJobs(b, ft, q, backends{})).To(Succeed())
	end := b.inflight.Begin(env("fine-tuning", "op1", "apply", time.Now().Add(time.Hour)))
	defer end()
	h := b.Handler("fine-tuning", "secret", []string{"finetune.", "quantize."})
	body, _ := json.Marshal(contract.HostRequest{OperationID: "op1", Op: op, Arguments: json.RawMessage(args)})
	req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(body))
	req.Header.Set(contract.HostCredentialHeader, "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out contract.HostResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

var _ = Describe("Fine-tuning and quantization job operations", func() {
	Describe("finetune", func() {
		It("start passes the same user key as the route", func() {
			ft := &fakeFT{}
			s := specs(ft)
			_, err := call(s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data"}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(ft.startUser).To(Equal("u1"), "named user")
			// Authentication off: the route passes "" so the job shows in the UI list.
			_, err = call(s["finetune.start"], "local-owner", "local-owner", `{"model":"org/base","datasetSource":"org/data"}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(ft.startUser).To(BeEmpty(), "local owner")
		})

		It("start refuses a local path as model or dataset", func() {
			s := specs(&fakeFT{})
			for _, args := range []string{
				`{"model":"/etc/passwd","datasetSource":"org/data"}`,
				`{"model":"../x","datasetSource":"org/data"}`,
				`{"model":"org/base","datasetSource":"/var/data.jsonl"}`,
				`{"model":"org\\base","datasetSource":"org/data"}`,
			} {
				_, err := call(s["finetune.start"], "named-user", "u1", args)
				Expect(err).To(MatchError(ErrBadRequest), args)
			}
		})

		It("start refuses a backend that is not a fine-tuning backend", func() {
			s := specs(&fakeFT{})
			_, err := call(s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data","backend":"llama-cpp"}`)
			Expect(err).To(MatchError(ErrBadRequest))
		})

		It("start names the missing fine-tuning engine, the default one included", func() {
			s := registerWithout(&fakeFT{}, nil)
			_, err := call(s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data"}`)
			Expect(err).To(MatchError(ErrBadRequest))
			Expect(err.Error()).To(ContainSubstring("fine-tuning engine trl is not installed"))
			_, err = call(s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data","backend":"../x"}`)
			Expect(err).To(MatchError(ErrBadRequest))
			Expect(err.Error()).NotTo(ContainSubstring(".."))
		})

		It("start refuses a field that is not in the tool schema", func() {
			s := specs(&fakeFT{})
			_, err := call(s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data","extraOptions":{"x":"y"}}`)
			Expect(err).To(MatchError(ErrBadRequest))
		})

		It("get of another user's job is not found", func() {
			ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"j1": {ID: "j1", UserID: "other"}}}
			s := specs(ft)
			_, err := call(s["finetune.get"], "named-user", "u1", `{"jobId":"j1"}`)
			Expect(err).To(MatchError(ErrNotFound))
		})

		It("list returns only the caller's jobs", func() {
			ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"a": {ID: "a", UserID: "u1"}, "b": {ID: "b", UserID: "u2"}}}
			s := specs(ft)
			out, err := call(s["finetune.list"], "named-user", "u1", `{}`)
			Expect(err).ToNot(HaveOccurred())
			var got struct {
				Jobs []struct {
					ID string `json:"id"`
				} `json:"jobs"`
			}
			_ = json.Unmarshal(out, &got)
			Expect(got.Jobs).To(HaveLen(1), "a caller must see only its own jobs: %s", out)
			Expect(got.Jobs[0].ID).To(Equal("a"))
		})

		It("a job result never carries a filesystem path", func() {
			ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"j1": {ID: "j1", UserID: "u1", OutputDir: "/data/fine-tune/j1", ExportStatus: "completed", ExportModelName: "my-model", Message: "saved to /data/fine-tune/j1/out"}}}
			s := specs(ft)
			out, _ := call(s["finetune.get"], "named-user", "u1", `{"jobId":"j1"}`)
			Expect(string(out)).ToNot(ContainSubstring("/data/"), "a result must not leak a server path")
			Expect(string(out)).ToNot(ContainSubstring("/models/"), "a result must not leak a server path")
			Expect(string(out)).To(ContainSubstring(`"exportedModelName":"my-model"`), "the registered model name is part of the result")
		})

		It("a second user's list does not show the first user's jobs", func() {
			ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"a": {ID: "a", UserID: "u1"}, "b": {ID: "b", UserID: "u2"}, "c": {ID: "c", UserID: "u1"}}}
			s := specs(ft)
			ids := func(user string) []string {
				out, err := call(s["finetune.list"], "named-user", user, `{}`)
				Expect(err).ToNot(HaveOccurred())
				var got struct {
					Jobs []struct {
						ID string `json:"id"`
					} `json:"jobs"`
				}
				_ = json.Unmarshal(out, &got)
				var r []string
				for _, j := range got.Jobs {
					r = append(r, j.ID)
				}
				return r
			}
			Expect(ids("u2")).To(Equal([]string{"b"}), "user 2 must see only its own job")
			Expect(ids("u1")).To(HaveLen(2), "user 1 must see its two jobs")
			// A user with no job sees none, not the others'.
			Expect(ids("u3")).To(BeEmpty(), "user 3 must see nothing")
		})
	})

	Describe("quantize", func() {
		It("start passes the same user key as the route", func() {
			q := &fakeQ{}
			s := register(nil, q)
			_, err := call(s["quantize.start"], "named-user", "u1", `{"model":"org/base","quantizationType":"q8_0"}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(q.startUser).To(Equal("u1"), "named user")
			_, err = call(s["quantize.start"], "local-owner", "local-owner", `{"model":"org/base"}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(q.startUser).To(BeEmpty(), "local owner")
			Expect(q.started[0].QuantizationType).To(Equal("q8_0"), "quantization type not passed")
		})

		It("start refuses a local path as model", func() {
			s := register(nil, &fakeQ{})
			for _, args := range []string{`{"model":"/models/x.gguf"}`, `{"model":"./x"}`, `{"model":"a\\b"}`} {
				_, err := call(s["quantize.start"], "named-user", "u1", args)
				Expect(err).To(MatchError(ErrBadRequest), args)
			}
		})

		It("start refuses a backend that is not a quantization backend", func() {
			s := register(nil, &fakeQ{})
			_, err := call(s["quantize.start"], "named-user", "u1", `{"model":"org/base","backend":"trl"}`)
			Expect(err).To(MatchError(ErrBadRequest))
		})

		It("start names the missing quantization engine, the default one included", func() {
			s := registerWithout(nil, &fakeQ{})
			_, err := call(s["quantize.start"], "named-user", "u1", `{"model":"org/base"}`)
			Expect(err).To(MatchError(ErrBadRequest))
			Expect(err.Error()).To(ContainSubstring("quantization engine llama-cpp-quantization is not installed"))
		})

		It("start refuses a field that is not in the tool schema", func() {
			s := register(nil, &fakeQ{})
			_, err := call(s["quantize.start"], "named-user", "u1", `{"model":"org/base","extraOptions":{"x":"y"}}`)
			Expect(err).To(MatchError(ErrBadRequest))
		})

		It("get of another user's job is not found", func() {
			q := &fakeQ{jobs: map[string]*schema.QuantizationJob{"q1": {ID: "q1", UserID: "other", OutputFile: "/data/q1/m.gguf"}}}
			s := register(nil, q)
			_, err := call(s["quantize.get"], "named-user", "u1", `{"jobId":"q1"}`)
			Expect(err).To(MatchError(ErrNotFound))
		})

		It("list returns only the caller's jobs without a path", func() {
			q := &fakeQ{jobs: map[string]*schema.QuantizationJob{
				"a": {ID: "a", UserID: "u1", OutputFile: "/data/a/m.gguf", OutputDir: "/data/a"},
				"b": {ID: "b", UserID: "u2"},
			}}
			s := register(nil, q)
			out, err := call(s["quantize.list"], "named-user", "u1", `{}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).ToNot(ContainSubstring("/data/"), "a caller sees no path")
			Expect(string(out)).ToNot(ContainSubstring(`"id":"b"`), "a caller sees only its own jobs")
			Expect(string(out)).To(ContainSubstring(`"id":"a"`), "a caller sees its own jobs")
		})
	})

	It("registers no operation for nil services", func() {
		b := New(NewInFlight(), always, contract.DefaultLimits())
		Expect(RegisterEngineJobs(b, nil, nil, backends{})).To(Succeed())
		Expect(b.Ops()).To(BeEmpty(), "no service, no operation")
	})

	// Every engine operation is checked against its own feature: refused
	// without it, refused with only the other family's feature, and answered
	// when the actor holds the operation's feature.
	Describe("feature gate", func() {
		const fineTune, quant = "fine_tuning", "quantization"
		for _, c := range []struct {
			op, args, feature, other string
		}{
			{"finetune.start", `{"model":"org/base","datasetSource":"org/data"}`, fineTune, quant},
			{"finetune.get", `{"jobId":"j1"}`, fineTune, quant},
			{"finetune.list", `{}`, fineTune, quant},
			{"finetune.checkpoints", `{"jobId":"j1"}`, fineTune, quant},
			{"quantize.start", `{"model":"org/base"}`, quant, fineTune},
			{"quantize.get", `{"jobId":"q1"}`, quant, fineTune},
			{"quantize.list", `{}`, quant, fineTune},
		} {
			It(c.op+" needs its feature", func() {
				none := func(string, string) bool { return false }
				code, out := serveEngine(c.op, c.args, none)
				Expect(code).To(Equal(http.StatusForbidden), "without any feature")
				Expect(out.Error).ToNot(BeNil())
				Expect(out.Error.Code).To(Equal(contract.HostFeature), "without any feature")
				otherOnly := func(_, f string) bool { return f == c.other }
				code, out = serveEngine(c.op, c.args, otherOnly)
				Expect(code).To(Equal(http.StatusForbidden), "with only %s", c.other)
				Expect(out.Error).ToNot(BeNil())
				Expect(out.Error.Code).To(Equal(contract.HostFeature), "with only %s", c.other)
				ownOnly := func(_, f string) bool { return f == c.feature }
				code, out = serveEngine(c.op, c.args, ownOnly)
				Expect(code).To(Equal(http.StatusOK), "with %s: %+v", c.feature, out.Error)
			})
		}
	})

	Describe("dataset source", func() {
		// A dataset reference that is not an id is refused, the field is
		// named, and the service is never reached.
		It("must be a gallery or Hugging Face ID", func() {
			bad := []string{
				"https://example.com/data.jsonl", // URL
				"hf://org/data",                  // scheme
				"file:///etc/passwd",             // scheme and absolute path
				"/var/data.jsonl",                // absolute path
				"../data",                        // parent segment
				"org/../data",                    // parent segment inside an id
				"org/..",                         // parent segment at the end
				"org data",                       // space
				"org/data/extra",                 // more than one separator
				`C:\data`,                        // backslash
				"",                               // empty
			}
			for _, src := range bad {
				ft := &fakeFT{}
				s := specs(ft)
				args, _ := json.Marshal(map[string]string{"model": "org/base", "datasetSource": src})
				_, err := call(s["finetune.start"], "named-user", "u1", string(args))
				Expect(err).To(MatchError(ErrBadRequest), "%q", src)
				Expect(err.Error()).To(ContainSubstring("datasetSource"), "%q", src)
				Expect(ft.started).To(BeEmpty(), "%q reached the service", src)
			}
		})

		// The answer the add-on process receives through the channel.
		It("refusal is a 400 naming the field", func() {
			code, out := serveEngine("finetune.start", `{"model":"org/base","datasetSource":"https://example.com/d.jsonl"}`, func(string, string) bool { return true })
			Expect(code).To(Equal(http.StatusBadRequest))
			Expect(out.Error).ToNot(BeNil())
			Expect(out.Error.Code).To(Equal(contract.HostBadRequest))
			Expect(out.Error.Message).To(ContainSubstring("datasetSource"))
		})

		// Keeps the ids the UI uses.
		It("accepts Hugging Face and gallery IDs", func() {
			for _, src := range []string{"org/data", "HuggingFaceH4/ultrachat_200k", "my-dataset", "org/data-v1.0"} {
				ft := &fakeFT{}
				s := specs(ft)
				args, _ := json.Marshal(map[string]string{"model": "org/base", "datasetSource": src})
				_, err := call(s["finetune.start"], "named-user", "u1", string(args))
				Expect(err).ToNot(HaveOccurred(), "%q", src)
			}
		})

		It("accepts a dataset ref as dataset source", func() {
			ft := &fakeFT{}
			ds := &fakeDatasets{}
			b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
			Expect(RegisterEngineJobs(b, ft, nil, backends{})).To(Succeed())
			Expect(RegisterEngineDatasets(b, ds)).To(Succeed())
			raw, err := call(b.specs["finetune.dataset"], "named-user", "u1", datasetArgs("x.jsonl", "[]"))
			Expect(err).ToNot(HaveOccurred())
			var out datasetResult
			Expect(json.Unmarshal(raw, &out)).To(Succeed())
			start := b.specs["finetune.start"]
			_, err = call(start, "named-user", "u1", `{"model":"org/base","datasetSource":"`+out.DatasetRef+`"}`)
			Expect(err).ToNot(HaveOccurred())
			Expect(ft.started[len(ft.started)-1].DatasetSource).To(Equal(ds.paths[out.DatasetRef]), "the service must get the stored path")
			_, err = call(start, "named-user", "u2", `{"model":"org/base","datasetSource":"`+out.DatasetRef+`"}`)
			Expect(err).To(MatchError(ErrBadRequest), "another user's reference")
			_, err = call(start, "named-user", "u1", `{"model":"org/base","datasetSource":"ffffffff-nope.jsonl"}`)
			Expect(err).To(MatchError(ErrBadRequest), "an unknown reference")
		})
	})
})
