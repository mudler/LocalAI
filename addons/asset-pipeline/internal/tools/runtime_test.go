// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRuntime(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Asset pipeline runtime") }

type host struct {
	runner *pipeline.Runner
	calls  int
}

func (h *host) Do(ctx context.Context, op, kind string, args, out any) error {
	h.calls++
	b, _ := json.Marshal(args)
	var a struct {
		Revision uint64
		Command  pipeline.Command
	}
	_ = json.Unmarshal(b, &a)
	w, err := h.runner.Apply(ctx, "local-owner|local-owner", "resource", op, a.Revision, a.Command)
	if err != nil {
		return err
	}
	b, _ = json.Marshal(pipeline.MakeView(w, "", 0))
	return json.Unmarshal(b, out)
}

var _ = Describe("Packaged runtime", func() {
	It("has a valid manifest with the exact required host operations", func() {
		data, err := os.ReadFile("../../manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Capabilities.HostOperations.Required).To(ConsistOf("pipeline.read", "pipeline.apply", "pipeline.upload"))
	})
	It("emits contract-valid artifact handles and confines output reads to their resource", func() {
		ctx := context.Background()
		rt := New(protocol.Startup{DataDir: GinkgoT().TempDir()})
		DeferCleanup(func() { Expect(rt.Close(ctx)).To(Succeed()) })
		fakeRunner, err := pipeline.NewRunner(ctx, rt.store, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(fakeRunner.Close(ctx)).To(Succeed()) })
		_, err = fakeRunner.Apply(ctx, "actor", "resource", "create", 0, pipeline.Command{Action: "create", Name: "asset"})
		Expect(err).NotTo(HaveOccurred())
		asset, err := rt.store.PutArtifact(ctx, "resource", "mesh.glb", "model/gltf-binary", bytes.NewBufferString("fixture"))
		Expect(err).NotTo(HaveOccurred())
		raw, _ := json.Marshal(map[string]string{"tool": "pipeline-artifact", "artifact": asset.ID})
		env := contract.Envelope{Request: contract.Request{ResourceID: "resource", Arguments: raw}}
		out, err := rt.Read(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Validate(contract.DefaultLimits())).To(Succeed())
		reader, err := rt.OpenOutput(ctx, env, out.Outputs[0].Handle)
		Expect(err).NotTo(HaveOccurred())
		Expect(reader.Close()).To(Succeed())
		env.Request.ResourceID = "foreign"
		_, err = rt.OpenOutput(ctx, env, out.Outputs[0].Handle)
		Expect(err).To(HaveOccurred())
	})
	It("commits durable receipts and does not repeat an acknowledged mutation after restart", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		rt := New(protocol.Startup{DataDir: dir})
		Expect(rt.err).NotTo(HaveOccurred())
		runner, err := pipeline.NewRunner(ctx, rt.store, nil)
		Expect(err).NotTo(HaveOccurred())
		fake := &host{runner: runner}
		rt.engine = fake
		env := contract.Envelope{Request: contract.Request{ResourceID: "resource", OperationID: "op", Arguments: json.RawMessage(`{"tool":"pipeline-create","name":"Tower"}`)}}
		rec, err := rt.Apply(ctx, env, contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptCommitted))
		Expect(fake.calls).To(Equal(1))
		Expect(runner.Close(ctx)).To(Succeed())
		Expect(rt.Close(ctx)).To(Succeed())
		rt = New(protocol.Startup{DataDir: dir})
		DeferCleanup(func() { Expect(rt.Close(ctx)).To(Succeed()) })
		again, err := rt.Apply(ctx, env, contract.Revision{Schema: 1})
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(rec))
		_, err = rt.Receipt(ctx, "unknown")
		Expect(err).To(MatchError(protocol.ErrNotFound))
		w, err := rt.store.Read(ctx, "resource")
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Name).To(Equal("Tower"))
		existsEnv := contract.Envelope{Request: contract.Request{ResourceID: "resource", Arguments: json.RawMessage(`{"tool":"resource-exists"}`)}}
		exists, err := rt.Read(ctx, existsEnv)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(exists.Payload)).To(MatchJSON(`{"exists":true}`))
		existsEnv.Request.ResourceID = "missing"
		exists, err = rt.Read(ctx, existsEnv)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(exists.Payload)).To(MatchJSON(`{"exists":false}`))
		_, err = rt.store.Update(ctx, "resource", "add", w.Revision, "add", func(w *pipeline.Workspace) error { w.Name = "Other"; return nil })
		Expect(err).NotTo(HaveOccurred())
	})
})
