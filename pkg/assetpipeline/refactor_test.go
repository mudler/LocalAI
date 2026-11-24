// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"encoding/json"
	"fmt"
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pipeline ownership and serialization boundaries", func() {
	It("owns captured arguments, outputs and all public history copies", func() {
		w := replayFixture()
		b := w.Branches[0]
		now := time.Now()
		a, err := w.Begin(b.ID, now)
		Expect(err).NotTo(HaveOccurred())
		a.Arguments.Node.Input.Value.Text = "caller changed arguments"
		outputs := []Value{{Type: schema.TextValue, Text: "accepted"}}
		diagnostics := []string{"original diagnostic"}
		Expect(w.Complete(b.ID, a.ID, outputs, "", diagnostics, nil, now)).To(Succeed())
		outputs[0].Text = "caller changed output"
		diagnostics[0] = "caller changed diagnostic"
		for _, event := range b.History() {
			event.Message = "caller changed message"
			if event.Arguments != nil {
				event.Arguments.Input.Text = "caller changed input"
			}
			if len(event.Outputs) > 0 {
				event.Outputs[0].Text = "caller changed history output"
			}
		}
		for _, event := range b.Events() {
			event.ID = "caller changed ID"
		}
		replay, err := b.Replay("")
		Expect(err).NotTo(HaveOccurred())
		Expect(replay).To(Equal(b.Current()))
		Expect(replay.Nodes["source"].Selected.Text).To(Equal("accepted"))
		Expect(replay.Nodes["source"].Attempt.Diagnostics).To(Equal([]string{"original diagnostic"}))
		Expect(roundTrip(w).Branches[0].Current()).To(Equal(replay))

		id, err := w.Fork(b.ID, b.Events()[len(b.Events())-1].ID, now)
		Expect(err).NotTo(HaveOccurred())
		child, err := w.Branch(id)
		Expect(err).NotTo(HaveOccurred())
		child.Checkpoint().ID = "caller changed checkpoint"
		_, err = child.Replay("")
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects invocation arguments that disagree with the configured node or resolved inputs", func() {
		w := replayFixture()
		b := w.Branches[0]
		before := b.Current()
		a, err := w.Begin(b.ID, time.Now())
		Expect(err).NotTo(HaveOccurred())
		for _, change := range []func(*Arguments){
			func(a *Arguments) { a.Node.Kind = Mesh },
			func(a *Arguments) { a.Input.Text = "different input" },
			func(a *Arguments) { a.Target.Advice = "different profile" },
		} {
			arguments := a.Arguments
			change(&arguments)
			state := StateFromSnapshot(before)
			event := Event{Version: 2, Type: schema.ExecutionEvent, Action: schema.Started, Node: "source", AttemptID: "invalid", Arguments: &arguments}
			Expect(ApplyEvent(&state, &event)).To(MatchError(ContainSubstring("arguments do not match")))
			Expect(state.Snapshot()).To(Equal(before))
		}
	})

	It("replaces nodes at the limit and preserves the result through replay", func() {
		g := Graph{Primary: "review", Nodes: []NodeConfig{{ID: "source", Kind: Source, Input: Binding{Value: &Value{Type: schema.TextValue, Text: "tower"}}}}}
		for i := 0; i < 30; i++ {
			g.Nodes = append(g.Nodes, NodeConfig{ID: fmt.Sprintf("unused-%d", i), Kind: Source, Input: Binding{Value: &Value{Type: schema.TextValue, Text: "unused"}}})
		}
		g.Nodes = append(g.Nodes, NodeConfig{ID: "review", Kind: Review, Input: Binding{From: "source"}})
		w := New("full", g, time.Now())
		b := w.Branches[0]
		g.Nodes[1].ID = "replacement"
		Expect(g.Validate()).To(Succeed())
		Expect(w.Configure(b.ID, g, time.Now())).To(Succeed())
		Expect(b.Current().Graph).To(Equal(g))
		Expect(roundTrip(w).Branches[0].Current().Graph).To(Equal(g))
	})

	It("decodes typed configuration values and rejects unknown nested fields", func() {
		var event Event
		raw := []byte(`{"version":2,"type":"configuration","node":"image","path":"/seed","valueType":"integer","value":9007199254740993}`)
		Expect(json.Unmarshal(raw, &event)).To(Succeed())
		Expect(event.Value).To(Equal(int64(9007199254740993)))
		raw = []byte(`{"version":2,"type":"configuration","node":"image","path":"/input","valueType":"object","value":{"from":"source","typo":true}}`)
		Expect(json.Unmarshal(raw, &event)).NotTo(Succeed())
		raw = []byte(`{"version":2,"type":"configuration","node":"image","path":"/count","valueType":"integer","value":1.5}`)
		Expect(json.Unmarshal(raw, &event)).NotTo(Succeed())
	})

	It("shares one execution record between the owner and connected nodes", func() {
		state := StateFromSnapshot(replayFixture().Branches[0].Current())
		node := state.Graph.Nodes[0]
		node.Execution().Selected = &Value{Type: schema.TextValue, Text: "shared"}
		Expect(state.Snapshot().Nodes[node.ID()].Selected.Text).To(Equal("shared"))
	})

	It("preserves nested observation numbers and isolates their containers", func() {
		raw := []byte(`{"version":2,"type":"system","action":"observed","path":"resources","valueType":"object","value":{"bytes":9007199254740993,"labels":["original"]}}`)
		var event Event
		Expect(json.Unmarshal(raw, &event)).To(Succeed())
		state := StateFromSnapshot(replayFixture().Branches[0].Current())
		Expect(ApplyEvent(&state, &event)).To(Succeed())
		event.Value.(map[string]any)["labels"].([]any)[0] = "changed"
		snapshot := state.Snapshot()
		snapshot.Observations["resources"].(map[string]any)["labels"].([]any)[0] = "changed again"
		encoded, err := json.Marshal(state.Snapshot().Observations["resources"])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`9007199254740993`))
		Expect(string(encoded)).To(ContainSubstring(`"original"`))
	})

	It("copies empty maps without applying JSON omission rules", func() {
		g := replayFixture().Branches[0].Current().Graph
		g.Nodes[0].Params = map[string]string{}
		w := New("empty maps", g, time.Now())
		Expect(w.Branches[0].Current().Graph.Nodes[0].Params).NotTo(BeNil())
		Expect(w.Branches[0].Current().Graph).To(Equal(g))
		_, err := w.Begin(w.Branches[0].ID, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(roundTrip(w).Branches[0].Current()).To(Equal(w.Branches[0].Current()))
	})
})
