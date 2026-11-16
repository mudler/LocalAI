// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type workbookExecutor struct{}

func (workbookExecutor) Execute(_ context.Context, _, _ string, a pipeline.Arguments, _ func(string)) (pipeline.Result, error) {
	return pipeline.Result{Outputs: []pipeline.Value{a.Input}}, nil
}

var _ = Describe("Pipeline host operations", func() {
	It("returns the new execution branch after a per-node rerun and Run all", func() {
		ctx := context.Background()
		s, err := pipeline.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		r, err := pipeline.NewRunner(ctx, s, workbookExecutor{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		broker := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
		Expect(RegisterPipeline(broker, func(string) (*pipeline.Runner, *pipeline.Store, error) { return r, s, nil })).To(Succeed())
		g := pipeline.DefaultGraph()
		g.Primary = "review"
		g.Nodes[0].Input.Value.Text = "tower"
		g.Nodes[1].Model, g.Nodes[1].Pause = "reviewer", false
		w, err := r.Apply(ctx, "owner", "asset", "create", 0, pipeline.Command{Action: "create", Graph: &g})
		Expect(err).NotTo(HaveOccurred())
		branch := w.Branches[0].ID
		for i, action := range []string{"run-node", "run-node", "run-all"} {
			Eventually(func() error {
				w, err = r.Read(ctx, "asset")
				if err != nil {
					return err
				}
				args, err := json.Marshal(map[string]any{"revision": w.Revision, "command": pipeline.Command{Action: pipeline.CommandAction(action), Branch: branch, Node: "review"}})
				if err != nil {
					return err
				}
				out, err := broker.specs["pipeline.apply"].Handler(ctx, Call{AddonID: "asset-pipeline", ResourceID: "asset", OperationID: action + string(rune('a'+i)), Arguments: args})
				if err != nil {
					return err
				}
				var view pipeline.View
				Expect(json.Unmarshal(out, &view)).To(Succeed())
				if i > 0 {
					Expect(view.Branch).NotTo(Equal(branch))
				}
				branch = view.Branch
				return nil
			}).Should(Succeed())
			Eventually(func() pipeline.Status {
				got, _ := r.Read(ctx, "asset")
				b, _ := got.Branch(branch)
				return b.Current().Nodes["review"].Status()
			}).Should(Equal(pipeline.Succeeded))
		}
	})
	It("uses envelope resource scope, revision checks and durable operation replay", func() {
		ctx := context.Background()
		s, err := pipeline.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		r, err := pipeline.NewRunner(ctx, s, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(r.Close(ctx)).To(Succeed()) })
		b := New(NewInFlight(), func(string, string) bool { return true }, contract.DefaultLimits())
		Expect(RegisterPipeline(b, func(addon string) (*pipeline.Runner, *pipeline.Store, error) {
			Expect(addon).To(Equal("asset-pipeline"))
			return r, s, nil
		})).To(Succeed())
		call := Call{AddonID: "asset-pipeline", ResourceID: "first", OperationID: "create", Actor: contract.Actor{Mode: "local-owner", UserID: "local-owner"}, Arguments: json.RawMessage(`{"revision":0,"command":{"action":"create","name":"test"}}`)}
		out, err := b.specs["pipeline.apply"].Handler(ctx, call)
		Expect(err).NotTo(HaveOccurred())
		again, err := b.specs["pipeline.apply"].Handler(ctx, call)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(out))
		call.Arguments = json.RawMessage(`{}`)
		call.ResourceID = "other"
		_, err = b.specs["pipeline.read"].Handler(ctx, call)
		Expect(err).To(MatchError(ErrNotFound))
		call.ResourceID = "first"
		call.Arguments = json.RawMessage(`{"branch":"unknown"}`)
		_, err = b.specs["pipeline.read"].Handler(ctx, call)
		Expect(err).To(MatchError(ErrNotFound))
		call.Arguments = json.RawMessage(`{"revision":0,"command":{"action":"create","name":"changed"}}`)
		_, err = b.specs["pipeline.apply"].Handler(ctx, call)
		Expect(err).To(MatchError(ErrConflict))
		call.ResourceID = ""
		_, err = b.specs["pipeline.read"].Handler(ctx, call)
		Expect(err).To(MatchError(ErrBadRequest))
	})
})
