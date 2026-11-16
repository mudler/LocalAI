// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image/png"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRemesher(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Standalone mesh remesher") }

var _ = Describe("Native remeshing", func() {
	It("rejects mismatched buffers before calling native code", func() {
		_, _, err := remeshNative(nil, nil, nil, nil)
		Expect(err).To(HaveOccurred())
		_, _, err = remeshNative(make([]float32, 9), []int32{0, 1, 2}, []float32{1}, nil)
		Expect(err).To(HaveOccurred())
	})
	It("wraps and reprojects a coloured tetrahedron without any inference model", func() {
		library := os.Getenv("MESHREMESH_LIBRARY")
		if library == "" {
			Skip("set MESHREMESH_LIBRARY to test the native remesher")
		}
		Expect(loadNativeLibrary(library)).To(Succeed())
		vertices := []float32{1, 1, 1, -1, -1, 1, -1, 1, -1, 1, -1, -1}
		triangles := []int32{0, 2, 1, 0, 1, 3, 0, 3, 2, 1, 2, 3}
		pbr := []float32{1, 0, 0, 0, 0.5, 1, 1, 0, 0, 0, 0.5, 1, 1, 0, 0, 0, 0.5, 1, 1, 0, 0, 0, 0.5, 1}
		glb, stats, err := remeshNative(vertices, triangles, pbr, map[string]string{"mode": "wrap", "alpha_ratio": "0.02", "texture_size": "256"})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(glb[:4])).To(Equal("glTF"))
		Expect(binary.LittleEndian.Uint32(glb[8:])).To(Equal(uint32(len(glb))))
		jsonSize := int(binary.LittleEndian.Uint32(glb[12:]))
		var doc struct {
			Images []struct {
				BufferView int `json:"bufferView"`
			} `json:"images"`
			Views []struct {
				Offset int `json:"byteOffset"`
				Length int `json:"byteLength"`
			} `json:"bufferViews"`
		}
		Expect(json.Unmarshal(glb[20:20+jsonSize], &doc)).To(Succeed())
		Expect(doc.Images).NotTo(BeEmpty())
		view := doc.Views[doc.Images[0].BufferView]
		start := 28 + jsonSize + view.Offset
		texture, err := png.Decode(bytes.NewReader(glb[start : start+view.Length]))
		Expect(err).NotTo(HaveOccurred())
		redPixels := 0
		for y := 0; y < texture.Bounds().Dy(); y++ {
			for x := 0; x < texture.Bounds().Dx(); x++ {
				r, g, b, a := texture.At(x, y).RGBA()
				if a > 0 && r > 1000 && r > g*2 && r > b*2 {
					redPixels++
				}
			}
		}
		Expect(redPixels).To(BeNumerically(">", 100))
		Expect(json.Valid([]byte(stats))).To(BeTrue())
		Expect(stats).To(ContainSubstring(`"mode":"wrap"`))
		_, _, err = remeshNative(vertices, []int32{0, 1, 999}, nil, nil)
		Expect(err).To(MatchError(ContainSubstring("invalid triangle index")))
	})
})
