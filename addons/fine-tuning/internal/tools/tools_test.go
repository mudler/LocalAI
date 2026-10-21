// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"time"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
func started() (contract.Runtime, *fakeEngine) {
	eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, GinkgoT().TempDir())
	rec, err := rt.Apply(ctx, envelope("fine-tune-start", "create-res1", "res1", startJSON), contract.Revision{Schema: 1})
	Expect(err).NotTo(HaveOccurred(), "setup")
	Expect(rec.State).To(Equal(contract.ReceiptCommitted), "setup: %+v", rec)
	return rt, eng
}

func exists(rt contract.Runtime, res string) (bool, error) {
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

func loadManifest() contract.Manifest {
	b, err := os.ReadFile("../../manifest.json")
	Expect(err).NotTo(HaveOccurred())
	m, err := contract.ParseManifest(b)
	Expect(err).NotTo(HaveOccurred(), "the manifest must satisfy the contract")
	return m
}

const datasetJSON = `{"name":"data.jsonl","content":"{\"text\":\"a\"}\n"}`

var _ = Describe("fine-tuning tools", func() {
	It("start calls the engine once and remembers the job", func() {
		eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
		rt := New(eng, GinkgoT().TempDir())
		rec, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 0})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		Expect(rec.Revision.Schema).NotTo(BeZero())
		Expect(eng.ops()).To(Equal([]string{"finetune.start"}))
		// The same operation again does not start a second job.
		again, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(contract.ReceiptCommitted))
		Expect(eng.count("finetune.start")).To(Equal(1))
	})

	It("start sends only the fields of the engine operation", func() {
		eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
		rt := New(eng, GinkgoT().TempDir())
		args := `{"name":"my run","model":"org/base","datasetSource":"org/data","numEpochs":2,"learningRate":0.0002,"adapterRank":8}`
		_, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", args), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		c := eng.last("finetune.start")
		Expect(c.opID).To(Equal("op1"), "the call must carry the operation of the request")
		for _, banned := range []string{"tool", "name", "resourceId"} {
			Expect(c.args).NotTo(HaveKey(banned), "the engine must not receive %q", banned)
		}
		Expect(c.args["model"]).To(Equal("org/base"))
		Expect(c.args["numEpochs"]).To(Equal(float64(2)))
		Expect(c.args["adapterRank"]).To(Equal(float64(8)))
	})

	It("previews a start with a nonzero revision and creates nothing", func() {
		eng := &fakeEngine{}
		rt := New(eng, GinkgoT().TempDir())
		p, err := rt.Preview(ctx, envelope("fine-tune-start", "op1", "res1", startJSON))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Revision.Schema).NotTo(BeZero())
		Expect(eng.ops()).To(BeEmpty())
		Expect(json.Valid(p.Summary)).To(BeTrue(), "summary %s", p.Summary)
		_, err = rt.Preview(ctx, envelope("fine-tune-start", "op1", "res1", `{"name":"x","model":"","datasetSource":"d"}`))
		Expect(err).To(MatchError(protocol.ErrBadRequest), "an empty model must be refused")
	})

	It("status reads the job of the resource", func() {
		rt, eng := started()
		eng.result["finetune.get"] = map[string]any{"id": "job-1", "status": "training", "model": "org/base"}
		res, err := rt.Read(ctx, envelope("fine-tune-status", "r1", "res1", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.last("finetune.get").args["jobId"]).To(Equal("job-1"))
		var v map[string]any
		Expect(json.Unmarshal(res.Payload, &v)).To(Succeed())
		Expect(v["name"]).To(Equal("my run"))
		Expect(v["status"]).To(Equal("training"))
		rev, _ := v["revision"].(map[string]any)
		Expect(rev["schema"]).NotTo(BeNil(), "inspect needs a revision: %v", v)
		Expect(rev["schema"]).NotTo(Equal(float64(0)), "inspect needs a revision: %v", v)
		Expect(v).NotTo(HaveKey("id"), "the engine job ID must not reach the model")
		Expect(v).NotTo(HaveKey("progressPercent"), "a snapshot percent is unreliable and must not be reported")
	})

	It("checkpoints read the job of the resource", func() {
		rt, eng := started()
		eng.result["finetune.checkpoints"] = map[string]any{"checkpoints": []any{map[string]any{"step": 10}}}
		res, err := rt.Read(ctx, envelope("fine-tune-checkpoints", "r1", "res1", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.Payload)).To(ContainSubstring(`"step":10`))
		Expect(eng.last("finetune.checkpoints").args["jobId"]).To(Equal("job-1"), "wrong job")
	})

	It("does not let a tool control a job it did not start", func() {
		eng := &fakeEngine{}
		rt := New(eng, GinkgoT().TempDir())
		_, err := rt.Apply(ctx, envelope("fine-tune-stop", "op2", "nope", `{}`), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrNotFound), "stop")
		_, err = rt.Apply(ctx, envelope("fine-tune-export", "op3", "nope", `{}`), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrNotFound), "export")
		_, err = rt.Read(ctx, envelope("fine-tune-status", "r1", "nope", ""))
		Expect(err).To(MatchError(protocol.ErrNotFound), "status")
		_, err = rt.Preview(ctx, envelope("fine-tune-stop", "op2", "nope", `{}`))
		Expect(err).To(MatchError(protocol.ErrNotFound), "preview")
		Expect(eng.ops()).To(BeEmpty(), "the engine must not be called")
	})

	It("stop and export use the stored job and advance the revision", func() {
		rt, eng := started()
		eng.result["finetune.stop"] = map[string]any{}
		eng.result["finetune.export"] = map[string]any{"model": "my-model"}
		p, err := rt.Preview(ctx, envelope("fine-tune-stop", "op2", "res1", `{"saveCheckpoint":true}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Revision.Schema).NotTo(BeZero())
		rec, err := rt.Apply(ctx, envelope("fine-tune-stop", "op2", "res1", `{"saveCheckpoint":true}`), p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		c := eng.last("finetune.stop")
		Expect(c.args["jobId"]).To(Equal("job-1"))
		Expect(c.args["saveCheckpoint"]).To(Equal(true))
		Expect(rec.Revision.Data).To(BeNumerically(">", p.Revision.Data), "the revision must advance")
		// A repeat of the same operation is answered from the receipt.
		_, err = rt.Apply(ctx, envelope("fine-tune-stop", "op2", "res1", `{"saveCheckpoint":true}`), p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.count("finetune.stop")).To(Equal(1))
		// Export names the format and the model, and never a path.
		ex, err := rt.Apply(ctx, envelope("fine-tune-export", "op3", "res1", `{"exportFormat":"gguf","name":"my-model"}`), rec.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(ex.State).To(Equal(contract.ReceiptCommitted))
		Expect(string(ex.Result.Payload)).To(ContainSubstring("my-model"))
		a := eng.last("finetune.export").args
		Expect(a["jobId"]).To(Equal("job-1"))
		Expect(a["exportFormat"]).To(Equal("gguf"))
		Expect(a["name"]).To(Equal("my-model"))
	})

	It("rejects a stale revision and does not call the engine", func() {
		rt, eng := started()
		rec, err := rt.Apply(ctx, envelope("fine-tune-stop", "op2", "res1", `{}`), contract.Revision{Schema: 1, Data: 99})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptRejected))
		Expect(eng.count("finetune.stop")).To(BeZero(), "the engine was called")
	})

	Describe("host refusals", func() {
		for code, want := range map[string]error{
			contract.HostFeature: protocol.ErrForbidden, contract.HostBadRequest: protocol.ErrBadRequest,
			contract.HostNotFound: protocol.ErrNotFound, contract.HostConflict: protocol.ErrConflict,
		} {
			It("maps "+code+" to a protocol error", func() {
				rt, eng := started()
				eng.result["finetune.export"] = &contract.HostError{Code: code, Message: "m"}
				_, err := rt.Apply(ctx, envelope("fine-tune-export", "op-"+code, "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
				Expect(err).To(MatchError(want))
			})
		}

		It("keeps an unavailable engine uncertain", func() {
			rt, eng := started()
			eng.result["finetune.export"] = &contract.HostError{Code: contract.HostUnavailable, Message: "m"}
			_, err := rt.Apply(ctx, envelope("fine-tune-export", "op-u", "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
			Expect(err).To(HaveOccurred())
			for _, e := range []error{protocol.ErrBadRequest, protocol.ErrForbidden, protocol.ErrNotFound, protocol.ErrConflict} {
				Expect(err).NotTo(MatchError(e), "an unavailable engine must stay uncertain")
			}
		})
	})

	It("answers resource-exists from the journal and the engine", func() {
		rt, eng := started()
		eng.result["finetune.get"] = map[string]any{"id": "job-1", "status": "training"}
		ok, err := exists(rt, "res1")
		Expect(err).NotTo(HaveOccurred(), "mapped")
		Expect(ok).To(BeTrue(), "mapped")
		ok, err = exists(rt, "other")
		Expect(err).NotTo(HaveOccurred(), "unmapped")
		Expect(ok).To(BeFalse(), "unmapped")
		// The job was deleted in the UI: the engine says not found.
		eng.result["finetune.get"] = &contract.HostError{Code: contract.HostNotFound, Message: "gone"}
		ok, err = exists(rt, "res1")
		Expect(err).NotTo(HaveOccurred(), "deleted job")
		Expect(ok).To(BeFalse(), "deleted job")
		// The engine cannot answer: unknown, never false.
		eng.result["finetune.get"] = &contract.HostError{Code: contract.HostUnavailable, Message: "down"}
		_, err = exists(rt, "res1")
		Expect(err).To(HaveOccurred(), "an unavailable engine must not read as absent")
		eng.result["finetune.get"] = hostclient.ErrNoHostOps
		_, err = exists(rt, "res1")
		Expect(err).To(HaveOccurred(), "no host endpoint must not read as absent")
	})

	It("keeps the journal across a restart", func() {
		dir := GinkgoT().TempDir()
		eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}, "finetune.get": map[string]any{"status": "training"}}}
		_, err := New(eng, dir).Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		again := New(eng, dir)
		_, err = again.Read(ctx, envelope("fine-tune-status", "r1", "res1", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.last("finetune.get").args["jobId"]).To(Equal("job-1"), "the restarted runtime lost the job")
		// The receipt of the first run answers a repeat after the restart.
		_, err = again.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.count("finetune.start")).To(Equal(1))
		r, err := again.Receipt(ctx, "op1")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(contract.ReceiptCommitted))
		_, err = again.Receipt(ctx, "never")
		Expect(err).To(MatchError(protocol.ErrNotFound), "an unknown operation must be not found")
		// The journal is a plain file written through a temporary one.
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			Expect(e.Name()).NotTo(ContainSubstring(".tmp"), "a temporary file was left")
		}
		_, err = os.Stat(filepath.Join(dir, "journal.json"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses every tool without a host endpoint", func() {
		var nilClient *hostclient.Client
		dir := GinkgoT().TempDir()
		rt := New(nilClient, dir)
		_, err := rt.Apply(ctx, envelope("fine-tune-start", "op1", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(hostclient.ErrNoHostOps), "start")
		// Seed a job as an earlier run with an endpoint would have.
		seed := New(&fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1"}}}, dir)
		_, err = seed.Apply(ctx, envelope("fine-tune-start", "op0", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		rt = New(nilClient, dir)
		for _, tool := range []string{"fine-tune-status", "fine-tune-checkpoints", "resource-exists"} {
			_, err := rt.Read(ctx, envelope(tool, "r-"+tool, "res1", ""))
			Expect(err).To(MatchError(hostclient.ErrNoHostOps), tool)
		}
		for _, tool := range []string{"fine-tune-stop", "fine-tune-export"} {
			_, err := rt.Apply(ctx, envelope(tool, "a-"+tool, "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
			Expect(err).To(MatchError(hostclient.ErrNoHostOps), tool)
		}
	})

	It("treats an unknown tool as a bad request", func() {
		rt := New(&fakeEngine{}, GinkgoT().TempDir())
		_, err := rt.Read(ctx, envelope("fine-tune-nope", "r1", "res1", ""))
		Expect(err).To(MatchError(protocol.ErrBadRequest), "read")
		_, err = rt.Apply(ctx, envelope("fine-tune-nope", "a1", "res1", ""), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrBadRequest), "apply")
		_, err = rt.OpenOutput(ctx, envelope("fine-tune-status", "o1", "res1", ""), "h")
		Expect(err).To(MatchError(protocol.ErrNotFound), "output")
	})

	Describe("manifest", func() {
		It("declares the tools and the resource mapping", func() {
			m := loadManifest()
			Expect(m.ID).To(Equal("fine-tuning"))
			Expect(m.Capabilities.UI.MainPage).To(Equal("jobs"))
			Expect(m.Capabilities.UI.Pages).To(Equal([]string{"jobs"}))
			Expect(m.Capabilities.UI.SDK.Min).To(BeEquivalentTo(2))
			Expect(m.Capabilities.UI.SDK.Max).To(BeEquivalentTo(2))
			Expect(m.Capabilities.Tools).To(Equal(Tools))
			r := m.Capabilities.Resources
			Expect(r).NotTo(BeNil())
			Expect(r.Noun).To(Equal("fine-tuning job"))
			Expect(r.Create).To(Equal("fine-tune-start"))
			Expect(r.Inspect).To(Equal("fine-tune-status"))
			kinds := map[string]string{}
			for _, d := range m.Capabilities.ToolDefinitions {
				kinds[d.Name] = d.Kind
			}
			want := map[string]string{"fine-tune-list": "read", "fine-tune-status": "read", "fine-tune-checkpoints": "read", "fine-tune-start": "create", "fine-tune-dataset": "create", "fine-tune-stop": "write", "fine-tune-export": "write"}
			Expect(kinds).To(Equal(want))
			for _, d := range m.Capabilities.ToolDefinitions {
				if d.Name == "fine-tune-list" {
					Expect(contract.IsResourceless(d)).To(BeTrue(), "the list tool must declare no resource")
				} else {
					Expect(contract.IsResourceless(d)).To(BeFalse(), "%s must not be resource-less", d.Name)
				}
				Expect(len(d.Description)).To(BeNumerically("<", 400), d.Name)
				Expect(d.Description).To(ContainSubstring("started"), "%s: the description must say that only jobs a tool started can be controlled", d.Name)
			}
		})

		It("names no user or path in any tool argument", func() {
			m := loadManifest()
			for _, d := range m.Capabilities.ToolDefinitions {
				var s struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}
				Expect(json.Unmarshal(d.InputSchema, &s)).To(Succeed())
				for name := range s.Properties {
					lower := strings.ToLower(name)
					Expect(contract.ForbiddenToolProperty(name) || strings.Contains(lower, "path") || strings.Contains(lower, "dir")).To(BeFalse(),
						"%s: property %q names an identity or a path", d.Name, name)
				}
			}
		})

		It("gives the start schema the task 20 fields and the name the host needs", func() {
			m := loadManifest()
			found := false
			for _, d := range m.Capabilities.ToolDefinitions {
				if d.Name != "fine-tune-start" {
					continue
				}
				found = true
				var s struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				}
				_ = json.Unmarshal(d.InputSchema, &s)
				want := []string{"name", "model", "backend", "trainingType", "trainingMethod", "datasetSource", "datasetSplit", "learningRate", "numEpochs", "batchSize", "maxSteps", "adapterRank", "adapterAlpha"}
				Expect(s.Properties).To(HaveLen(len(want)))
				for _, w := range want {
					Expect(s.Properties).To(HaveKey(w))
				}
				break
			}
			Expect(found).To(BeTrue(), "no start definition")
		})
	})

	It("lists the caller's jobs without a resource", func() {
		eng := &fakeEngine{result: map[string]any{"finetune.list": map[string]any{"jobs": []any{map[string]any{"id": "ui-job", "status": "completed"}}}}}
		rt := New(eng, GinkgoT().TempDir())
		res, err := rt.Read(ctx, envelope("fine-tune-list", "r1", "", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.Payload)).To(ContainSubstring("ui-job"))
		Expect(eng.ops()).To(Equal([]string{"finetune.list"}))
		Expect(eng.last("finetune.list").args).To(BeNil(), "the list takes no argument")
		_, err = rt.Read(ctx, envelope("fine-tune-list", "r2", "", `{"userId":"other"}`))
		Expect(err).To(MatchError(protocol.ErrBadRequest), "an argument must be refused")
		var nilClient *hostclient.Client
		_, err = New(nilClient, GinkgoT().TempDir()).Read(ctx, envelope("fine-tune-list", "r3", "", ""))
		Expect(err).To(MatchError(hostclient.ErrNoHostOps), "no endpoint")
	})

	It("sends a dataset once and returns its ref only through the resource", func() {
		eng := &fakeEngine{result: map[string]any{"finetune.dataset": map[string]any{"datasetRef": "0000000a-data.jsonl"}}}
		rt := New(eng, GinkgoT().TempDir())
		p, err := rt.Preview(ctx, envelope("fine-tune-dataset", "create-d1", "d1", datasetJSON))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Revision.Schema).NotTo(BeZero())
		Expect(eng.ops()).To(BeEmpty())
		Expect(string(p.Summary)).NotTo(ContainSubstring("text"), "the approval card must not carry the content")
		rec, err := rt.Apply(ctx, envelope("fine-tune-dataset", "create-d1", "d1", datasetJSON), p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		Expect(string(rec.Result.Payload)).To(ContainSubstring("0000000a-data.jsonl"))
		_, err = rt.Apply(ctx, envelope("fine-tune-dataset", "create-d1", "d1", datasetJSON), p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.count("finetune.dataset")).To(Equal(1))
		a := eng.last("finetune.dataset").args
		Expect(a["name"]).To(Equal("data.jsonl"))
		Expect(a["content"]).NotTo(BeNil())
		Expect(a).To(HaveLen(2), "host operation args")
		// The resource is the dataset: its status gives the reference to the owner of the resource.
		st, err := rt.Read(ctx, envelope("fine-tune-status", "r1", "d1", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(st.Payload)).To(ContainSubstring(`"datasetRef":"0000000a-data.jsonl"`))
		Expect(string(st.Payload)).To(ContainSubstring(`"kind":"dataset"`))
		Expect(eng.ops()).To(HaveLen(1), "the status of a dataset must not call the engine")
		ok, err := exists(rt, "d1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		// A dataset is not a job.
		for _, tool := range []string{"fine-tune-checkpoints"} {
			_, err := rt.Read(ctx, envelope(tool, "r2", "d1", ""))
			Expect(err).To(MatchError(protocol.ErrNotFound), tool)
		}
		_, err = rt.Apply(ctx, envelope("fine-tune-stop", "s1", "d1", `{}`), contract.Revision{Schema: 1, Data: 1})
		Expect(err).To(MatchError(protocol.ErrNotFound), "stop")
	})

	Describe("dataset validation", func() {
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
			It("refuses "+name, func() {
				rt := New(&fakeEngine{}, GinkgoT().TempDir())
				_, err := rt.Preview(ctx, envelope("fine-tune-dataset", "o-"+name, "d9", args))
				Expect(err).To(MatchError(protocol.ErrBadRequest))
			})
		}

		It("accepts the largest allowed dataset", func() {
			rt := New(&fakeEngine{}, GinkgoT().TempDir())
			ok := `{"name":"d.jsonl","content":"` + strings.Repeat("a", 900<<10) + `"}`
			_, err := rt.Preview(ctx, envelope("fine-tune-dataset", "o-ok", "d9", ok))
			Expect(err).NotTo(HaveOccurred(), "900 KiB")
		})
	})

	It("passes a dataset ref to start and keeps no job when the host refuses it", func() {
		eng := &fakeEngine{result: map[string]any{"finetune.start": map[string]any{"id": "job-1", "status": "queued"}}}
		rt := New(eng, GinkgoT().TempDir())
		args := `{"name":"run","model":"org/base","datasetSource":"0000000a-data.jsonl"}`
		_, err := rt.Apply(ctx, envelope("fine-tune-start", "create-r1", "r1", args), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.last("finetune.start").args["datasetSource"]).To(Equal("0000000a-data.jsonl"))
		// The host refuses the reference (the per-user check is proven in the host tests); the add-on only maps the refusal and keeps nothing.
		eng.result["finetune.start"] = &contract.HostError{Code: contract.HostBadRequest, Message: "datasetSource is not a dataset reference of this user"}
		_, err = rt.Apply(ctx, envelope("fine-tune-start", "create-r2", "r2", `{"name":"run","model":"org/base","datasetSource":"0000000b-theirs.jsonl"}`), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrBadRequest))
		_, err = rt.Read(ctx, envelope("fine-tune-status", "r3", "r2", ""))
		Expect(err).To(MatchError(protocol.ErrNotFound), "a refused start must not leave a job")
		_, err = rt.Receipt(ctx, "create-r2")
		Expect(err).To(MatchError(protocol.ErrNotFound), "a refused start must not leave a receipt")
	})
})
