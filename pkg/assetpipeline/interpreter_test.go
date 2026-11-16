// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	"github.com/mudler/LocalAI/pkg/assetpipeline/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Flat event interpreter", func() {
	It("records only changed configuration fields and exports configuration independent of execution", func() {
		w := replayFixture()
		b := w.Branches[0]
		g := b.Current().Graph
		g.Primary = "image"
		g.Nodes[2].Model = "z-image"
		g.Nodes[2].Count = 4
		start := len(b.Events())
		Expect(w.Configure(b.ID, g, time.Now())).To(Succeed())

		var paths []string
		for _, e := range b.Events()[start:] {
			Expect(e.Version).To(Equal(2))

			if e.Type == schema.ConfigurationEvent {
				paths = append(paths, e.Node+string(e.Path))
			}

			raw, err := json.Marshal(e)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring(`"changes"`))
			Expect(string(raw)).NotTo(ContainSubstring(`"graph"`))
		}
		Expect(paths).To(ConsistOf("image/model", "image/count", "/primary"))

		_, err := w.RunNode(b.ID, "review", time.Now())
		Expect(err).NotTo(HaveOccurred())

		a := b.Current().Nodes["review"].Attempt
		Expect(w.Complete(b.ID, a.ID, []Value{{Type: "text", Text: "apple"}}, "", nil, nil, time.Now())).To(Succeed())

		config, err := b.ConfigurationAt("")
		Expect(err).NotTo(HaveOccurred())
		Expect(config).To(Equal(g))

		raw, err := json.Marshal(config)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(`"state"`))
		Expect(string(raw)).NotTo(ContainSubstring(`"attempt"`))
	})

	It("rejects hidden fields, type mismatches and unsupported paths", func() {
		for _, e := range []Event{
			{
				Type:      schema.ConfigurationEvent,
				Node:      "image",
				Path:      "/count",
				ValueType: schema.String,
				Value:     json.RawMessage(`"4"`),
			},
			{
				Type:      schema.ConfigurationEvent,
				Node:      "image",
				Path:      "/count",
				ValueType: schema.Integer,
				Value:     json.RawMessage(`4`),
				Status:    Running,
			},
			{
				Type:      schema.ConfigurationEvent,
				Node:      "image",
				Path:      "/state/status",
				ValueType: schema.String,
				Value:     json.RawMessage(`"running"`),
			},
			{
				Type:    schema.ExecutionEvent,
				Node:    "image",
				Action:  schema.Invalidated,
				Outputs: []Value{{Type: "image", Artifact: "hidden"}},
			},
			{Type: schema.ExecutionEvent, Action: schema.Notice, Value: json.RawMessage(`42`)},
			{
				Type:      schema.SystemEvent,
				Action:    schema.Observed,
				Path:      "free-vram",
				ValueType: schema.Integer,
				Value:     json.RawMessage(`42`),
				RunAll:    new(bool),
			},
		} {
			e.Version = 2
			s := StateFromSnapshot(replayFixture().Branches[0].Current())
			Expect(ApplyEvent(&s, &e)).NotTo(Succeed())
		}
	})

	It("records system observations without touching configuration or invoking anything", func() {
		s := StateFromSnapshot(replayFixture().Branches[0].Current())
		config := s.Snapshot().Graph
		e := Event{
			Version:   2,
			Type:      schema.SystemEvent,
			Action:    schema.Observed,
			Path:      "free-vram-bytes",
			ValueType: schema.Integer,
			Value:     int64(8589934592),
		}
		Expect(ApplyEvent(&s, &e)).To(Succeed())
		Expect(s.Snapshot().Graph).To(Equal(config))
		Expect(s.Snapshot().Busy()).To(BeFalse())
		Expect(s.Snapshot().Observations[string(e.Path)]).To(Equal(int64(8589934592)))
	})

	It("preserves wrapped structured failures and classifies cancellation without string matching", func() {
		w := replayFixture()
		b := w.Branches[0]
		_, err := w.RunNode(b.ID, "review", time.Now())
		Expect(err).NotTo(HaveOccurred())

		a := b.Current().Nodes["review"].Attempt
		Expect(w.Complete(b.ID, a.ID, nil, "", nil, fmt.Errorf("backend stopped: %w", context.Canceled), time.Now())).To(Succeed())

		state := b.Current().Nodes["review"]
		Expect(state.Attempt.Status).To(Equal(Cancelled))
		Expect(state.Attempt.Failure.Code).To(Equal(schema.CancelledFailure))
		Expect(state.Attempt.Failure.Cause.Message).To(Equal("context canceled"))

		restored := roundTrip(w)
		Expect(restored.Branches[0].Current().Nodes["review"].Attempt.Failure).To(Equal(state.Attempt.Failure))
	})

	It("replaces bindings and parameter maps instead of merging omitted fields", func() {
		w := replayFixture()
		b := w.Branches[0]
		g := b.Current().Graph
		g.Nodes[1].Input = Binding{Value: &Value{Type: "text", Text: "direct"}}
		g.Nodes[3].Params = map[string]string{"background": "black"}
		Expect(w.Configure(b.ID, g, time.Now())).To(Succeed())
		Expect(b.Current().Graph).To(Equal(g))

		g.Nodes[1].Input = Binding{From: "source"}
		g.Nodes[3].Params = nil
		Expect(w.Configure(b.ID, g, time.Now())).To(Succeed())
		Expect(b.Current().Graph).To(Equal(g))
	})

	It("turns missing output payloads into failures before they can be selected", func() {
		w := replayFixture()
		b := w.Branches[0]
		_, err := w.RunNode(b.ID, "review", time.Now())
		Expect(err).NotTo(HaveOccurred())

		a := b.Current().Nodes["review"].Attempt
		invalid := Event{
			Version:   2,
			Type:      schema.ExecutionEvent,
			Action:    schema.Finished,
			Node:      "review",
			AttemptID: a.ID,
			Status:    Succeeded,
			Outputs:   []Value{{Type: "text"}},
		}
		state := StateFromSnapshot(b.Current())
		Expect(ApplyEvent(&state, &invalid)).To(MatchError("input text is empty"))
		Expect(w.Complete(b.ID, a.ID, invalid.Outputs, "", nil, nil, time.Now())).To(Succeed())
		Expect(b.Current().Nodes["review"].Attempt.Status).To(Equal(Failed))
		Expect(b.Current().Nodes["review"].Attempt.Failure.Code).To(Equal(schema.InvalidOutputFailure))
		Expect(b.Current().Nodes["review"].Selected).To(BeNil())
	})

	It("keeps producer pointers stable through execution events", func() {
		w := replayFixture()
		b := w.Branches[0]
		_, err := w.RunNode(b.ID, "review", time.Now())
		Expect(err).NotTo(HaveOccurred())

		state := StateFromSnapshot(b.Current())
		producer := state.Graph.Nodes[1]
		consumer := state.Graph.Nodes[2].(*ImageGenerationNode)
		Expect(consumer.Config.Input.(TextInput).Source).To(BeIdenticalTo(producer))

		e := Event{
			Version:   2,
			Type:      schema.ExecutionEvent,
			Action:    schema.Finished,
			Node:      producer.ID(),
			AttemptID: producer.Execution().Attempt.ID,
			Status:    Succeeded,
			Outputs:   []Value{{Type: "text", Text: "accepted"}},
		}
		Expect(ApplyEvent(&state, &e)).To(Succeed())

		e.Action, e.Status, e.Selection, e.Outputs = schema.Selected, "", &e.Outputs[0], nil
		Expect(ApplyEvent(&state, &e)).To(Succeed())
		Expect(state.Graph.Nodes[1]).To(BeIdenticalTo(producer))

		value, _, err := consumer.ResolveInputs()
		Expect(err).NotTo(HaveOccurred())
		Expect(value.Text).To(Equal("accepted"))
	})
})

var _ = Describe("Typed runtime nodes", func() {
	It("rejects unsupported engine input capabilities without panicking", func() {
		for _, typ := range []ValueType{"mesh", "motion"} {
			g := DefaultGraph()
			g.Target.Inputs = []string{string(typ)}
			g.Nodes[4].Input = Binding{Value: &Value{Type: typ, Artifact: "asset"}}
			Expect(g.Validate()).To(MatchError(ContainSubstring("incompatible")))

			_, err := BuildGraph(g, nil)
			Expect(err).To(HaveOccurred())
		}
	})

	It("retains invalid field edits as a non-executable draft instead of losing configuration", func() {
		w := replayFixture()
		b := w.Branches[0]
		g := b.Current().Graph
		g.Nodes[1].Count = 2
		Expect(w.Configure(b.ID, g, time.Now())).To(Succeed())
		Expect(b.Current().Graph).To(Equal(g))

		config, err := b.ConfigurationAt("")
		Expect(err).NotTo(HaveOccurred())
		Expect(config).To(Equal(g))
		Expect(StateFromSnapshot(b.Current()).Graph).To(BeNil())

		_, err = w.RunNode(b.ID, "review", time.Now())
		Expect(err).To(MatchError(ContainSubstring("image count")))
	})

	It("connects direct producer pointers, exposes only supported outputs, and isolates branches", func() {
		g := DefaultGraph()
		g.Primary = "mesh"
		states := map[string]NodeState{"image": {
			Attempt:  &Attempt{ID: "a", Status: Succeeded},
			Selected: &Value{Type: "image", Artifact: "image-asset"},
		}}
		runtime, err := BuildGraph(g, states)
		Expect(err).NotTo(HaveOccurred())

		images := runtime.Nodes[2].(*ImageGenerationNode)
		extract := runtime.Nodes[3].(*ExtractionNode)
		mesh := runtime.Nodes[4].(*MeshGenerationNode)
		Expect(extract.Config.Input.(ImageInput).Source).To(BeIdenticalTo(images))
		Expect(mesh.Config.Input.(ImageInput).Source).To(BeIdenticalTo(extract))

		_, isImageProducer := any(mesh).(ImageProducer)
		Expect(isImageProducer).To(BeFalse())

		input, _, err := extract.ResolveInputs()
		Expect(err).NotTo(HaveOccurred())
		Expect(input.Artifact).To(Equal("image-asset"))

		images.State.Stale = true
		_, _, err = extract.ResolveInputs()
		Expect(err).To(HaveOccurred())
		Expect(runtime.Configuration()).To(Equal(g))

		other, err := BuildGraph(g, states)
		Expect(err).NotTo(HaveOccurred())
		Expect(other.Nodes[2]).NotTo(BeIdenticalTo(images))
		Expect(other.Nodes[2].Execution().Stale).To(BeFalse())

		_, _, err = other.Nodes[3].ResolveInputs()
		Expect(err).NotTo(HaveOccurred())
	})

	It("validates the supported mesh format at its boundary", func() {
		Expect(schema.GLB.Valid()).To(BeTrue())
		Expect(schema.GLB.MIME()).To(Equal("model/gltf-binary"))
		Expect(schema.MeshFormat("obj").Valid()).To(BeFalse())
		Expect(schema.MeshFormat("obj").MIME()).To(BeEmpty())
	})

	It("does not share nested mutable execution values with another projection or the caller", func() {
		g := DefaultGraph()
		states := map[string]NodeState{"image": {
			Attempt: &Attempt{
				ID:        "a",
				Status:    Succeeded,
				Arguments: Arguments{Node: NodeConfig{Params: map[string]string{"width": "1024"}}},
				Outputs:   []Value{{Type: "image", Artifact: "original"}},
				Failure:   &Failure{Message: "original"},
			},
			Selected: &Value{Type: "image", Artifact: "original"},
		}}
		first, err := BuildGraph(g, states)
		Expect(err).NotTo(HaveOccurred())

		second, err := BuildGraph(g, states)
		Expect(err).NotTo(HaveOccurred())

		changed := first.Nodes[2].Execution()
		changed.Attempt.Outputs[0].Artifact = "changed"
		changed.Attempt.Arguments.Node.Params["width"] = "1"
		changed.Attempt.Failure.Message = "changed"
		changed.Selected.Artifact = "changed"
		Expect(*second.Nodes[2].Execution()).To(Equal(states["image"]))
		Expect(states["image"].Selected.Artifact).To(Equal("original"))
		Expect(states["image"].Attempt.Outputs[0].Artifact).To(Equal("original"))
		Expect(states["image"].Attempt.Arguments.Node.Params["width"]).To(Equal("1024"))
		Expect(states["image"].Attempt.Failure.Message).To(Equal("original"))
	})
})
