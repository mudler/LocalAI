// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func modelSpecs(ex Exporter, im Importer) map[string]Spec {
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	Expect(RegisterEngineModels(b, ex, im)).To(Succeed())
	return b.specs
}

// serveAs runs one operation through the channel handler as the given actor,
// with a live envelope for it, and returns the status and the answer.
func serveAs(b *Broker, op, mode, user, args string) (int, contract.HostResponse) {
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
func adminBroker(ex Exporter, im Importer) *Broker {
	b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
	b.SetAdminCheck(func(actor contract.Actor) bool { return actor.UserID == "admin" })
	Expect(RegisterEngineModels(b, ex, im)).To(Succeed())
	return b
}

var _ = Describe("Model export and import operations", func() {
	It("export registers a model and returns only its name", func() {
		ex := newExporter("/models/my-model")
		s := modelSpecs(ex, nil)
		out, err := call(s["finetune.export"], "named-user", "u1", `{"jobId":"j1","name":"my-model","exportFormat":"lora"}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal(`{"model":"my-model"}`))
		Expect(string(out)).ToNot(ContainSubstring("/"), "result carries a path")
		Expect(ex.user).To(Equal("u1"))
		Expect(ex.req.Name).To(Equal("my-model"))
		Expect(ex.req.ExportFormat).To(Equal("lora"))
	})

	It("export uses the highest step checkpoint", func() {
		ex := newExporter("/models/my-model")
		ex.cps = []Checkpoint{{Step: 100, Path: "/out/ckpt-100"}, {Step: 300, Path: "/out/ckpt-300"}, {Step: 200, Path: "/out/ckpt-200"}}
		s := modelSpecs(ex, nil)
		_, err := call(s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(ex.req.CheckpointPath).To(Equal("/out/ckpt-300"), "want the highest step")
	})

	It("export of a job with no checkpoint is a conflict naming it", func() {
		ex := newExporter("/models/my-model")
		ex.cps = nil
		s := modelSpecs(ex, nil)
		_, err := call(s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`)
		Expect(err).To(MatchError(ErrConflict))
		Expect(err.Error()).To(ContainSubstring("checkpoint"))
		Expect(ex.calls).To(Equal(0), "service called for a job with no checkpoint")
	})

	It("export refuses a bad name", func() {
		ex := newExporter("/models/x")
		s := modelSpecs(ex, nil)
		long := strings.Repeat("a", 65)
		for _, name := range []string{"../x", "a/b", "   ", long} {
			args, _ := json.Marshal(map[string]string{"jobId": "j1", "name": name})
			_, err := call(s["finetune.export"], "named-user", "u1", string(args))
			Expect(err).To(MatchError(ErrBadRequest), "%q", name)
		}
		Expect(ex.calls).To(Equal(0), "service called for a refused name")
	})

	It("export refuses an export format that is not listed", func() {
		ex := newExporter("/models/x")
		s := modelSpecs(ex, nil)
		for _, f := range []string{"adapter", "merged_8bit", "GGUF", "gguf "} {
			args, _ := json.Marshal(map[string]string{"jobId": "j1", "exportFormat": f})
			_, err := call(s["finetune.export"], "named-user", "u1", string(args))
			Expect(err).To(MatchError(ErrBadRequest), "%q", f)
		}
		for _, f := range []string{"lora", "merged_16bit", "merged_4bit", "gguf"} {
			args, _ := json.Marshal(map[string]string{"jobId": "j1", "exportFormat": f})
			_, err := call(s["finetune.export"], "named-user", "u1", string(args))
			Expect(err).ToNot(HaveOccurred(), "%q", f)
		}
	})

	It("export of another user's job is not found", func() {
		ex := newExporter("/models/x")
		ex.owner = map[string]string{"j1": "u2"}
		s := modelSpecs(ex, nil)
		_, err := call(s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`)
		Expect(err).To(MatchError(ErrNotFound))
	})

	It("export runs once per operation ID", func() {
		ex := newExporter("/models/my-model")
		b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
		Expect(RegisterEngineModels(b, ex, nil)).To(Succeed())
		spec := b.specs["finetune.export"]
		c := Call{AddonID: "fine-tuning", OperationID: "op1", Actor: contract.Actor{UserID: "u1", Mode: "named-user"}, Arguments: json.RawMessage(`{"jobId":"j1"}`)}
		first, err := b.run(context.Background(), spec, c)
		Expect(err).ToNot(HaveOccurred())
		second, err := b.run(context.Background(), spec, c)
		Expect(err).ToNot(HaveOccurred())
		Expect(ex.calls).To(Equal(1))
		Expect(string(first)).To(Equal(string(second)))
	})

	It("export refuses a returned path that is not a model name", func() {
		ex := newExporter("/models/bad name")
		s := modelSpecs(ex, nil)
		_, err := call(s["finetune.export"], "named-user", "u1", `{"jobId":"j1"}`)
		Expect(err).To(HaveOccurred(), "want an outage error")
		Expect(err).ToNot(MatchError(ErrBadRequest), "want an outage error")
	})

	It("import registers the quantized model and returns only its name", func() {
		im := &fakeImporter{path: "/models/q-model"}
		s := modelSpecs(nil, im)
		out, err := call(s["quantize.import"], "named-user", "u1", `{"jobId":"q1","name":"q-model"}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(Equal(`{"model":"q-model"}`))
	})

	It("import of a job that is not completed is a conflict", func() {
		im := &fakeImporter{err: fmt.Errorf("%w: job q1 is not completed", ErrConflict)}
		s := modelSpecs(nil, im)
		_, err := call(s["quantize.import"], "named-user", "u1", `{"jobId":"q1"}`)
		Expect(err).To(MatchError(ErrConflict))
	})

	It("model operations are apply, idempotent and admin only", func() {
		s := modelSpecs(newExporter("/models/x"), &fakeImporter{})
		for _, op := range []string{"finetune.export", "quantize.import"} {
			Expect(s[op].Scope).To(Equal("apply"), op)
			Expect(s[op].Idempotent).To(BeTrue(), op)
			Expect(s[op].Admin).To(BeTrue(), op)
		}
		Expect(s["finetune.export"].Feature).To(Equal(featureFineTuning))
		Expect(s["quantize.import"].Feature).To(Equal(featureQuantization))
	})

	It("export is refused for a feature-holding non-admin", func() {
		ex := newExporter("/models/my-model")
		b := adminBroker(ex, nil)
		code, out := serveAs(b, "finetune.export", "named-user", "u1", `{"jobId":"j1"}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal(contract.HostFeature))
		Expect(ex.calls).To(Equal(0), "service called for a non-admin")
	})

	It("export is allowed for an admin", func() {
		ex := newExporter("/models/my-model")
		ex.owner = map[string]string{"j1": "admin"}
		b := adminBroker(ex, nil)
		code, out := serveAs(b, "finetune.export", "named-user", "admin", `{"jobId":"j1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out.Error).To(BeNil())
	})

	It("import is refused for a feature-holding non-admin", func() {
		im := &fakeImporter{path: "/models/q-model"}
		b := adminBroker(nil, im)
		code, out := serveAs(b, "quantize.import", "named-user", "u1", `{"jobId":"q1"}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal(contract.HostFeature))
		Expect(im.calls).To(Equal(0), "service called for a non-admin")
	})

	It("import is allowed for an admin", func() {
		im := &fakeImporter{path: "/models/q-model"}
		b := adminBroker(nil, im)
		code, out := serveAs(b, "quantize.import", "named-user", "admin", `{"jobId":"q1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out.Error).To(BeNil())
	})

	// The broker does not shortcut the local owner: the admin check decides in
	// every mode, so a local-owner actor the check refuses is refused.
	It("lets the admin check decide in every mode", func() {
		ex := newExporter("/models/my-model")
		ex.owner = map[string]string{"j1": "u1"}
		b := adminBroker(ex, nil)
		code, out := serveAs(b, "finetune.export", "local-owner", "u1", `{"jobId":"j1"}`)
		Expect(code).To(Equal(http.StatusForbidden), "local-owner actor the check refuses")
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal(contract.HostFeature))
		Expect(ex.calls).To(Equal(0), "service called for a refused actor")
	})
})
