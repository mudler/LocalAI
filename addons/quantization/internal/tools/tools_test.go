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
		Request:   contract.Request{AddonID: "quantization", ResourceID: resource, OperationID: opID, Generation: 1, Action: "apply", Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"}, Scope: "apply", ExpiresAt: time.Now().Add(time.Minute)},
	}
}

const startJSON = `{"name":"my quant","model":"org/base","quantizationType":"q4_k_m"}`

// started returns a runtime that already holds the job job-1 for resource res1.
func started(t *testing.T) (contract.Runtime, *fakeEngine) {
	t.Helper()
	eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, t.TempDir())
	rec, err := rt.Apply(ctx, envelope("quantize-start", "create-res1", "res1", startJSON), contract.Revision{Schema: 1})
	if err != nil || rec.State != contract.ReceiptCommitted {
		t.Fatalf("setup: %v %+v", err, rec)
	}
	return rt, eng
}

func TestStartCallsTheEngineOnceAndRemembersTheJob(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, t.TempDir())
	rec, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 0})
	if err != nil || rec.State != contract.ReceiptCommitted || rec.Revision.Schema == 0 {
		t.Fatalf("%v %+v", err, rec)
	}
	if got := eng.ops(); !reflect.DeepEqual(got, []string{"quantize.start"}) {
		t.Fatalf("ops %v", got)
	}
	again, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 1})
	if err != nil || again.State != contract.ReceiptCommitted {
		t.Fatalf("%v %+v", err, again)
	}
	if n := eng.count("quantize.start"); n != 1 {
		t.Fatalf("started %d jobs", n)
	}
	// A different operation on the same resource is a clash, not a second job.
	if _, err := rt.Apply(ctx, envelope("quantize-start", "op9", "res1", startJSON), contract.Revision{Schema: 1}); !errors.Is(err, protocol.ErrConflict) {
		t.Fatalf("want a conflict, got %v", err)
	}
}

func TestStartSendsOnlyTheFieldsOfTheEngineOperation(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, t.TempDir())
	args := `{"name":"my quant","model":"org/base","backend":"llama-cpp-quantization","quantizationType":"q5_k_m"}`
	if _, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", args), contract.Revision{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	c := eng.last("quantize.start")
	if c.opID != "op1" {
		t.Fatalf("the call must carry the operation of the request, got %q", c.opID)
	}
	if len(c.args) != 3 || c.args["model"] != "org/base" || c.args["backend"] != "llama-cpp-quantization" || c.args["quantizationType"] != "q5_k_m" {
		t.Fatalf("args %v", c.args)
	}
}

func TestPreviewOfStartHasANonzeroRevisionAndCreatesNothing(t *testing.T) {
	eng := &fakeEngine{}
	rt := New(eng, t.TempDir())
	p, err := rt.Preview(ctx, envelope("quantize-start", "op1", "res1", startJSON))
	if err != nil || p.Revision.Schema == 0 || len(eng.ops()) != 0 {
		t.Fatalf("%v %+v %v", err, p, eng.ops())
	}
	if !json.Valid(p.Summary) || !strings.Contains(string(p.Summary), "q4_k_m") {
		t.Fatalf("summary %s", p.Summary)
	}
	for name, args := range map[string]string{
		"empty model":  `{"name":"x","model":""}`,
		"bad type":     `{"name":"x","model":"m","quantizationType":"Q4 K"}`,
		"long name":    `{"name":"` + strings.Repeat("n", 81) + `","model":"m"}`,
		"a token":      `{"name":"x","model":"m","hfToken":"hf_x"}`,
		"unknown part": `{"name":"x","model":"m","path":"/etc"}`,
	} {
		if _, err := rt.Preview(ctx, envelope("quantize-start", "op-"+name, "res1", args)); !errors.Is(err, protocol.ErrBadRequest) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestStatusReadsTheJobOfTheResource(t *testing.T) {
	rt, eng := started(t)
	eng.result["quantize.get"] = map[string]any{"id": "job-1", "status": "quantizing", "model": "org/base"}
	res, err := rt.Read(ctx, envelope("quantize-status", "r1", "res1", ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := eng.last("quantize.get").args["jobId"]; got != "job-1" {
		t.Fatalf("jobId %v", got)
	}
	var v map[string]any
	if err := json.Unmarshal(res.Payload, &v); err != nil {
		t.Fatal(err)
	}
	if v["name"] != "my quant" || v["status"] != "quantizing" {
		t.Fatalf("payload %v", v)
	}
	rev, _ := v["revision"].(map[string]any)
	if rev["schema"] == nil || rev["schema"] == float64(0) {
		t.Fatalf("inspect needs a revision: %v", v)
	}
	if _, has := v["id"]; has {
		t.Fatalf("the engine job ID must not reach the model: %v", v)
	}
}

func TestAToolCannotControlAJobItDidNotStart(t *testing.T) {
	eng := &fakeEngine{}
	rt := New(eng, t.TempDir())
	if _, err := rt.Apply(ctx, envelope("quantize-stop", "op2", "nope", `{}`), contract.Revision{Schema: 1}); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("stop: %v", err)
	}
	if _, err := rt.Apply(ctx, envelope("quantize-import", "op3", "nope", `{}`), contract.Revision{Schema: 1}); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("import: %v", err)
	}
	if _, err := rt.Read(ctx, envelope("quantize-status", "r1", "nope", "")); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("status: %v", err)
	}
	if _, err := rt.Preview(ctx, envelope("quantize-stop", "op2", "nope", `{}`)); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("preview: %v", err)
	}
	if n := len(eng.ops()); n != 0 {
		t.Fatalf("the engine was called %d times: %v", n, eng.ops())
	}
}

func TestStopAndImportUseTheStoredJobAndAdvanceTheRevision(t *testing.T) {
	rt, eng := started(t)
	eng.result["quantize.import"] = map[string]any{"model": "my-model"}
	p, err := rt.Preview(ctx, envelope("quantize-stop", "op2", "res1", `{}`))
	if err != nil || p.Revision.Schema == 0 {
		t.Fatalf("%v %+v", err, p)
	}
	rec, err := rt.Apply(ctx, envelope("quantize-stop", "op2", "res1", `{}`), p.Revision)
	if err != nil || rec.State != contract.ReceiptCommitted {
		t.Fatalf("%v %+v", err, rec)
	}
	c := eng.last("quantize.stop")
	if len(c.args) != 1 || c.args["jobId"] != "job-1" {
		t.Fatalf("stop args %v (a quantization job has no checkpoint to save)", c.args)
	}
	if rec.Revision.Data <= p.Revision.Data {
		t.Fatalf("the revision must advance: %+v then %+v", p.Revision, rec.Revision)
	}
	if _, err := rt.Apply(ctx, envelope("quantize-stop", "op2", "res1", `{}`), p.Revision); err != nil || eng.count("quantize.stop") != 1 {
		t.Fatalf("stop ran %d times (%v)", eng.count("quantize.stop"), err)
	}
	// Import names the model and never a path.
	ex, err := rt.Apply(ctx, envelope("quantize-import", "op3", "res1", `{"name":"my-model"}`), rec.Revision)
	if err != nil || ex.State != contract.ReceiptCommitted || !strings.Contains(string(ex.Result.Payload), "my-model") {
		t.Fatalf("%v %+v", err, ex)
	}
	if a := eng.last("quantize.import").args; len(a) != 2 || a["jobId"] != "job-1" || a["name"] != "my-model" {
		t.Fatalf("import args %v", a)
	}
	if _, err := rt.Preview(ctx, envelope("quantize-import", "op4", "res1", `{"name":"../x"}`)); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("a bad model name must be refused, got %v", err)
	}
	if _, err := rt.Apply(ctx, envelope("quantize-import", "op5", "res1", `{"name":"a b"}`), ex.Revision); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("a bad model name must be refused at apply, got %v", err)
	}
}

func TestAStaleRevisionIsRejectedAndTheEngineIsNotCalled(t *testing.T) {
	rt, eng := started(t)
	rec, err := rt.Apply(ctx, envelope("quantize-stop", "op2", "res1", `{}`), contract.Revision{Schema: 1, Data: 99})
	if err != nil || rec.State != contract.ReceiptRejected {
		t.Fatalf("%v %+v", err, rec)
	}
	if eng.count("quantize.stop") != 0 {
		t.Fatal("the engine was called")
	}
}

func TestHostRefusalsMapToProtocolErrors(t *testing.T) {
	rt, eng := started(t)
	for code, want := range map[string]error{
		contract.HostFeature: protocol.ErrForbidden, contract.HostBadRequest: protocol.ErrBadRequest,
		contract.HostNotFound: protocol.ErrNotFound, contract.HostConflict: protocol.ErrConflict,
	} {
		eng.result["quantize.import"] = &contract.HostError{Code: code, Message: "m"}
		_, err := rt.Apply(ctx, envelope("quantize-import", "op-"+code, "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
		if !errors.Is(err, want) {
			t.Fatalf("%s: %v, want %v", code, err, want)
		}
	}
	eng.result["quantize.import"] = &contract.HostError{Code: contract.HostUnavailable, Message: "m"}
	_, err := rt.Apply(ctx, envelope("quantize-import", "op-u", "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
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
	eng.result["quantize.get"] = map[string]any{"id": "job-1", "status": "quantizing"}
	if ok, err := exists(t, rt, "res1"); err != nil || !ok {
		t.Fatalf("mapped: %v %v", ok, err)
	}
	if ok, err := exists(t, rt, "other"); err != nil || ok {
		t.Fatalf("unmapped: %v %v", ok, err)
	}
	eng.result["quantize.get"] = &contract.HostError{Code: contract.HostNotFound, Message: "gone"}
	if ok, err := exists(t, rt, "res1"); err != nil || ok {
		t.Fatalf("deleted job: %v %v", ok, err)
	}
	eng.result["quantize.get"] = &contract.HostError{Code: contract.HostUnavailable, Message: "down"}
	if _, err := exists(t, rt, "res1"); err == nil {
		t.Fatal("an unavailable engine must not read as absent")
	}
	eng.result["quantize.get"] = hostclient.ErrNoHostOps
	if _, err := exists(t, rt, "res1"); err == nil {
		t.Fatal("no host endpoint must not read as absent")
	}
}

func TestJournalSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}, "quantize.get": map[string]any{"status": "quantizing"}}}
	if _, err := New(eng, dir).Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	again := New(eng, dir)
	if _, err := again.Read(ctx, envelope("quantize-status", "r1", "res1", "")); err != nil {
		t.Fatal(err)
	}
	if eng.last("quantize.get").args["jobId"] != "job-1" {
		t.Fatal("the restarted runtime lost the job")
	}
	if _, err := again.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1}); err != nil || eng.count("quantize.start") != 1 {
		t.Fatalf("started %d times (%v)", eng.count("quantize.start"), err)
	}
	if r, err := again.Receipt(ctx, "op1"); err != nil || r.State != contract.ReceiptCommitted {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := again.Receipt(ctx, "never"); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("an unknown operation must be not found, got %v", err)
	}
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
	if _, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1}); !errors.Is(err, hostclient.ErrNoHostOps) {
		t.Fatalf("start: %v", err)
	}
	seed := New(&fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1"}}}, dir)
	if _, err := seed.Apply(ctx, envelope("quantize-start", "op0", "res1", startJSON), contract.Revision{Schema: 1}); err != nil {
		t.Fatal(err)
	}
	rt = New(nilClient, dir)
	for _, tool := range []string{"quantize-status", "resource-exists"} {
		if _, err := rt.Read(ctx, envelope(tool, "r-"+tool, "res1", "")); !errors.Is(err, hostclient.ErrNoHostOps) {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	if _, err := rt.Read(ctx, envelope("quantize-list", "r-list", "", "")); !errors.Is(err, hostclient.ErrNoHostOps) {
		t.Fatalf("list: %v", err)
	}
	for _, tool := range []string{"quantize-stop", "quantize-import"} {
		if _, err := rt.Apply(ctx, envelope(tool, "a-"+tool, "res1", `{}`), contract.Revision{Schema: 1, Data: 1}); !errors.Is(err, hostclient.ErrNoHostOps) {
			t.Fatalf("%s: %v", tool, err)
		}
	}
}

func TestAnUnknownToolIsABadRequest(t *testing.T) {
	rt := New(&fakeEngine{}, t.TempDir())
	if _, err := rt.Read(ctx, envelope("quantize-nope", "r1", "res1", "")); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("read: %v", err)
	}
	if _, err := rt.Apply(ctx, envelope("quantize-nope", "a1", "res1", ""), contract.Revision{Schema: 1}); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("apply: %v", err)
	}
	if _, err := rt.OpenOutput(ctx, envelope("quantize-status", "o1", "res1", ""), "h"); !errors.Is(err, protocol.ErrNotFound) {
		t.Fatalf("output: %v", err)
	}
}

func TestListReadsTheCallersJobsWithoutAResource(t *testing.T) {
	eng := &fakeEngine{result: map[string]any{"quantize.list": map[string]any{"jobs": []any{map[string]any{"id": "ui-job", "status": "completed"}}}}}
	rt := New(eng, t.TempDir())
	res, err := rt.Read(ctx, envelope("quantize-list", "r1", "", ""))
	if err != nil || !strings.Contains(string(res.Payload), "ui-job") {
		t.Fatalf("%v %s", err, res.Payload)
	}
	if got := eng.ops(); !reflect.DeepEqual(got, []string{"quantize.list"}) {
		t.Fatalf("ops %v", got)
	}
	if c := eng.last("quantize.list"); c.args != nil {
		t.Fatalf("the list takes no argument, got %v", c.args)
	}
	if _, err := rt.Read(ctx, envelope("quantize-list", "r2", "", `{"userId":"other"}`)); !errors.Is(err, protocol.ErrBadRequest) {
		t.Fatalf("an argument must be refused, got %v", err)
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
	if m.ID != "quantization" || m.Capabilities.UI.MainPage != "jobs" || !reflect.DeepEqual(m.Capabilities.UI.Pages, []string{"jobs"}) || m.Capabilities.UI.Title == nil || m.Capabilities.UI.Title.Text != "Quantization" {
		t.Fatalf("manifest %+v", m)
	}
	if m.Capabilities.UI.SDK.Min != 2 || m.Capabilities.UI.SDK.Max != 2 {
		t.Fatalf("sdk %+v", m.Capabilities.UI.SDK)
	}
	if !reflect.DeepEqual(m.Capabilities.Tools, Tools) {
		t.Fatalf("manifest tools %v, package tools %v", m.Capabilities.Tools, Tools)
	}
	r := m.Capabilities.Resources
	if r == nil || r.Noun != "quantization job" || r.Create != "quantize-start" || r.Inspect != "quantize-status" {
		t.Fatalf("resources %+v", r)
	}
	kinds := map[string]string{}
	for _, d := range m.Capabilities.ToolDefinitions {
		kinds[d.Name] = d.Kind
	}
	want := map[string]string{"quantize-list": "read", "quantize-status": "read", "quantize-start": "create", "quantize-stop": "write", "quantize-import": "write"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds %v", kinds)
	}
	for _, d := range m.Capabilities.ToolDefinitions {
		if d.Name == "quantize-list" && !contract.IsResourceless(d) {
			t.Fatal("the list tool must declare no resource")
		}
		if d.Name != "quantize-list" && contract.IsResourceless(d) {
			t.Fatalf("%s must not be resource-less", d.Name)
		}
		if len(d.Description) >= 400 || !strings.Contains(d.Description, "started") {
			t.Fatalf("%s: the description must be short and say that only jobs a tool started can be controlled", d.Name)
		}
	}
}

func TestNoToolArgumentNamesAUserAPathOrASecret(t *testing.T) {
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
			if contract.ForbiddenToolProperty(name) || strings.Contains(lower, "path") || strings.Contains(lower, "dir") || strings.Contains(lower, "token") {
				t.Fatalf("%s: property %q names an identity, a path or a secret", d.Name, name)
			}
		}
	}
}

func TestStartSchemaHasTheHostOperationFieldsAndTheNameTheHostNeeds(t *testing.T) {
	m := loadManifest(t)
	for _, d := range m.Capabilities.ToolDefinitions {
		if d.Name != "quantize-start" {
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
		if len(got) != 4 {
			t.Fatalf("properties %v", got)
		}
		for _, w := range []string{"name", "model", "backend", "quantizationType"} {
			if _, ok := s.Properties[w]; !ok {
				t.Fatalf("missing %s", w)
			}
		}
		if !reflect.DeepEqual(s.Required, []string{"name", "model"}) {
			t.Fatalf("required %v", s.Required)
		}
		return
	}
	t.Fatal("no start definition")
}
