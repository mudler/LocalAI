// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/json"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	motion "github.com/mudler/LocalAI/pkg/motion/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestGemX(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "GEM-X backend") }

var _ = Describe("GEM-X adapter", func() {
	It("rejects malformed RGB and subject coordinates before native inference", func() {
		f := &motion.Frame{Width: 8, Height: 8, Rgb: make([]byte, 192)}
		Expect(validateFrame(f)).To(Succeed())
		f.Width = math.MaxUint32
		Expect(validateFrame(f)).To(HaveOccurred())
		f.Width = 8
		f.SubjectBox = []float32{0, 0, float32(math.NaN()), 1}
		Expect(validateFrame(f)).To(HaveOccurred())
	})
	It("validates all component files and bounded configuration", func() {
		dir := GinkgoT().TempDir()
		for _, name := range []string{"gem", "pose", "detector"} {
			Expect(os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0600)).To(Succeed())
		}
		GinkgoT().Setenv("GEMX_MODULE", dir)
		o := &pb.ModelOptions{ModelFile: "gem", ModelPath: dir, Threads: 32, Options: []string{"vitpose:pose", "yolox:detector"}}
		c, err := parseOptions(o)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.threads).To(Equal(uint32(8)))
		Expect(c.selection).To(Equal(uint32(1)))
		o.Options = append(o.Options, "window:121")
		_, err = parseOptions(o)
		Expect(err).To(HaveOccurred())
		o.Options = []string{"vitpose:pose", "yolox:missing"}
		_, err = parseOptions(o)
		Expect(err).To(HaveOccurred())
	})
	It("publishes only skeleton metadata, never native private configuration", func() {
		var raw map[string]json.RawMessage
		Expect(json.Unmarshal([]byte(`{"config":{"model_paths":{"gem":"/private/model"}},"smpl24":{"schema":"test.v1","joint_names":["pelvis"],"parents":[-1],"root":0,"rest_local_translations":[0,0,0],"rest_local_rotations":[1,0,0,0],"quaternion_order":"wxyz","space":"root-local"}}`), &raw)).To(Succeed())
		d, err := publicDefinition(raw, "smpl24")
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Conventions["quaternion_order"]).To(Equal("wxyz"))
		bytes, err := proto.Marshal(d)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(bytes)).NotTo(ContainSubstring("private"))
		Expect(d.Channels).NotTo(ContainElement("local_rotations"))
	})
	It("rejects unsupported profiles before touching native state", func() {
		g := &GemX{}
		err := g.MotionStream(context.Background(), func() (*pb.MotionRequest, error) { return &pb.MotionRequest{Profile: "robot"}, nil }, nil)
		Expect(err).To(HaveOccurred())
	})
	It("releases session ownership after a client closes during warmup", func() {
		oldReset := liveReset
		DeferCleanup(func() { liveReset = oldReset })
		liveReset = func(uintptr, *byte, uint64) int32 { return 0 }
		raw := map[string]json.RawMessage{"soma77": json.RawMessage(`{"schema":"test","joint_names":["root"],"parents":[-1],"root":0,"rest_local_translations":[0,0,0],"rest_local_rotations":[0,0,0,1]}`)}
		g := &GemX{pipeline: 1, definition: raw}
		calls := 0
		sent := 0
		err := g.MotionStream(context.Background(), func() (*pb.MotionRequest, error) {
			calls++
			if calls == 1 {
				return &pb.MotionRequest{Profile: "soma77"}, nil
			}
			return nil, io.EOF
		}, func(r *pb.MotionResponse) error {
			out := &motion.Output{}
			Expect(proto.Unmarshal(r.Output, out)).To(Succeed())
			Expect(out.GetDefinition()).NotTo(BeNil())
			sent++
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(sent).To(Equal(1))
		Expect(g.active).To(BeFalse())
	})
	It("binds the real native ABI and rejects invalid assets when explicitly enabled", func() {
		library := os.Getenv("GEMX_TEST_LIBRARY")
		if library == "" {
			Skip("set GEMX_TEST_LIBRARY to a built library including the LocalAI bridge")
		}
		Expect(loadNativeLibrary(library)).To(Succeed())
		var handle uintptr
		buf := make([]byte, 2048)
		opts := []uint32{0, 1, 30, 1, 1, 0}
		code := liveCreate("/missing/gem", "/missing/pose", "/missing/detector", "/missing/module", "CPU", &opts[0], 2000000, &handle, &buf[0], uint64(len(buf)))
		Expect(code).NotTo(BeZero())
		Expect(handle).To(BeZero())
		Expect(nativeError(code, buf)).To(HaveOccurred())
	})
	It("streams real model frames with displacement intervals fenced by resets", func() {
		models := os.Getenv("GEMX_TEST_MODELS")
		if models == "" {
			Skip("set GEMX_TEST_MODELS and GEMX_TEST_LIBRARY for native inference")
		}
		Expect(loadNativeLibrary(os.Getenv("GEMX_TEST_LIBRARY"))).To(Succeed())
		g := &GemX{}
		Expect(g.Load(&pb.ModelOptions{ModelFile: "gem-x-contact-f32.gguf", ModelPath: models, Threads: 4, Options: []string{"vitpose:vitpose-f32.gguf", "yolox:yolox-f32.gguf", "device:vulkan"}})).To(Succeed())
		DeferCleanup(func() { Expect(g.Free()).To(Succeed()) })
		requests := []*pb.MotionRequest{{Profile: "smpl24"}}
		timestamps := []int64{0, 100000, 350000, 400000, 3000000, 3100000}
		for i, timestamp := range timestamps {
			f := &motion.Frame{Width: 16, Height: 16, Rgb: make([]byte, 16*16*3), Sequence: uint64(i + 1), SourceTimeUs: timestamp, SubjectBox: []float32{0, 0, 15, 15}, SubjectId: 1}
			if i >= 3 {
				f.SubjectId = 2
			}
			data, err := proto.Marshal(&motion.Input{Payload: &motion.Input_Frame{Frame: f}})
			Expect(err).NotTo(HaveOccurred())
			requests = append(requests, &pb.MotionRequest{Input: data})
		}
		var outputs []*motion.Output
		Expect(g.MotionStream(context.Background(), func() (*pb.MotionRequest, error) {
			if len(requests) == 0 {
				return nil, io.EOF
			}
			r := requests[0]
			requests = requests[1:]
			return r, nil
		}, func(r *pb.MotionResponse) error {
			o := &motion.Output{}
			Expect(proto.Unmarshal(r.Output, o)).To(Succeed())
			outputs = append(outputs, o)
			return nil
		})).To(Succeed())
		Expect(outputs).To(HaveLen(7))
		Expect(outputs[0].GetDefinition().JointNames).To(HaveLen(24))
		Expect(outputs[1].GetEvent().Type).To(Equal("warmup"))
		Expect(outputs[2].GetPose().Positions).To(HaveLen(72))
		Expect(outputs[2].GetPose().Anchor).To(HaveLen(4))
		Expect(outputs[0].GetDefinition().Conventions["image_positions"]).To(Equal("source-pixel xy, joint_names order, top-left origin, unmirrored"))
		Expect(outputs[2].GetPose().ImagePositions).To(HaveLen(48))
		Expect(outputs[2].GetPose().SourceTimeUs).To(Equal(int64(100000)))
		Expect(outputs[4].GetEvent().Type).To(Equal("warmup")) // subject change
		Expect(outputs[5].GetEvent().Type).To(Equal("warmup")) // source-time gap
		Expect(outputs[6].GetPose().Epoch).To(BeNumerically(">", outputs[3].GetPose().Epoch))
		if supportsRootDisplacement(g.definition, "smpl24") {
			Expect(outputs[0].GetDefinition().Channels).To(ContainElement("root_displacement"))
			for _, index := range []int{2, 3, 6} {
				p := outputs[index].GetPose()
				Expect(p.RootDisplacement).To(HaveLen(3))
				Expect(p.DisplacementStartTimeUs).To(Equal(timestamps[index-2]))
				Expect(p.RootTranslation).To(Equal([]float32{0, 0, 0}))
			}
		}
	})

})
