// SPDX-License-Identifier: MIT
package main

import (
	"encoding/binary"
	"encoding/json"
	"maps"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func mutateGLB(glb []byte, mutate func(map[string]any)) []byte {
	size := int(binary.LittleEndian.Uint32(glb[12:]))
	var doc map[string]any
	Expect(json.Unmarshal(glb[20:20+size], &doc)).To(Succeed())
	mutate(doc)
	text, err := json.Marshal(doc)
	Expect(err).NotTo(HaveOccurred())
	for len(text)%4 != 0 {
		text = append(text, ' ')
	}
	out := append([]byte{}, glb[:20]...)
	out = append(out, text...)
	out = append(out, glb[20+size:]...)
	binary.LittleEndian.PutUint32(out[8:], uint32(len(out)))
	binary.LittleEndian.PutUint32(out[12:], uint32(len(text)))
	return out
}

var _ = Describe("Model-free remeshing admission", func() {
	It("does not capture unrelated model probes", func() {
		m := &MeshRemesh{}
		Expect(m.Load(&pb.ModelOptions{ModelFile: "qwen.gguf"})).NotTo(Succeed())
		Expect(m.loaded).To(BeFalse())
		Expect(m.Load(&pb.ModelOptions{ModelFile: "meshremesh"})).To(Succeed())
		Expect(m.Load(&pb.ModelOptions{Model: "meshremesh", ModelFile: "/models/meshremesh"})).To(Succeed())
		Expect(m.Free()).To(Succeed())
		Expect(m.loaded).To(BeFalse())
	})
	It("validates parameters without mutating the recorded request", func() {
		request := &pb.Generate3DRequest{Src: "source.glb", Dst: "dest.glb", Params: map[string]string{"operation": "remesh", "mode": "wrap_quad", "target_quads": "2000"}}
		original := maps.Clone(request.Params)
		params, err := remeshParameters(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(params).NotTo(HaveKey("operation"))
		Expect(request.Params).To(Equal(original))
		for _, invalid := range []map[string]string{{"operation": "generate"}, {"operation": "remesh", "target_quads": "0"}, {"operation": "remesh", "adaptivity": "NaN"}, {"operation": "remesh", "unknown": "1"}, {"operation": "print_remesh", "mode": "quad"}} {
			request.Params = invalid
			_, err := remeshParameters(request)
			Expect(err).To(HaveOccurred())
		}
		request.Params = map[string]string{"operation": "print_remesh", "detail_percent": "0.5"}
		params, err = remeshParameters(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(params["mode"]).To(Equal("wrap"))
	})
	It("rejects layouts that would silently lose transforms, textures or skin weights", func() {
		for _, mutate := range []func(map[string]any){
			func(d map[string]any) { d["textures"] = []any{map[string]any{"source": 0}} },
			func(d map[string]any) { d["skins"] = []any{map[string]any{}} },
			func(d map[string]any) { d["nodes"] = []any{map[string]any{"mesh": 0, "translation": []int{1, 0, 0}}} },
			func(d map[string]any) {
				d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["mode"] = 1
			},
		} {
			_, err := parseVertexGLB(mutateGLB(tinyVertexGLB(), mutate))
			Expect(err).To(HaveOccurred())
		}
	})
	It("records quality statistics while preserving the binary payload", func() {
		src := tinyVertexGLB()
		out, err := withRemeshStats(src, `{"boundary_edges":3,"mode":"quad"}`)
		Expect(err).NotTo(HaveOccurred())
		size := int(binary.LittleEndian.Uint32(out[12:]))
		var doc map[string]any
		Expect(json.Unmarshal(out[20:20+size], &doc)).To(Succeed())
		Expect(doc["extras"].(map[string]any)["localai_remesh"].(map[string]any)["boundary_edges"]).To(Equal(float64(3)))
		Expect(out[20+size:]).To(Equal(src[20+int(binary.LittleEndian.Uint32(src[12:])):]))
	})
	It("refuses to silently discard emissive materials", func() {
		mesh := mutateGLB(tinyVertexGLB(), func(d map[string]any) {
			d["materials"] = []any{map[string]any{"emissiveFactor": []float64{1, 0, 0}}}
			d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["material"] = 0
		})
		_, err := parseVertexGLB(mesh)
		Expect(err).To(MatchError(ContainSubstring("emissive materials")))
	})
})
