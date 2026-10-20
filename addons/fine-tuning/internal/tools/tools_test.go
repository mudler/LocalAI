// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var ctx = context.Background()

type call struct {
	opID, op string
	args     map[string]any
}

// fakeEngine records host operations and answers from canned results. A
// result of type error is returned as the failure of that operation.
type fakeEngine struct {
	mu     sync.Mutex
	result map[string]any
	calls  []call
}

func (f *fakeEngine) Do(_ context.Context, opID, op string, args, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var a map[string]any
	if args != nil {
		b, _ := json.Marshal(args)
		_ = json.Unmarshal(b, &a)
	}
	f.calls = append(f.calls, call{opID, op, a})
	r, ok := f.result[op]
	if !ok {
		r = map[string]any{}
	}
	if err, isErr := r.(error); isErr {
		return err
	}
	if out != nil {
		b, _ := json.Marshal(r)
		return json.Unmarshal(b, out)
	}
	return nil
}

func (f *fakeEngine) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.op)
	}
	return out
}

func (f *fakeEngine) count(op string) int {
	n := 0
	for _, o := range f.ops() {
		if o == op {
			n++
		}
	}
	return n
}

func (f *fakeEngine) last(op string) call {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].op == op {
			return f.calls[i]
		}
	}
	return call{}
}

func envelope(tool, opID, resource, args string) contract.Envelope {
	m := map[string]any{}
	if args != "" {
		_ = json.Unmarshal([]byte(args), &m)
	}
	m["tool"] = tool
	raw, _ := json.Marshal(m)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "fine-tuning", ResourceID: resource, OperationID: opID, Generation: 1, Action: "apply", Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"}, Scope: "apply", ExpiresAt: time.Now().Add(time.Minute)},
	}
}

const startJSON = `{"name":"my run","model":"org/base","datasetSource":"org/data"}`

// started returns a runtime that already holds the job job-1 for resource res1.
func started(t *testing.T) (contract.Runtime, *fakeEngine) {
	t.Helper()
	eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, t.TempDir())
	rec, err := rt.Apply(ctx, envelope("fine-tune-start", "create-res1", "res1", startJSON), contract.Revision{Schema: 1})
	if err != nil || rec.State != contract.ReceiptCommitted {
		t.Fatalf("setup: %v %+v", err, rec)
	}
	return rt, eng
}

func TestStartCallsTheEngineOnceAndRemembersTheJob(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, t.TempDir())
	rec, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 0})
	if err != nil || rec.State != contract.ReceiptCommitted || rec.Revision.Schema == 0 {
		t.Fatalf("%v %+v", err, rec)
	}
	if got := eng.ops(); !reflect.DeepEqual(got, []string{"finetune.start"}) {
		t.Fatalf("ops %v", got)
	}
	// The same operation again does not start a second job.
	again, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 1})
	if err != nil || again.State != contract.ReceiptCommitted {
		t.Fatalf("%v %+v", err, again)
	}
	if n := eng.count("finetune.start"); n != 1 {
		t.Fatalf("started %d jobs", n)
	}
}

func TestStartSendsOnlyTheFieldsOfTheEngineOperation(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, t.TempDir())
	args := `{"name":"my run","model":"org/base","datasetSource":"org/data","numEpochs":2,"learningRate":0.0002,"adapterRank":8}`
	if _, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", args), contract.Revision{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	c := eng.last("finetune.start")
	if c.opID != "op1" {
		t.Fatalf("the call must carry the operation of the request, got %q", c.opID)
	}
	for _, banned := range []string{"tool", "name", "resourceId"} {
		if _, ok := c.args[banned]; ok {
			t.Fatalf("the engine must not receive %q: %v", banned, c.args)
		}
	}
	if c.args["model"] != "org/base" || c.args["numEpochs"] != float64(2) || c.args["adapterRank"] != float64(8) {
		t.Fatalf("args %v", c.args)
	}
}

func TestPreviewOfStartHasANonzeroRevisionAndCreatesNothing(t *testing.T) {
	eng := &fakeEngine{}
	rt := New(eng, t.TempDir())
	p, err := rt.Preview(ctx, envelope("fine-tune-start", "op1", "res1", startJSON))
	if err != nil || p.Revision.Schema == 0 || len(eng.ops()) != 0 {
		t.Fatalf("%v %+v %v", err, p, eng.ops())
	}
	if !json.Valid(p.Summary) {
		t.Fatalf("summary %s", p.Summary)
	}
	if _, err := rt.Preview(ctx, envelope("fine-tune-start", "op1", "res1", `{"name":"x","model":"","datasetSource":"d"}`)); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("an empty model must be refused, got %v", err)
	}
}

func TestStatusReadsTheJobOfTheResource(t *testing.T) {
	rt, eng := started(t)
	eng.result["finetune.get"] = map[string]any{"id": "job-1", "status": "training", "model": "org/base"}
	res, err := rt.Read(ctx, envelope("fine-tune-status", "r1", "res1", ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := eng.last("finetune.get").args["jobId"]; got != "job-1" {
		t.Fatalf("jobId %v", got)
	}
	var v map[string]any
	if err := json.Unmarshal(res.Payload, &v); err != nil {
		t.Fatal(err)
	}
	if v["name"] != "my run" || v["status"] != "training" {
		t.Fatalf("payload %v", v)
	}
	rev, _ := v["revision"].(map[string]any)
	if rev["schema"] == nil || rev["schema"] == float64(0) {
		t.Fatalf("inspect needs a revision: %v", v)
	}
	if _, has := v["id"]; has {
		t.Fatalf("the engine job ID must not reach the model: %v", v)
	}
	if _, has := v["progressPercent"]; has {
		t.Fatalf("a snapshot percent is unreliable and must not be reported: %v", v)
	}
}

func TestCheckpointsReadTheJobOfTheResource(t *testing.T) {
	rt, eng := started(t)
	eng.result["finetune.checkpoints"] = map[string]any{"checkpoints": []any{map[string]any{"step": 10}}}
	res, err := rt.Read(ctx, envelope("fine-tune-checkpoints", "r1", "res1", ""))
	if err != nil || !strings.Contains(string(res.Payload), `"step":10`) {
		t.Fatalf("%v %s", err, res.Payload)
	}
	if eng.last("finetune.checkpoints").args["jobId"] != "job-1" {
		t.Fatal("wrong job")
	}
}

func TestAToolCannotControlAJobItDidNotStart(t *testing.T) {
	eng := &fakeEngine{}
	rt := New(eng, t.TempDir())
	if _, err := rt.Apply(ctx, envelope("fine-tune-stop", "op2", "nope", `{}`), contract.Revision{Schema: 1}); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("stop: %v", err)
	}
	if _, err := rt.Apply(ctx, envelope("fine-tune-export", "op3", "nope", `{}`), contract.Revision{Schema: 1}); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("export: %v", err)
	}
	if _, err := rt.Read(ctx, envelope("fine-tune-status", "r1", "nope", "")); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("status: %v", err)
	}
	if _, err := rt.Preview(ctx, envelope("fine-tune-stop", "op2", "nope", `{}`)); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("preview: %v", err)
	}
	if n := len(eng.ops()); n != 0 {
		t.Fatalf("the engine was called %d times: %v", n, eng.ops())
	}
}

func TestStopAndExportUseTheStoredJobAndAdvanceTheRevision(t *testing.T) {
	rt, eng := started(t)
	eng.result["finetune.stop"] = map[string]any{}
	eng.result["finetune.export"] = map[string]any{"model": "my-model"}
	p, err := rt.Preview(ctx, envelope("fine-tune-stop", "op2", "res1", `{"saveCheckpoint":true}`))
	if err != nil || p.Revision.Schema == 0 {
		t.Fatalf("%v %+v", err, p)
	}
	rec, err := rt.Apply(ctx, envelope("fine-tune-stop", "op2", "res1", `{"saveCheckpoint":true}`), p.Revision)
	if err != nil || rec.State != contract.ReceiptCommitted {
		t.Fatalf("%v %+v", err, rec)
	}
	c := eng.last("finetune.stop")
	if c.args["jobId"] != "job-1" || c.args["saveCheckpoint"] != true {
		t.Fatalf("stop args %v", c.args)
	}
	if rec.Revision.Data <= p.Revision.Data {
		t.Fatalf("the revision must advance: %+v then %+v", p.Revision, rec.Revision)
	}
	// A repeat of the same operation is answered from the receipt.
	if _, err := rt.Apply(ctx, envelope("fine-tune-stop", "op2", "res1", `{"saveCheckpoint":true}`), p.Revision); err != nil || eng.count("finetune.stop") != 1 {
		t.Fatalf("stop ran %d times (%v)", eng.count("finetune.stop"), err)
	}
	// Export names the format and the model, and never a path.
	ex, err := rt.Apply(ctx, envelope("fine-tune-export", "op3", "res1", `{"exportFormat":"gguf","name":"my-model"}`), rec.Revision)
	if err != nil || ex.State != contract.ReceiptCommitted || !strings.Contains(string(ex.Result.Payload), "my-model") {
		t.Fatalf("%v %+v", err, ex)
	}
	if a := eng.last("finetune.export").args; a["jobId"] != "job-1" || a["exportFormat"] != "gguf" || a["name"] != "my-model" {
		t.Fatalf("export args %v", a)
	}
}

func TestAStaleRevisionIsRejectedAndTheEngineIsNotCalled(t *testing.T) {
	rt, eng := started(t)
	rec, err := rt.Apply(ctx, envelope("fine-tune-stop", "op2", "res1", `{}`), contract.Revision{Schema: 1, Data: 99})
	if err != nil || rec.State != contract.ReceiptRejected {
		t.Fatalf("%v %+v", err, rec)
	}
	if eng.count("finetune.stop") != 0 {
		t.Fatal("the engine was called")
	}
}

func TestHostRefusalsMapToProtocolErrors(t *testing.T) {
	rt, eng := started(t)
	for code, want := range map[string]error{
		contract.HostFeature: protocol.ErrForbidden, contract.HostBadRequest: protocol.ErrBadRequest,
		contract.HostNotFound: protocol.ErrNotFound, contract.HostConflict: protocol.ErrConflict,
	} {
		eng.result["finetune.export"] = &contract.HostError{Code: code, Message: "m"}
		_, err := rt.Apply(ctx, envelope("fine-tune-export", "op-"+code, "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
		if !errors.Is(err, want) {
			t.Fatalf("%s: %v, want %v", code, err, want)
		}
	}
	eng.result["finetune.export"] = &contract.HostError{Code: contract.HostUnavailable, Message: "m"}
	_, err := rt.Apply(ctx, envelope("fine-tune-export", "op-u", "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
	if err == nil || errors.Is(err, protocol.ErrBadRequest) || errors.Is(err, protocol.ErrForbidden) || errors.Is(err, protocol.ErrNotFound) || errors.Is(err, protocol.ErrConflict) {
		t.Fatalf("an unavailable engine must stay uncertain, got %v", err)
	}
}

func exists(t *testing.T, rt contract.Runtime, res string) (bool, error) {
	t.Helper()
	r, err := rt.Read(ctx, envelope("resource-exists", "x-"+res, res, ""))
	if err != nil {
		return false, err
	}
	var v struct {
		Exists bool `json:"exists"`
	}
	_ = json.Unmarshal(r.Payload, &v)
	return v.Exists, nil
}

func TestResourceExistsAnswersFromTheJournalAndTheEngine(t *testing.T) {
	rt, eng := started(t)
	eng.result["finetune.get"] = map[string]any{"id": "job-1", "status": "training"}
	if ok, err := exists(t, rt, "res1"); err != nil || !ok {
		t.Fatalf("mapped: %v %v", ok, err)
	}
	if ok, err := exists(t, rt, "other"); err != nil || ok {
		t.Fatalf("unmapped: %v %v", ok, err)
	}
	// The job was deleted in the UI: the engine says not found.
	eng.result["finetune.get"] = &contract.HostError{Code: contract.HostNotFound, Message: "gone"}
	if ok, err := exists(t, rt, "res1"); err != nil || ok {
		t.Fatalf("deleted job: %v %v", ok, err)
	}
	// The engine cannot answer: unknown, never false.
	eng.result["finetune.get"] = &contract.HostError{Code: contract.HostUnavailable, Message: "down"}
	if _, err := exists(t, rt, "res1"); err == nil {
		t.Fatal("an unavailable engine must not read as absent")
	}
	eng.result["finetune.get"] = hostclient.ErrNoHostOps
	if _, err := exists(t, rt, "res1"); err == nil {
		t.Fatal("no host endpoint must not read as absent")
	}
}

func TestJournalSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}, "finetune.get": map[string]any{"status": "training"}}}
	if _, err := New(eng, dir).Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	again := New(eng, dir)
	if _, err := again.Read(ctx, envelope("fine-tune-status", "r1", "res1", "")); err != nil {
		t.Fatal(err)
	}
	if eng.last("finetune.get").args["jobId"] != "job-1" {
		t.Fatal("the restarted runtime lost the job")
	}
	// The receipt of the first run answers a repeat after the restart.
	if _, err := again.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1}); err != nil || eng.count("finetune.start") != 1 {
		t.Fatalf("started %d times (%v)", eng.count("finetune.start"), err)
	}
	if r, err := again.Receipt(ctx, "op1"); err != nil || r.State != contract.ReceiptCommitted {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := again.Receipt(ctx, "never"); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("an unknown operation must be not found, got %v", err)
	}
	// The journal is a plain file written through a temporary one.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("a temporary file was left: %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "journal.json")); err != nil {
		t.Fatal(err)
	}
}

func TestWithoutAHostEndpointEveryToolRefuses(t *testing.T) {
	var nilClient *hostclient.Client
	dir := t.TempDir()
	rt := New(nilClient, dir)
	if _, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1}); !errors.Is(err, hostclient.ErrNoHostOps) {
		t.Fatalf("start: %v", err)
	}
	// Seed a job as an earlier run with an endpoint would have.
	seed := New(&fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1"}}}, dir)
	if _, err := seed.Apply(ctx, envelope("fine-tune-start", "op0", "res1", startJSON), contract.Revision{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	rt = New(nilClient, dir)
	for _, tool := range []string{"fine-tune-status", "fine-tune-checkpoints", "resource-exists"} {
		if _, err := rt.Read(ctx, envelope(tool, "r-"+tool, "res1", "")); !errors.Is(err, hostclient.ErrNoHostOps) {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	for _, tool := range []string{"fine-tune-stop", "fine-tune-export"} {
		if _, err := rt.Apply(ctx, envelope(tool, "a-"+tool, "res1", `{}`), contract.Revision{Schema: 1, Data: 1}); !errors.Is(err, hostclient.ErrNoHostOps) {
			t.Fatalf("%s: %v", tool, err)
		}
	}
}

func TestAnUnknownToolIsABadRequest(t *testing.T) {
	rt := New(&fakeEngine{}, t.TempDir())
	if _, err := rt.Read(ctx, envelope("fine-tune-nope", "r1", "res1", "")); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("read: %v", err)
	}
	if _, err := rt.Apply(ctx, envelope("fine-tune-nope", "a1", "res1", ""), contract.Revision{Schema: 1}); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("apply: %v", err)
	}
	if _, err := rt.OpenOutput(ctx, envelope("fine-tune-status", "o1", "res1", ""), "h"); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("output: %v", err)
	}
}

func loadManifest(t *testing.T) contract.Manifest {
	t.Helper()
	b, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := contract.ParseManifest(b)
	if err != nil {
		t.Fatalf("the manifest must satisfy the contract: %v", err)
	}
	return m
}

func TestManifestDeclaresTheToolsAndTheResourceMapping(t *testing.T) {
	m := loadManifest(t)
	if m.ID != "fine-tuning" || m.Capabilities.UI.MainPage != "jobs" || !reflect.DeepEqual(m.Capabilities.UI.Pages, []string{"jobs"}) {
		t.Fatalf("manifest %+v", m)
	}
	if m.Capabilities.UI.SDK.Min != 2 || m.Capabilities.UI.SDK.Max != 2 {
		t.Fatalf("sdk %+v", m.Capabilities.UI.SDK)
	}
	if !reflect.DeepEqual(m.Capabilities.Tools, Tools) {
		t.Fatalf("manifest tools %v, package tools %v", m.Capabilities.Tools, Tools)
	}
	r := m.Capabilities.Resources
	if r == nil || r.Noun != "fine-tuning job" || r.Create != "fine-tune-start" || r.Inspect != "fine-tune-status" {
		t.Fatalf("resources %+v", r)
	}
	kinds := map[string]string{}
	for _, d := range m.Capabilities.ToolDefinitions {
		kinds[d.Name] = d.Kind
	}
	want := map[string]string{"fine-tune-list": "read", "fine-tune-status": "read", "fine-tune-checkpoints": "read", "fine-tune-start": "create", "fine-tune-dataset": "create", "fine-tune-stop": "write", "fine-tune-export": "write"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds %v", kinds)
	}
	for _, d := range m.Capabilities.ToolDefinitions {
		if d.Name == "fine-tune-list" && !contract.IsResourceless(d) {
			t.Fatal("the list tool must declare no resource")
		}
		if d.Name != "fine-tune-list" && contract.IsResourceless(d) {
			t.Fatalf("%s must not be resource-less", d.Name)
		}
		if len(d.Description) >= 400 || !strings.Contains(d.Description, "started") {
			t.Fatalf("%s: the description must be short and say that only jobs a tool started can be controlled", d.Name)
		}
	}
}

func TestNoToolArgumentNamesAUserOrAPath(t *testing.T) {
	m := loadManifest(t)
	for _, d := range m.Capabilities.ToolDefinitions {
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(d.InputSchema, &s); err != nil {
			t.Fatal(err)
		}
		for name := range s.Properties {
			lower := strings.ToLower(name)
			if contract.ForbiddenToolProperty(name) || strings.Contains(lower, "path") || strings.Contains(lower, "dir") {
				t.Fatalf("%s: property %q names an identity or a path", d.Name, name)
			}
		}
	}
}

func TestStartSchemaHasTheTask20FieldsAndTheNameTheHostNeeds(t *testing.T) {
	m := loadManifest(t)
	for _, d := range m.Capabilities.ToolDefinitions {
		if d.Name != "fine-tune-start" {
			continue
		}
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		_ = json.Unmarshal(d.InputSchema, &s)
		var got []string
		for k := range s.Properties {
			got = append(got, k)
		}
		want := []string{"name", "model", "backend", "trainingType", "trainingMethod", "datasetSource", "datasetSplit", "learningRate", "numEpochs", "batchSize", "maxSteps", "adapterRank", "adapterAlpha"}
		if len(got) != len(want) {
			t.Fatalf("properties %v", got)
		}
		for _, w := range want {
			if _, ok := s.Properties[w]; !ok {
				t.Fatalf("missing %s", w)
			}
		}
		return
	}
	t.Fatal("no start definition")
}

func TestListReadsTheCallersJobsWithoutAResource(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"finetune.list": map[string]any{"jobs": []any{map[string]any{"id": "ui-job", "status": "completed"}}}}}
	rt := New(eng, t.TempDir())
	res, err := rt.Read(ctx, envelope("fine-tune-list", "r1", "", ""))
	if err != nil || !strings.Contains(string(res.Payload), "ui-job") {
		t.Fatalf("%v %s", err, res.Payload)
	}
	if got := eng.ops(); !reflect.DeepEqual(got, []string{"finetune.list"}) {
		t.Fatalf("ops %v", got)
	}
	if c := eng.last("finetune.list"); c.args != nil {
		t.Fatalf("the list takes no argument, got %v", c.args)
	}
	if _, err := rt.Read(ctx, envelope("fine-tune-list", "r2", "", `{"userId":"other"}`)); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("an argument must be refused, got %v", err)
	}
	var nilClient *hostclient.Client
	if _, err := New(nilClient, t.TempDir()).Read(ctx, envelope("fine-tune-list", "r3", "", "")); !errors.Is(err, hostclient.ErrNoHostOps) {
		t.Fatalf("no endpoint: %v", err)
	}
}

const datasetJSON = `{"name":"data.jsonl","content":"{\"text\":\"a\"}\n"}`

func TestDatasetIsSentOnceAndItsRefComesBackOnlyThroughTheResource(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"finetune.dataset": map[string]any{"datasetRef": "0000000a-data.jsonl"}}}
	rt := New(eng, t.TempDir())
	p, err := rt.Preview(ctx, envelope("fine-tune-dataset", "create-d1", "d1", datasetJSON))
	if err != nil || p.Revision.Schema == 0 || len(eng.ops()) != 0 {
		t.Fatalf("%v %+v %v", err, p, eng.ops())
	}
	if strings.Contains(string(p.Summary), "text") {
		t.Fatalf("the approval card must not carry the content: %s", p.Summary)
	}
	rec, err := rt.Apply(ctx, envelope("fine-tune-dataset", "create-d1", "d1", datasetJSON), p.Revision)
	if err != nil || rec.State != contract.ReceiptCommitted || !strings.Contains(string(rec.Result.Payload), "0000000a-data.jsonl") {
		t.Fatalf("%v %+v", err, rec)
	}
	if _, err := rt.Apply(ctx, envelope("fine-tune-dataset", "create-d1", "d1", datasetJSON), p.Revision); err != nil || eng.count("finetune.dataset") != 1 {
		t.Fatalf("sent %d times (%v)", eng.count("finetune.dataset"), err)
	}
	if a := eng.last("finetune.dataset").args; a["name"] != "data.jsonl" || a["content"] == nil || len(a) != 2 {
		t.Fatalf("host operation args %v", a)
	}
	// The resource is the dataset: its status gives the reference to the owner of the resource.
	st, err := rt.Read(ctx, envelope("fine-tune-status", "r1", "d1", ""))
	if err != nil || !strings.Contains(string(st.Payload), `"datasetRef":"0000000a-data.jsonl"`) || !strings.Contains(string(st.Payload), `"kind":"dataset"`) {
		t.Fatalf("%v %s", err, st.Payload)
	}
	if n := len(eng.ops()); n != 1 {
		t.Fatalf("the status of a dataset must not call the engine: %v", eng.ops())
	}
	if ok, err := exists(t, rt, "d1"); err != nil || !ok {
		t.Fatalf("exists %v %v", ok, err)
	}
	// A dataset is not a job.
	for _, tool := range []string{"fine-tune-checkpoints"} {
		if _, err := rt.Read(ctx, envelope(tool, "r2", "d1", "")); !errors.Is(err, protocol.ErrNotFound) {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	if _, err := rt.Apply(ctx, envelope("fine-tune-stop", "s1", "d1", `{}`), contract.Revision{Schema: 1, Data: 1}); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("stop: %v", err)
	}
}

func TestDatasetRefusesWhatTheHostWouldRefuse(t *testing.T) {
	rt := New(&fakeEngine{}, t.TempDir())
	big := strings.Repeat("a", 900<<10+1)
	for name, args := range map[string]string{
		"empty":         `{"name":"d.jsonl","content":""}`,
		"too large":     `{"name":"d.jsonl","content":"` + big + `"}`,
		"a folder":      `{"name":"a/d.jsonl","content":"x"}`,
		"parent":        `{"name":"../d.jsonl","content":"x"}`,
		"wrong type":    `{"name":"d.exe","content":"x"}`,
		"only the type": `{"name":".jsonl","content":"x"}`,
		"extra field":   `{"name":"d.jsonl","content":"x","path":"/etc"}`,
	} {
		if _, err := rt.Preview(ctx, envelope("fine-tune-dataset", "o-"+name, "d9", args)); !errors.Is(err, protocol.ErrBadRequest) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// The largest allowed dataset passes.
	ok := `{"name":"d.jsonl","content":"` + strings.Repeat("a", 900<<10) + `"}`
	if _, err := rt.Preview(ctx, envelope("fine-tune-dataset", "o-ok", "d9", ok)); err != nil {
		t.Fatalf("900 KiB: %v", err)
	}
}

func TestStartPassesADatasetRefAndKeepsNoJobWhenTheHostRefusesIt(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, t.TempDir())
	args := `{"name":"run","model":"org/base","datasetSource":"0000000a-data.jsonl"}`
	if _, err := rt.Apply(ctx, envelope("fine-tune-start", "create-r1", "r1", args), contract.Revision{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	if eng.last("finetune.start").args["datasetSource"] != "0000000a-data.jsonl" {
		t.Fatalf("args %v", eng.last("finetune.start").args)
	}
	// The host refuses the reference (the per-user check is proven in the host tests); the add-on only maps the refusal and keeps nothing.
	eng.result["finetune.start"] = &contract.HostError{Code: contract.HostBadRequest, Message: "datasetSource is not a dataset reference of this user"}
	_, err := rt.Apply(ctx, envelope("fine-tune-start", "create-r2", "r2", `{"name":"run","model":"org/base","datasetSource":"0000000b-theirs.jsonl"}`), contract.Revision{Schema: 1})
	if !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("want a bad request, got %v", err)
	}
	if _, err := rt.Read(ctx, envelope("fine-tune-status", "r3", "r2", "")); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("a refused start must not leave a job: %v", err)
	}
	if _, err := rt.Receipt(ctx, "create-r2"); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("a refused start must not leave a receipt: %v", err)
	}
}
