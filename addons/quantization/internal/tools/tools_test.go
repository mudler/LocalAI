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
		Request:   contract.Request{AddonID: "quantization", ResourceID: resource, OperationID: opID, Generation: 1, Action: "apply", Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"}, Scope: "apply", ExpiresAt: time.Now().Add(time.Minute)},
	}
}

const startJSON = `{"name":"my quant","model":"org/base","quantizationType":"q4_k_m"}`

// started returns a runtime that already holds the job job-1 for resource res1.
func started() (contract.Runtime, *fakeEngine) {
	eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}}}
	rt := New(eng, GinkgoT().TempDir())
	rec, err := rt.Apply(ctx, envelope("quantize-start", "create-res1", "res1", startJSON), contract.Revision{Schema: 1})
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

var _ = Describe("quantization tools", func() {
	It("start calls the engine once and remembers the job", func() {
		eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}}}
		rt := New(eng, GinkgoT().TempDir())
		rec, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 0})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		Expect(rec.Revision.Schema).NotTo(BeZero())
		Expect(eng.ops()).To(Equal([]string{"quantize.start"}))
		again, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1, Data: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.State).To(Equal(contract.ReceiptCommitted))
		Expect(eng.count("quantize.start")).To(Equal(1))
		// A different operation on the same resource is a clash, not a second job.
		_, err = rt.Apply(ctx, envelope("quantize-start", "op9", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrConflict))
	})

	It("start sends only the fields of the engine operation", func() {
		eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}}}
		rt := New(eng, GinkgoT().TempDir())
		args := `{"name":"my quant","model":"org/base","backend":"llama-cpp-quantization","quantizationType":"q5_k_m"}`
		_, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", args), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		c := eng.last("quantize.start")
		Expect(c.opID).To(Equal("op1"), "the call must carry the operation of the request")
		Expect(c.args).To(HaveLen(3))
		Expect(c.args["model"]).To(Equal("org/base"))
		Expect(c.args["backend"]).To(Equal("llama-cpp-quantization"))
		Expect(c.args["quantizationType"]).To(Equal("q5_k_m"))
	})

	Describe("preview of start", func() {
		It("has a nonzero revision and creates nothing", func() {
			eng := &fakeEngine{}
			rt := New(eng, GinkgoT().TempDir())
			p, err := rt.Preview(ctx, envelope("quantize-start", "op1", "res1", startJSON))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Revision.Schema).NotTo(BeZero())
			Expect(eng.ops()).To(BeEmpty())
			Expect(json.Valid(p.Summary)).To(BeTrue(), "summary %s", p.Summary)
			Expect(string(p.Summary)).To(ContainSubstring("q4_k_m"))
		})

		for name, args := range map[string]string{
			"empty model":  `{"name":"x","model":""}`,
			"bad type":     `{"name":"x","model":"m","quantizationType":"Q4 K"}`,
			"long name":    `{"name":"` + strings.Repeat("n", 81) + `","model":"m"}`,
			"a token":      `{"name":"x","model":"m","hfToken":"hf_x"}`,
			"unknown part": `{"name":"x","model":"m","path":"/etc"}`,
		} {
			It("refuses "+name, func() {
				rt := New(&fakeEngine{}, GinkgoT().TempDir())
				_, err := rt.Preview(ctx, envelope("quantize-start", "op-"+name, "res1", args))
				Expect(err).To(MatchError(protocol.ErrBadRequest))
			})
		}
	})

	It("status reads the job of the resource", func() {
		rt, eng := started()
		eng.result["quantize.get"] = map[string]any{"id": "job-1", "status": "quantizing", "model": "org/base"}
		res, err := rt.Read(ctx, envelope("quantize-status", "r1", "res1", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.last("quantize.get").args["jobId"]).To(Equal("job-1"))
		var v map[string]any
		Expect(json.Unmarshal(res.Payload, &v)).To(Succeed())
		Expect(v["name"]).To(Equal("my quant"))
		Expect(v["status"]).To(Equal("quantizing"))
		rev, _ := v["revision"].(map[string]any)
		Expect(rev["schema"]).NotTo(BeNil(), "inspect needs a revision: %v", v)
		Expect(rev["schema"]).NotTo(Equal(float64(0)), "inspect needs a revision: %v", v)
		Expect(v).NotTo(HaveKey("id"), "the engine job ID must not reach the model")
	})

	It("does not let a tool control a job it did not start", func() {
		eng := &fakeEngine{}
		rt := New(eng, GinkgoT().TempDir())
		_, err := rt.Apply(ctx, envelope("quantize-stop", "op2", "nope", `{}`), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrNotFound), "stop")
		_, err = rt.Apply(ctx, envelope("quantize-import", "op3", "nope", `{}`), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrNotFound), "import")
		_, err = rt.Read(ctx, envelope("quantize-status", "r1", "nope", ""))
		Expect(err).To(MatchError(protocol.ErrNotFound), "status")
		_, err = rt.Preview(ctx, envelope("quantize-stop", "op2", "nope", `{}`))
		Expect(err).To(MatchError(protocol.ErrNotFound), "preview")
		Expect(eng.ops()).To(BeEmpty(), "the engine must not be called")
	})

	It("stop and import use the stored job and advance the revision", func() {
		rt, eng := started()
		eng.result["quantize.import"] = map[string]any{"model": "my-model"}
		p, err := rt.Preview(ctx, envelope("quantize-stop", "op2", "res1", `{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Revision.Schema).NotTo(BeZero())
		rec, err := rt.Apply(ctx, envelope("quantize-stop", "op2", "res1", `{}`), p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		c := eng.last("quantize.stop")
		Expect(c.args).To(HaveLen(1), "a quantization job has no checkpoint to save")
		Expect(c.args["jobId"]).To(Equal("job-1"))
		Expect(rec.Revision.Data).To(BeNumerically(">", p.Revision.Data), "the revision must advance")
		_, err = rt.Apply(ctx, envelope("quantize-stop", "op2", "res1", `{}`), p.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.count("quantize.stop")).To(Equal(1))
		// Import names the model and never a path.
		ex, err := rt.Apply(ctx, envelope("quantize-import", "op3", "res1", `{"name":"my-model"}`), rec.Revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(ex.State).To(Equal(contract.ReceiptCommitted))
		Expect(string(ex.Result.Payload)).To(ContainSubstring("my-model"))
		a := eng.last("quantize.import").args
		Expect(a).To(HaveLen(2))
		Expect(a["jobId"]).To(Equal("job-1"))
		Expect(a["name"]).To(Equal("my-model"))
		_, err = rt.Preview(ctx, envelope("quantize-import", "op4", "res1", `{"name":"../x"}`))
		Expect(err).To(MatchError(protocol.ErrBadRequest), "a bad model name must be refused")
		_, err = rt.Apply(ctx, envelope("quantize-import", "op5", "res1", `{"name":"a b"}`), ex.Revision)
		Expect(err).To(MatchError(protocol.ErrBadRequest), "a bad model name must be refused at apply")
	})

	It("rejects a stale revision and does not call the engine", func() {
		rt, eng := started()
		rec, err := rt.Apply(ctx, envelope("quantize-stop", "op2", "res1", `{}`), contract.Revision{Schema: 1, Data: 99})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptRejected))
		Expect(eng.count("quantize.stop")).To(BeZero(), "the engine was called")
	})

	Describe("host refusals", func() {
		for code, want := range map[string]error{
			contract.HostFeature: protocol.ErrForbidden, contract.HostBadRequest: protocol.ErrBadRequest,
			contract.HostNotFound: protocol.ErrNotFound, contract.HostConflict: protocol.ErrConflict,
		} {
			It("maps "+code+" to a protocol error", func() {
				rt, eng := started()
				eng.result["quantize.import"] = &contract.HostError{Code: code, Message: "m"}
				_, err := rt.Apply(ctx, envelope("quantize-import", "op-"+code, "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
				Expect(err).To(MatchError(want))
			})
		}

		It("keeps an unavailable engine uncertain", func() {
			rt, eng := started()
			eng.result["quantize.import"] = &contract.HostError{Code: contract.HostUnavailable, Message: "m"}
			_, err := rt.Apply(ctx, envelope("quantize-import", "op-u", "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
			Expect(err).To(HaveOccurred())
			for _, e := range []error{protocol.ErrBadRequest, protocol.ErrForbidden, protocol.ErrNotFound, protocol.ErrConflict} {
				Expect(err).NotTo(MatchError(e), "an unavailable engine must stay uncertain")
			}
		})
	})

	It("answers resource-exists from the journal and the engine", func() {
		rt, eng := started()
		eng.result["quantize.get"] = map[string]any{"id": "job-1", "status": "quantizing"}
		ok, err := exists(rt, "res1")
		Expect(err).NotTo(HaveOccurred(), "mapped")
		Expect(ok).To(BeTrue(), "mapped")
		ok, err = exists(rt, "other")
		Expect(err).NotTo(HaveOccurred(), "unmapped")
		Expect(ok).To(BeFalse(), "unmapped")
		eng.result["quantize.get"] = &contract.HostError{Code: contract.HostNotFound, Message: "gone"}
		ok, err = exists(rt, "res1")
		Expect(err).NotTo(HaveOccurred(), "deleted job")
		Expect(ok).To(BeFalse(), "deleted job")
		eng.result["quantize.get"] = &contract.HostError{Code: contract.HostUnavailable, Message: "down"}
		_, err = exists(rt, "res1")
		Expect(err).To(HaveOccurred(), "an unavailable engine must not read as absent")
		eng.result["quantize.get"] = hostclient.ErrNoHostOps
		_, err = exists(rt, "res1")
		Expect(err).To(HaveOccurred(), "no host endpoint must not read as absent")
	})

	It("keeps the journal across a restart", func() {
		dir := GinkgoT().TempDir()
		eng := &fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1", "status": "queued"}, "quantize.get": map[string]any{"status": "quantizing"}}}
		_, err := New(eng, dir).Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		again := New(eng, dir)
		_, err = again.Read(ctx, envelope("quantize-status", "r1", "res1", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.last("quantize.get").args["jobId"]).To(Equal("job-1"), "the restarted runtime lost the job")
		_, err = again.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(eng.count("quantize.start")).To(Equal(1))
		r, err := again.Receipt(ctx, "op1")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(contract.ReceiptCommitted))
		_, err = again.Receipt(ctx, "never")
		Expect(err).To(MatchError(protocol.ErrNotFound), "an unknown operation must be not found")
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
		_, err := rt.Apply(ctx, envelope("quantize-start", "op1", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(hostclient.ErrNoHostOps), "start")
		seed := New(&fakeEngine{result: map[string]any{"quantize.start": map[string]any{"id": "job-1"}}}, dir)
		_, err = seed.Apply(ctx, envelope("quantize-start", "op0", "res1", startJSON), contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		rt = New(nilClient, dir)
		for _, tool := range []string{"quantize-status", "resource-exists"} {
			_, err := rt.Read(ctx, envelope(tool, "r-"+tool, "res1", ""))
			Expect(err).To(MatchError(hostclient.ErrNoHostOps), tool)
		}
		_, err = rt.Read(ctx, envelope("quantize-list", "r-list", "", ""))
		Expect(err).To(MatchError(hostclient.ErrNoHostOps), "list")
		for _, tool := range []string{"quantize-stop", "quantize-import"} {
			_, err := rt.Apply(ctx, envelope(tool, "a-"+tool, "res1", `{}`), contract.Revision{Schema: 1, Data: 1})
			Expect(err).To(MatchError(hostclient.ErrNoHostOps), tool)
		}
	})

	It("treats an unknown tool as a bad request", func() {
		rt := New(&fakeEngine{}, GinkgoT().TempDir())
		_, err := rt.Read(ctx, envelope("quantize-nope", "r1", "res1", ""))
		Expect(err).To(MatchError(protocol.ErrBadRequest), "read")
		_, err = rt.Apply(ctx, envelope("quantize-nope", "a1", "res1", ""), contract.Revision{Schema: 1})
		Expect(err).To(MatchError(protocol.ErrBadRequest), "apply")
		_, err = rt.OpenOutput(ctx, envelope("quantize-status", "o1", "res1", ""), "h")
		Expect(err).To(MatchError(protocol.ErrNotFound), "output")
	})

	It("lists the caller's jobs without a resource", func() {
		eng := &fakeEngine{result: map[string]any{"quantize.list": map[string]any{"jobs": []any{map[string]any{"id": "ui-job", "status": "completed"}}}}}
		rt := New(eng, GinkgoT().TempDir())
		res, err := rt.Read(ctx, envelope("quantize-list", "r1", "", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.Payload)).To(ContainSubstring("ui-job"))
		Expect(eng.ops()).To(Equal([]string{"quantize.list"}))
		Expect(eng.last("quantize.list").args).To(BeNil(), "the list takes no argument")
		_, err = rt.Read(ctx, envelope("quantize-list", "r2", "", `{"userId":"other"}`))
		Expect(err).To(MatchError(protocol.ErrBadRequest), "an argument must be refused")
	})

	Describe("manifest", func() {
		It("declares the tools and the resource mapping", func() {
			m := loadManifest()
			Expect(m.ID).To(Equal("quantization"))
			Expect(m.Capabilities.UI.MainPage).To(Equal("jobs"))
			Expect(m.Capabilities.UI.Pages).To(Equal([]string{"jobs"}))
			Expect(m.Capabilities.UI.Title).NotTo(BeNil())
			Expect(m.Capabilities.UI.Title.Text).To(Equal("Quantization"))
			Expect(m.Capabilities.UI.SDK.Min).To(BeEquivalentTo(2))
			Expect(m.Capabilities.UI.SDK.Max).To(BeEquivalentTo(2))
			Expect(m.Capabilities.Tools).To(Equal(Tools))
			r := m.Capabilities.Resources
			Expect(r).NotTo(BeNil())
			Expect(r.Noun).To(Equal("quantization job"))
			Expect(r.Create).To(Equal("quantize-start"))
			Expect(r.Inspect).To(Equal("quantize-status"))
			kinds := map[string]string{}
			for _, d := range m.Capabilities.ToolDefinitions {
				kinds[d.Name] = d.Kind
			}
			want := map[string]string{"quantize-list": "read", "quantize-status": "read", "quantize-start": "create", "quantize-stop": "write", "quantize-import": "write"}
			Expect(kinds).To(Equal(want))
			for _, d := range m.Capabilities.ToolDefinitions {
				if d.Name == "quantize-list" {
					Expect(contract.IsResourceless(d)).To(BeTrue(), "the list tool must declare no resource")
				} else {
					Expect(contract.IsResourceless(d)).To(BeFalse(), "%s must not be resource-less", d.Name)
				}
				Expect(len(d.Description)).To(BeNumerically("<", 400), d.Name)
				Expect(d.Description).To(ContainSubstring("started"), "%s: the description must say that only jobs a tool started can be controlled", d.Name)
			}
		})

		It("names no user, path or secret in any tool argument", func() {
			m := loadManifest()
			for _, d := range m.Capabilities.ToolDefinitions {
				var s struct {
					Properties map[string]json.RawMessage `json:"properties"`
				}
				Expect(json.Unmarshal(d.InputSchema, &s)).To(Succeed())
				for name := range s.Properties {
					lower := strings.ToLower(name)
					Expect(contract.ForbiddenToolProperty(name) || strings.Contains(lower, "path") || strings.Contains(lower, "dir") || strings.Contains(lower, "token")).To(BeFalse(),
						"%s: property %q names an identity, a path or a secret", d.Name, name)
				}
			}
		})

		It("gives the start schema the host operation fields and the name the host needs", func() {
			m := loadManifest()
			found := false
			for _, d := range m.Capabilities.ToolDefinitions {
				if d.Name != "quantize-start" {
					continue
				}
				found = true
				var s struct {
					Properties map[string]json.RawMessage `json:"properties"`
					Required   []string                   `json:"required"`
				}
				_ = json.Unmarshal(d.InputSchema, &s)
				Expect(s.Properties).To(HaveLen(4))
				for _, w := range []string{"name", "model", "backend", "quantizationType"} {
					Expect(s.Properties).To(HaveKey(w))
				}
				Expect(s.Required).To(Equal([]string{"name", "model"}))
				break
			}
			Expect(found).To(BeTrue(), "no start definition")
		})
	})
})
