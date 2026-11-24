// SPDX-License-Identifier: MIT
package main

import (
	"encoding/json"
	"math"

	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("Source-frame pose projection", func() {
	It("projects translated camera joints using landscape and portrait frame intrinsics", func() {
		camera := make([]float32, 231)
		camera[3], camera[4], camera[5] = 1, -1, 2
		p := poseProjection{joints: []int{1, 0}, width: 640, height: 480}

		pixels := p.project(camera, []float32{0, 0, 2})

		Expect(pixels).To(Equal([]float32{480, 80, 320, 240}))

		p.width, p.height = 480, 640
		pixels = p.project(camera, []float32{0, 0, 2})

		Expect(pixels).To(Equal([]float32{400, 160, 240, 320}))
	})
	It("preserves SMPL ordering from the native definition", func() {
		mapping := make([]int, 24)
		for i := range mapping {
			mapping[i] = 76 - i
		}

		raw, err := json.Marshal(map[string]any{"mapping": mapping})
		Expect(err).NotTo(HaveOccurred())

		p, err := projectionForProfile(map[string]json.RawMessage{"smpl24": raw}, "smpl24")

		Expect(err).NotTo(HaveOccurred())
		Expect(p.joints).To(Equal(mapping))
	})
	It("preserves SOMA identity ordering", func() {
		soma, err := projectionForProfile(nil, "soma77")

		Expect(err).NotTo(HaveOccurred())
		Expect(soma.joints).To(HaveLen(77))
		for i, joint := range soma.joints {
			Expect(joint).To(Equal(i))
		}
	})
	It("rejects an incomplete native mapping", func() {
		definition := map[string]json.RawMessage{"smpl24": json.RawMessage(`{"mapping":[0]}`)}

		_, err := projectionForProfile(definition, "smpl24")

		Expect(err).To(HaveOccurred())
	})
	DescribeTable("omits image coordinates for unusable input",
		func(camera []float32, translation []float32, width uint32) {
			p := poseProjection{joints: []int{0}, width: width, height: 480}

			pixels := p.project(camera, translation)

			Expect(pixels).To(BeNil())
		},
		Entry("zero depth", make([]float32, 231), []float32{0, 0, 0}, uint32(640)),
		Entry("behind camera", make([]float32, 231), []float32{0, 0, -1}, uint32(640)),
		Entry("nonfinite translation", make([]float32, 231), []float32{float32(math.NaN()), 0, 1}, uint32(640)),
		Entry("incomplete camera channel", make([]float32, 3), []float32{0, 0, 1}, uint32(640)),
		Entry("missing frame dimensions", make([]float32, 231), []float32{0, 0, 1}, uint32(0)),
	)
	It("round-trips optional image positions alongside frame identity", func() {
		pose := &motion.Pose{Sequence: 7, SourceTimeUs: 42, ImagePositions: []float32{12, 34}}

		encoded, err := proto.Marshal(pose)
		Expect(err).NotTo(HaveOccurred())

		decoded := &motion.Pose{}
		Expect(proto.Unmarshal(encoded, decoded)).To(Succeed())

		Expect(proto.Equal(pose, decoded)).To(BeTrue())
	})
})
