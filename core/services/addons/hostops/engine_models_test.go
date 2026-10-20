// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type fakeExporter struct {
	owner map[string]string
	cps   []Checkpoint
	path  string
	calls int
	user  string
	req   schema.ExportRequest
	err   error
}

func (f *fakeExporter) Checkpoints(_ context.Context, user, jobID string) ([]Checkpoint, error) {
	if o, ok := f.owner[jobID]; !ok || (user != "" && o != user) {
		return nil, fmt.Errorf("%w: job not found", ErrNotFound)
	}
	return f.cps, nil
}

func (f *fakeExporter) ExportModel(_ context.Context, user, jobID string, req schema.ExportRequest) (string, error) {
	f.calls++
	f.user = user
	f.req = req
	if o, ok := f.owner[jobID]; !ok || (user != "" && o != user) {
		return "", fmt.Errorf("%w: job not found", ErrNotFound)
	}
	if f.err != nil {
		return "", f.err
	}
	return f.path, nil
}

type fakeImporter struct {
	path  string
	calls int
	err   error
}

func (f *fakeImporter) ImportModel(_ context.Context, _ string, _ string, _ schema.QuantizationImportRequest) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.path, nil
}

// oneCheckpoint is the checkpoint a job of the fake has by default.
var oneCheckpoint = []Checkpoint{{Step: 1, Path: "/out/ckpt-1"}}

func newExporter(path string) *fakeExporter {
	return &fakeExporter{owner: map[string]string{"j1": "u1"}, cps: oneCheckpoint, path: path}
}

func modelSpecs(t *testing.T, ex Exporter, im Importer) map[string]Spec {
	t.Helper()
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	if err := RegisterEngineModels(b, ex, im); err != nil {
		t.Fatal(err)
	}
	return b.specs
}

// serveAs runs one operation through the channel handler as the given actor,
// with a live envelope for it, and returns the status and the answer.
func serveAs(t *testing.T, b *Broker, op, mode, user, args string) (int, contract.HostResponse) {
	t.Helper()
	end := b.inflight.Begin(contract.Envelope{
		Request:   contract.Request{AddonID: "fine-tuning", OperationID: "op1", Generation: 1, Action: "start", Arguments: json.RawMessage(`{}`)},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "c1", Mode: mode}, Scope: "apply", ExpiresAt: time.Now().Add(time.Minute)},
	})
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

// adminBroker builds a broker where every user holds the feature, and only the
// user "admin" is an administrator.
func adminBroker(t *testing.T, ex Exporter, im Importer) *Broker {
	t.Helper()
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	b.SetAdminCheck(func(actor contract.Actor) bool { return actor.UserID == "admin" })
	if err := RegisterEngineModels(b, ex, im); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestExportRegistersAModelAndReturnsOnlyItsName(t *testing.T) {
	ex := newExporter("/models/my-model")
	s := modelSpecs(t, ex, nil)
	out, err := call(t, s["finetune.export"], "named-user", "u1", `{"jobId":"j1","name":"my-model","exportFormat":"lora"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"model":"my-model"}` {
		t.Fatalf("got %s", out)
	}
	if strings.Contains(string(out), "/") {
		t.Fatalf("result carries a path: %s", out)
	}
	if ex.user != "u1" || ex.req.Name != "my-model" || ex.req.ExportFormat != "lora" {
		t.Fatalf("service got user=%q req=%+v", ex.user, ex.req)
	}
}

func TestExportUsesTheHighestStepCheckpoint(t *testing.T) {
	ex := newExporter("/models/my-model")
	ex.cps = []Checkpoint{{Step: 100, Path: "/out/ckpt-100"}, {Step: 300, Path: "/out/ckpt-300"}, {Step: 200, Path: "/out/ckpt-200"}}
	s := modelSpecs(t, ex, nil)
	if _, err := call(t, s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`); err != nil {
		t.Fatal(err)
	}
	if ex.req.CheckpointPath != "/out/ckpt-300" {
		t.Fatalf("export got checkpoint %q, want the highest step", ex.req.CheckpointPath)
	}
}

func TestExportOfAJobWithNoCheckpointIsAConflictNamingIt(t *testing.T) {
	ex := newExporter("/models/my-model")
	ex.cps = nil
	s := modelSpecs(t, ex, nil)
	_, err := call(t, s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "checkpoint") {
		t.Fatalf("got %v, want a conflict naming the checkpoint", err)
	}
	if ex.calls != 0 {
		t.Fatalf("service called %d times for a job with no checkpoint", ex.calls)
	}
}

func TestExportRefusesABadName(t *testing.T) {
	ex := newExporter("/models/x")
	s := modelSpecs(t, ex, nil)
	long := strings.Repeat("a", 65)
	for _, name := range []string{"../x", "a/b", "   ", long} {
		args, _ := json.Marshal(map[string]string{"jobId": "j1", "name": name})
		if _, err := call(t, s["finetune.export"], "named-user", "u1", string(args)); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%q: want ErrBadRequest, got %v", name, err)
		}
	}
	if ex.calls != 0 {
		t.Fatalf("service called %d times for a refused name", ex.calls)
	}
}

func TestExportRefusesAnExportFormatThatIsNotListed(t *testing.T) {
	ex := newExporter("/models/x")
	s := modelSpecs(t, ex, nil)
	for _, f := range []string{"adapter", "merged_8bit", "GGUF", "gguf "} {
		args, _ := json.Marshal(map[string]string{"jobId": "j1", "exportFormat": f})
		if _, err := call(t, s["finetune.export"], "named-user", "u1", string(args)); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%q: want ErrBadRequest, got %v", f, err)
		}
	}
	for _, f := range []string{"lora", "merged_16bit", "merged_4bit", "gguf"} {
		args, _ := json.Marshal(map[string]string{"jobId": "j1", "exportFormat": f})
		if _, err := call(t, s["finetune.export"], "named-user", "u1", string(args)); err != nil {
			t.Fatalf("%q: %v", f, err)
		}
	}
}

func TestExportOfAnotherUsersJobIsNotFound(t *testing.T) {
	ex := newExporter("/models/x")
	ex.owner = map[string]string{"j1": "u2"}
	s := modelSpecs(t, ex, nil)
	if _, err := call(t, s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestExportRunsOncePerOperationID(t *testing.T) {
	ex := newExporter("/models/my-model")
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	if err := RegisterEngineModels(b, ex, nil); err != nil {
		t.Fatal(err)
	}
	spec := b.specs["finetune.export"]
	c := Call{AddonID: "fine-tuning", OperationID: "op1", Actor: contract.Actor{UserID: "u1", Mode: "named-user"}, Arguments: json.RawMessage(`{"jobId":"j1"}`)}
	first, err := b.run(context.Background(), spec, c)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.run(context.Background(), spec, c)
	if err != nil {
		t.Fatal(err)
	}
	if ex.calls != 1 || string(first) != string(second) {
		t.Fatalf("calls=%d first=%s second=%s", ex.calls, first, second)
	}
}

func TestExportRefusesAReturnedPathThatIsNotAModelName(t *testing.T) {
	ex := newExporter("/models/bad name")
	s := modelSpecs(t, ex, nil)
	if _, err := call(t, s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`); err == nil || errors.Is(err, ErrBadRequest) {
		t.Fatalf("want an outage error, got %v", err)
	}
}

func TestImportRegistersTheQuantizedModelAndReturnsOnlyItsName(t *testing.T) {
	im := &fakeImporter{path: "/models/q-model"}
	s := modelSpecs(t, nil, im)
	out, err := call(t, s["quantize.import"], "named-user", "u1", `{"jobId":"q1","name":"q-model"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"model":"q-model"}` {
		t.Fatalf("got %s", out)
	}
}

func TestImportOfAJobThatIsNotCompletedIsAConflict(t *testing.T) {
	im := &fakeImporter{err: fmt.Errorf("%w: job q1 is not completed", ErrConflict)}
	s := modelSpecs(t, nil, im)
	if _, err := call(t, s["quantize.import"], "named-user", "u1", `{"jobId":"q1"}`); !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v", err)
	}
}

func TestModelOperationsAreApplyAndIdempotentAndAdminOnly(t *testing.T) {
	s := modelSpecs(t, newExporter("/models/x"), &fakeImporter{})
	for _, op := range []string{"finetune.export", "quantize.import"} {
		if s[op].Scope != "apply" || !s[op].Idempotent || !s[op].Admin {
			t.Fatalf("%s: scope=%q idempotent=%v admin=%v", op, s[op].Scope, s[op].Idempotent, s[op].Admin)
		}
	}
	if s["finetune.export"].Feature != featureFineTuning || s["quantize.import"].Feature != featureQuantization {
		t.Fatalf("features: %q %q", s["finetune.export"].Feature, s["quantize.import"].Feature)
	}
}

func TestExportIsRefusedForAFeatureHoldingNonAdmin(t *testing.T) {
	ex := newExporter("/models/my-model")
	b := adminBroker(t, ex, nil)
	code, out := serveAs(t, b, "finetune.export", "named-user", "u1", `{"jobId":"j1"}`)
	if code != http.StatusForbidden || out.Error == nil || out.Error.Code != contract.HostFeature {
		t.Fatalf("%d %+v", code, out.Error)
	}
	if ex.calls != 0 {
		t.Fatalf("service called %d times for a non-admin", ex.calls)
	}
}

func TestExportIsAllowedForAnAdmin(t *testing.T) {
	ex := newExporter("/models/my-model")
	ex.owner = map[string]string{"j1": "admin"}
	b := adminBroker(t, ex, nil)
	code, out := serveAs(t, b, "finetune.export", "named-user", "admin", `{"jobId":"j1"}`)
	if code != http.StatusOK || out.Error != nil {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestImportIsRefusedForAFeatureHoldingNonAdmin(t *testing.T) {
	im := &fakeImporter{path: "/models/q-model"}
	b := adminBroker(t, nil, im)
	code, out := serveAs(t, b, "quantize.import", "named-user", "u1", `{"jobId":"q1"}`)
	if code != http.StatusForbidden || out.Error == nil || out.Error.Code != contract.HostFeature {
		t.Fatalf("%d %+v", code, out.Error)
	}
	if im.calls != 0 {
		t.Fatalf("service called %d times for a non-admin", im.calls)
	}
}

func TestImportIsAllowedForAnAdmin(t *testing.T) {
	im := &fakeImporter{path: "/models/q-model"}
	b := adminBroker(t, nil, im)
	code, out := serveAs(t, b, "quantize.import", "named-user", "admin", `{"jobId":"q1"}`)
	if code != http.StatusOK || out.Error != nil {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

// The broker does not shortcut the local owner: the admin check decides in
// every mode, so a local-owner actor the check refuses is refused.
func TestTheAdminCheckDecidesInEveryMode(t *testing.T) {
	ex := newExporter("/models/my-model")
	ex.owner = map[string]string{"j1": "u1"}
	b := adminBroker(t, ex, nil)
	if code, out := serveAs(t, b, "finetune.export", "local-owner", "u1", `{"jobId":"j1"}`); code != http.StatusForbidden || out.Error.Code != contract.HostFeature {
		t.Fatalf("local-owner actor the check refuses: %d %+v", code, out.Error)
	}
	if ex.calls != 0 {
		t.Fatalf("service called %d times for a refused actor", ex.calls)
	}
}
