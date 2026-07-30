package main

import (
	"encoding/binary"
	"fmt"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func tinyVertexGLB() []byte {
	bin := make([]byte, 80)
	positions := []float32{1, 2, 3, 4, 5, 6, 7, 8, 9}
	for i, value := range positions {
		binary.LittleEndian.PutUint32(bin[i*4:], math.Float32bits(value))
	}
	colors := []uint16{
		65535, 0, 0, 65535,
		0, 65535, 0, 32768,
		0, 0, 65535, 65535,
	}
	for i, value := range colors {
		binary.LittleEndian.PutUint16(bin[36+i*2:], value)
	}
	copy(bin[60:], []byte{0, 153, 64, 128, 255, 32})
	for i, value := range []uint32{0, 1, 2} {
		binary.LittleEndian.PutUint32(bin[68+i*4:], value)
	}

	jsonChunk := []byte(fmt.Sprintf(`{"asset":{"version":"2.0"},"meshes":[{"primitives":[{"attributes":{"POSITION":0,"COLOR_0":1,"_METALLIC_ROUGHNESS":2},"indices":3}]}],"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3"},{"bufferView":1,"componentType":5123,"normalized":true,"count":3,"type":"VEC4"},{"bufferView":2,"componentType":5121,"normalized":true,"count":3,"type":"VEC2"},{"bufferView":3,"componentType":5125,"count":3,"type":"SCALAR"}],"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":36},{"buffer":0,"byteOffset":36,"byteLength":24},{"buffer":0,"byteOffset":60,"byteLength":6},{"buffer":0,"byteOffset":68,"byteLength":12}],"buffers":[{"byteLength":%d}]}`, len(bin)))
	for len(jsonChunk)%4 != 0 {
		jsonChunk = append(jsonChunk, ' ')
	}
	total := 12 + 8 + len(jsonChunk) + 8 + len(bin)
	glb := make([]byte, total)
	binary.LittleEndian.PutUint32(glb[0:], glbMagic)
	binary.LittleEndian.PutUint32(glb[4:], 2)
	binary.LittleEndian.PutUint32(glb[8:], uint32(total))
	binary.LittleEndian.PutUint32(glb[12:], uint32(len(jsonChunk)))
	binary.LittleEndian.PutUint32(glb[16:], glbJSONChunk)
	copy(glb[20:], jsonChunk)
	binHeader := 20 + len(jsonChunk)
	binary.LittleEndian.PutUint32(glb[binHeader:], uint32(len(bin)))
	binary.LittleEndian.PutUint32(glb[binHeader+4:], glbBINChunk)
	copy(glb[binHeader+8:], bin)
	return glb
}

var _ = Describe("vertex GLB parsing for print remeshing", func() {
	It("restores trellis coordinates, topology, and PBR values", func() {
		mesh, err := parseVertexGLB(tinyVertexGLB())
		Expect(err).NotTo(HaveOccurred())
		Expect(mesh.verts).To(Equal([]float32{1, -3, 2, 4, -6, 5, 7, -9, 8}))
		Expect(mesh.tris).To(Equal([]int32{0, 1, 2}))
		Expect(mesh.pbr).To(HaveLen(18))
		Expect(mesh.pbr[0]).To(BeNumerically("~", 1, 1e-5))
		Expect(mesh.pbr[3]).To(BeNumerically("~", 0, 1e-5))
		Expect(mesh.pbr[4]).To(BeNumerically("~", 0.6, 0.01))
		Expect(mesh.pbr[11]).To(BeNumerically("~", 32768.0/65535.0, 1e-5))
	})

	It("rejects indices outside the source vertex buffer", func() {
		glb := tinyVertexGLB()
		binary.LittleEndian.PutUint32(glb[len(glb)-12:], 3)
		_, err := parseVertexGLB(glb)
		Expect(err).To(MatchError(ContainSubstring("outside the vertex buffer")))
	})

	It("rejects non-GLB input", func() {
		_, err := parseVertexGLB([]byte("not a mesh"))
		Expect(err).To(MatchError("input is not a GLB file"))
	})
})
