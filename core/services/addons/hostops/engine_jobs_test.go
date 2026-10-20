// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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

func call(t *testing.T, spec Spec, mode, user, args string) (json.RawMessage, error) {
	t.Helper()
	return spec.Handler(context.Background(), Call{AddonID: "fine-tuning", OperationID: "op1", Actor: contract.Actor{UserID: user, Mode: mode}, Arguments: json.RawMessage(args)})
}

func register(t *testing.T, ft FineTuner, q Quantizer) map[string]Spec {
	t.Helper()
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	if err := RegisterEngineJobs(b, ft, q, backends{}); err != nil {
		t.Fatal(err)
	}
	return b.specs
}

func specs(t *testing.T, ft FineTuner) map[string]Spec {
	t.Helper()
	return register(t, ft, nil)
}

func TestStartPassesTheSameUserKeyAsTheRoute(t *testing.T) {
	ft := &fakeFT{}
	s := specs(t, ft)
	if _, err := call(t, s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data"}`); err != nil || ft.startUser != "u1" {
		t.Fatalf("named user: %v %q", err, ft.startUser)
	}
	// Authentication off: the route passes "" so the job shows in the UI list.
	if _, err := call(t, s["finetune.start"], "local-owner", "local-owner", `{"model":"org/base","datasetSource":"org/data"}`); err != nil || ft.startUser != "" {
		t.Fatalf("local owner: %v %q", err, ft.startUser)
	}
}

func TestStartRefusesALocalPathAsModelOrDataset(t *testing.T) {
	s := specs(t, &fakeFT{})
	for _, args := range []string{
		`{"model":"/etc/passwd","datasetSource":"org/data"}`,
		`{"model":"../x","datasetSource":"org/data"}`,
		`{"model":"org/base","datasetSource":"/var/data.jsonl"}`,
		`{"model":"org\\base","datasetSource":"org/data"}`,
	} {
		if _, err := call(t, s["finetune.start"], "named-user", "u1", args); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%s: want ErrBadRequest, got %v", args, err)
		}
	}
}

func TestStartRefusesABackendThatIsNotAFineTuningBackend(t *testing.T) {
	s := specs(t, &fakeFT{})
	if _, err := call(t, s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data","backend":"llama-cpp"}`); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestStartRefusesAFieldThatIsNotInTheToolSchema(t *testing.T) {
	s := specs(t, &fakeFT{})
	if _, err := call(t, s["finetune.start"], "named-user", "u1", `{"model":"org/base","datasetSource":"org/data","extraOptions":{"x":"y"}}`); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestGetOfAnotherUsersJobIsNotFound(t *testing.T) {
	ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"j1": {ID: "j1", UserID: "other"}}}
	s := specs(t, ft)
	if _, err := call(t, s["finetune.get"], "named-user", "u1", `{"jobId":"j1"}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestListReturnsOnlyTheCallersJobs(t *testing.T) {
	ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"a": {ID: "a", UserID: "u1"}, "b": {ID: "b", UserID: "u2"}}}
	s := specs(t, ft)
	out, err := call(t, s["finetune.list"], "named-user", "u1", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Jobs []struct {
			ID string `json:"id"`
		} `json:"jobs"`
	}
	_ = json.Unmarshal(out, &got)
	if len(got.Jobs) != 1 || got.Jobs[0].ID != "a" {
		t.Fatalf("a caller must see only its own jobs: %s", out)
	}
}

func TestAJobResultNeverCarriesAFilesystemPath(t *testing.T) {
	ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"j1": {ID: "j1", UserID: "u1", OutputDir: "/data/fine-tune/j1", ExportStatus: "completed", ExportModelName: "my-model", Message: "saved to /data/fine-tune/j1/out"}}}
	s := specs(t, ft)
	out, _ := call(t, s["finetune.get"], "named-user", "u1", `{"jobId":"j1"}`)
	if strings.Contains(string(out), "/data/") || strings.Contains(string(out), "/models/") {
		t.Fatalf("a result must not leak a server path: %s", out)
	}
	if !strings.Contains(string(out), `"exportedModelName":"my-model"`) {
		t.Fatalf("the registered model name is part of the result: %s", out)
	}
}

func TestQuantizeStartPassesTheSameUserKeyAsTheRoute(t *testing.T) {
	q := &fakeQ{}
	s := register(t, nil, q)
	if _, err := call(t, s["quantize.start"], "named-user", "u1", `{"model":"org/base","quantizationType":"q8_0"}`); err != nil || q.startUser != "u1" {
		t.Fatalf("named user: %v %q", err, q.startUser)
	}
	if _, err := call(t, s["quantize.start"], "local-owner", "local-owner", `{"model":"org/base"}`); err != nil || q.startUser != "" {
		t.Fatalf("local owner: %v %q", err, q.startUser)
	}
	if got := q.started[0].QuantizationType; got != "q8_0" {
		t.Fatalf("quantization type not passed: %q", got)
	}
}

func TestQuantizeStartRefusesALocalPathAsModel(t *testing.T) {
	s := register(t, nil, &fakeQ{})
	for _, args := range []string{`{"model":"/models/x.gguf"}`, `{"model":"./x"}`, `{"model":"a\\b"}`} {
		if _, err := call(t, s["quantize.start"], "named-user", "u1", args); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%s: want ErrBadRequest, got %v", args, err)
		}
	}
}

func TestQuantizeStartRefusesABackendThatIsNotAQuantizationBackend(t *testing.T) {
	s := register(t, nil, &fakeQ{})
	if _, err := call(t, s["quantize.start"], "named-user", "u1", `{"model":"org/base","backend":"trl"}`); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestQuantizeStartRefusesAFieldThatIsNotInTheToolSchema(t *testing.T) {
	s := register(t, nil, &fakeQ{})
	if _, err := call(t, s["quantize.start"], "named-user", "u1", `{"model":"org/base","extraOptions":{"x":"y"}}`); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestQuantizeGetOfAnotherUsersJobIsNotFound(t *testing.T) {
	q := &fakeQ{jobs: map[string]*schema.QuantizationJob{"q1": {ID: "q1", UserID: "other", OutputFile: "/data/q1/m.gguf"}}}
	s := register(t, nil, q)
	if _, err := call(t, s["quantize.get"], "named-user", "u1", `{"jobId":"q1"}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestQuantizeListReturnsOnlyTheCallersJobsWithoutAPath(t *testing.T) {
	q := &fakeQ{jobs: map[string]*schema.QuantizationJob{
		"a": {ID: "a", UserID: "u1", OutputFile: "/data/a/m.gguf", OutputDir: "/data/a"},
		"b": {ID: "b", UserID: "u2"},
	}}
	s := register(t, nil, q)
	out, err := call(t, s["quantize.list"], "named-user", "u1", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "/data/") || strings.Contains(string(out), `"id":"b"`) || !strings.Contains(string(out), `"id":"a"`) {
		t.Fatalf("a caller sees only its own jobs and no path: %s", out)
	}
}

func TestNilServicesRegisterNoOperation(t *testing.T) {
	b := New(NewInFlight(), always, contract.DefaultLimits())
	if err := RegisterEngineJobs(b, nil, nil, backends{}); err != nil {
		t.Fatal(err)
	}
	if ops := b.Ops(); len(ops) != 0 {
		t.Fatalf("no service, no operation: %v", ops)
	}
}

// serveEngine runs one engine operation through the broker's HTTP channel, as
// the add-on process would call it, for a named user holding the features
// that has allows. The jobs the fakes hold belong to u1 so that a call with
// valid arguments answers 200 for an actor who holds the feature.
func serveEngine(t *testing.T, op, args string, has func(user, feature string) bool) (int, contract.HostResponse) {
	t.Helper()
	ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"j1": {ID: "j1", UserID: "u1"}}}
	q := &fakeQ{jobs: map[string]*schema.QuantizationJob{"q1": {ID: "q1", UserID: "u1"}}}
	b := New(NewInFlight(), has, contract.DefaultLimits())
	if err := RegisterEngineJobs(b, ft, q, backends{}); err != nil {
		t.Fatal(err)
	}
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

// TestEachEngineOperationNeedsItsFeature checks every engine operation against
// its own feature: refused without it, refused with only the other family's
// feature, and answered when the actor holds the operation's feature.
func TestEachEngineOperationNeedsItsFeature(t *testing.T) {
	const fineTune, quant = "fine_tuning", "quantization"
	cases := []struct {
		op, args, feature, other string
	}{
		{"finetune.start", `{"model":"org/base","datasetSource":"org/data"}`, fineTune, quant},
		{"finetune.get", `{"jobId":"j1"}`, fineTune, quant},
		{"finetune.list", `{}`, fineTune, quant},
		{"finetune.checkpoints", `{"jobId":"j1"}`, fineTune, quant},
		{"quantize.start", `{"model":"org/base"}`, quant, fineTune},
		{"quantize.get", `{"jobId":"q1"}`, quant, fineTune},
		{"quantize.list", `{}`, quant, fineTune},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			none := func(string, string) bool { return false }
			if code, out := serveEngine(t, c.op, c.args, none); code != http.StatusForbidden || out.Error == nil || out.Error.Code != contract.HostFeature {
				t.Fatalf("without any feature: %d %+v", code, out.Error)
			}
			otherOnly := func(_, f string) bool { return f == c.other }
			if code, out := serveEngine(t, c.op, c.args, otherOnly); code != http.StatusForbidden || out.Error == nil || out.Error.Code != contract.HostFeature {
				t.Fatalf("with only %s: %d %+v", c.other, code, out.Error)
			}
			ownOnly := func(_, f string) bool { return f == c.feature }
			if code, out := serveEngine(t, c.op, c.args, ownOnly); code != http.StatusOK {
				t.Fatalf("with %s: %d %+v", c.feature, code, out.Error)
			}
		})
	}
}

// TestDatasetSourceMustBeAGalleryOrHuggingFaceID refuses a dataset reference
// that is not an id, names the field, and never reaches the service.
func TestDatasetSourceMustBeAGalleryOrHuggingFaceID(t *testing.T) {
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
		s := specs(t, ft)
		args, _ := json.Marshal(map[string]string{"model": "org/base", "datasetSource": src})
		_, err := call(t, s["finetune.start"], "named-user", "u1", string(args))
		if !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "datasetSource") {
			t.Fatalf("%q: want a bad request naming datasetSource, got %v", src, err)
		}
		if len(ft.started) != 0 {
			t.Fatalf("%q reached the service", src)
		}
	}
}

// TestDatasetSourceRefusalIsA400NamingTheField checks the answer the add-on
// process receives through the channel.
func TestDatasetSourceRefusalIsA400NamingTheField(t *testing.T) {
	code, out := serveEngine(t, "finetune.start", `{"model":"org/base","datasetSource":"https://example.com/d.jsonl"}`, func(string, string) bool { return true })
	if code != http.StatusBadRequest || out.Error == nil || out.Error.Code != contract.HostBadRequest || !strings.Contains(out.Error.Message, "datasetSource") {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

// TestDatasetSourceAcceptsHuggingFaceAndGalleryIDs keeps the ids the UI uses.
func TestDatasetSourceAcceptsHuggingFaceAndGalleryIDs(t *testing.T) {
	for _, src := range []string{"org/data", "HuggingFaceH4/ultrachat_200k", "my-dataset", "org/data-v1.0"} {
		ft := &fakeFT{}
		s := specs(t, ft)
		args, _ := json.Marshal(map[string]string{"model": "org/base", "datasetSource": src})
		if _, err := call(t, s["finetune.start"], "named-user", "u1", string(args)); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
	}
}

func TestStartAcceptsADatasetRefAsDatasetSource(t *testing.T) {
	ft := &fakeFT{}
	ds := &fakeDatasets{}
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	if err := RegisterEngineJobs(b, ft, nil, backends{}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterEngineDatasets(b, ds); err != nil {
		t.Fatal(err)
	}
	raw, err := call(t, b.specs["finetune.dataset"], "named-user", "u1", datasetArgs(t, "x.jsonl", "[]"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		DatasetRef string `json:"datasetRef"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	start := b.specs["finetune.start"]
	if _, err := call(t, start, "named-user", "u1", `{"model":"org/base","datasetSource":"`+out.DatasetRef+`"}`); err != nil {
		t.Fatal(err)
	}
	if got := ft.started[len(ft.started)-1].DatasetSource; got != ds.paths[out.DatasetRef] {
		t.Fatalf("the service got %q, want the stored path", got)
	}
	if _, err := call(t, start, "named-user", "u2", `{"model":"org/base","datasetSource":"`+out.DatasetRef+`"}`); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("another user's reference: want ErrBadRequest, got %v", err)
	}
	if _, err := call(t, start, "named-user", "u1", `{"model":"org/base","datasetSource":"ffffffff-nope.jsonl"}`); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("an unknown reference: want ErrBadRequest, got %v", err)
	}
}

func TestASecondUsersListDoesNotShowTheFirstUsersJobs(t *testing.T) {
	ft := &fakeFT{jobs: map[string]*schema.FineTuneJob{"a": {ID: "a", UserID: "u1"}, "b": {ID: "b", UserID: "u2"}, "c": {ID: "c", UserID: "u1"}}}
	s := specs(t, ft)
	ids := func(user string) []string {
		out, err := call(t, s["finetune.list"], "named-user", user, `{}`)
		if err != nil {
			t.Fatal(err)
		}
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
	if got := ids("u2"); len(got) != 1 || got[0] != "b" {
		t.Fatalf("user 2 must see only its own job, got %v", got)
	}
	if got := ids("u1"); len(got) != 2 {
		t.Fatalf("user 1 must see its two jobs, got %v", got)
	}
	// A user with no job sees none, not the others'.
	if got := ids("u3"); len(got) != 0 {
		t.Fatalf("user 3 must see nothing, got %v", got)
	}
}
