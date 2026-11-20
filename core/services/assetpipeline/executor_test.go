// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExecutor(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Asset pipeline adapters") }

var _ = Describe("Stage admission", func() {
	It("rejects a secondary artifact from another pipeline before model loading", func() {
		ctx := context.Background()
		store, err := pipeline.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = store.Close() }()
		_, err = store.Update(ctx, "other", "create", 0, "create", func(w *pipeline.Workspace) error {
			*w = pipeline.New("other", pipeline.DefaultGraph(), time.Now())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		artifact, err := store.PutArtifact(ctx, "other", "motion.glb", "model/gltf-binary", bytes.NewReader([]byte("fixture")))
		Expect(err).NotTo(HaveOccurred())

		e := &Executor{
			Store:     store,
			Authorize: func(context.Context, string, string, pipeline.Kind) (bool, error) { return false, nil },
		}
		_, err = e.Execute(ctx, "actor", "current", pipeline.Arguments{
			Node:   pipeline.NodeConfig{Kind: pipeline.Retarget},
			Inputs: map[string]pipeline.Value{"motion": {Type: "motion", Artifact: artifact.ID}},
		}, func(string) { Fail("must not run inference") })
		Expect(err).To(HaveOccurred())
	})

	It("refuses missing or mislabeled reference images before inference", func() {
		ctx := context.Background()
		store, err := pipeline.Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = store.Close() }()
		_, err = store.Update(ctx, "editing", "create", 0, "create", func(w *pipeline.Workspace) error {
			*w = pipeline.New("editing", pipeline.DefaultGraph(), time.Now())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		mesh, err := store.PutArtifact(ctx, "editing", "mesh.glb", pipeline.MIMEGLB, bytes.NewReader([]byte("fixture")))
		Expect(err).NotTo(HaveOccurred())
		for _, reference := range []pipeline.Value{
			{Type: "image"}, {Type: "image", Artifact: "missing"},
			{Type: "image", Artifact: mesh.ID}, {Type: "mesh", Artifact: mesh.ID},
		} {
			_, err = (&Executor{Store: store}).images(ctx, "editing", pipeline.Arguments{
				Node:   pipeline.NodeConfig{Kind: pipeline.Image, Count: 1},
				Inputs: map[string]pipeline.Value{pipeline.ReferenceInput: reference},
			}, nil, func(string) { Fail("invalid reference must not start inference") })
			Expect(err).To(HaveOccurred())
		}
	})

	It("rejects segmentation points from a previous input before accessing artifacts or inference", func() {
		a := pipeline.Arguments{
			Node:  pipeline.NodeConfig{Params: map[string]string{"points": "[10,20,1]", "points_image": "previous"}},
			Input: pipeline.Value{Type: "image", Artifact: "current"},
		}
		_, err := (&Executor{}).extract(context.Background(), "resource", a, nil, func(string) { Fail("must not start inference") })
		Expect(err).To(MatchError(ContainSubstring("input image changed; clear segmentation points")))
	})

	It("accepts matching, untagged legacy, and cleared segmentation points", func() {
		a := pipeline.Arguments{
			Node:  pipeline.NodeConfig{Params: map[string]string{"points": "[10,20,1]"}},
			Input: pipeline.Value{Type: "image", Artifact: "current"},
		}
		for _, origin := range []string{"", "current"} {
			a.Node.Params["points_image"] = origin
			points, _, err := segmentationCoordinates(a)
			Expect(err).NotTo(HaveOccurred())
			Expect(points).To(Equal([]float32{10, 20, 1}))
		}

		a.Node.Params["points_image"] = "previous"
		a.Node.Params["points"] = "[]"
		_, _, err := segmentationCoordinates(a)
		Expect(err).NotTo(HaveOccurred())
	})

	It("validates coordinate syntax and tuple lengths before inference", func() {
		for _, params := range []map[string]string{{"points": "invalid"}, {"points": "[1,2]"}, {"boxes": "invalid"}, {"boxes": "[1,2,3]"}} {
			_, _, err := segmentationCoordinates(pipeline.Arguments{Node: pipeline.NodeConfig{Params: params}})
			Expect(err).To(HaveOccurred())
		}
	})

	It("checks resource and current feature authority even for a source node", func() {
		ctx := context.Background()
		a := pipeline.Arguments{
			Node:  pipeline.NodeConfig{Kind: pipeline.Source},
			Input: pipeline.Value{Type: "text", Text: "tower"},
		}
		var calls []string
		e := &Executor{AuthorizeResource: func(context.Context, string, string) error { calls = append(calls, "resource"); return nil }, Authorize: func(context.Context, string, string, pipeline.Kind) (bool, error) {
			calls = append(calls, "feature")
			return false, nil
		}}
		result, err := e.Execute(ctx, "actor", "resource", a, func(string) {})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Outputs).To(Equal([]pipeline.Value{a.Input}))
		Expect(calls).To(Equal([]string{"resource", "feature"}))

		e.AuthorizeResource = func(context.Context, string, string) error { return errors.New("revoked") }
		_, err = e.Execute(ctx, "actor", "resource", a, func(string) {})
		Expect(err).To(MatchError("revoked"))
		Expect(calls).To(HaveLen(2))
	})

	It("fails closed without an authorization service", func() {
		_, err := (&Executor{}).Execute(context.Background(), "actor", "r", pipeline.Arguments{}, func(string) {})
		Expect(err).To(HaveOccurred())
	})

	It("bounds inference parameters before allocating backend work", func() {
		value, err := intParam(nil, "steps", 9, 1, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal(9))

		for _, input := range []string{"0", "101", "invalid"} {
			_, err = intParam(map[string]string{"steps": input}, "steps", 9, 1, 100)
			Expect(err).To(HaveOccurred())
		}
	})
})
