// SPDX-License-Identifier: MIT
package main

import (
	"encoding/binary"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func materialFixture(textured bool) *glbDocument {
	d := &glbDocument{root: glbObject{}, bin: make([]byte, 48)}
	for i := uint32(0); i < 3; i++ {
		binary.LittleEndian.PutUint32(d.bin[36+4*i:], i)
	}
	glbSet(d.root, "asset", map[string]string{"version": "2.0"})
	glbSet(d.root, "nodes", []any{map[string]int{"mesh": 0}})
	glbSet(d.root, "scene", 0)
	glbSet(d.root, "scenes", []any{map[string]any{"nodes": []int{0}}})
	views := []any{map[string]int{"buffer": 0, "byteOffset": 0, "byteLength": 36}, map[string]int{"buffer": 0, "byteOffset": 36, "byteLength": 12}}
	accessors := []any{map[string]any{"bufferView": 0, "componentType": 5126, "type": "VEC3", "count": 3}, map[string]any{"bufferView": 1, "componentType": 5125, "type": "SCALAR", "count": 3}}
	attrs := map[string]int{"POSITION": 0}
	if textured {
		views = append(views, map[string]int{"buffer": 0, "byteOffset": 48, "byteLength": 24}, map[string]int{"buffer": 0, "byteOffset": 72, "byteLength": 4})
		accessors = append(accessors, map[string]any{"bufferView": 2, "componentType": 5126, "type": "VEC2", "count": 3})
		attrs["TEXCOORD_0"] = 2
		d.bin = append(d.bin, make([]byte, 24)...)
		d.bin = append(d.bin, []byte("PNG!")...)
		glbSet(d.root, "images", []any{map[string]any{"bufferView": 3, "mimeType": "image/png"}})
		glbSet(d.root, "textures", []any{map[string]int{"source": 0, "sampler": 0}})
		glbSet(d.root, "samplers", []any{map[string]int{"wrapS": 33071, "wrapT": 33071}})
		glbSet(d.root, "materials", []any{map[string]any{"pbrMetallicRoughness": map[string]any{"baseColorTexture": map[string]int{"index": 0}, "metallicFactor": 0.7, "roughnessFactor": 0.2}, "doubleSided": true}})
	}
	glbSet(d.root, "bufferViews", views)
	glbSet(d.root, "accessors", accessors)
	glbSet(d.root, "meshes", []any{map[string]any{"primitives": []any{map[string]any{"attributes": attrs, "indices": 1, "mode": 4, "material": 0}}}})
	return d
}

var _ = Describe("Material-preserving rig output", func() {
	var source, output string
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		source = filepath.Join(dir, "source.glb")
		output = filepath.Join(dir, "rigged.glb")
	})
	It("retains exact PBR resources and UV bytes while keeping the generated rig", func() {
		src, dst := materialFixture(true), materialFixture(false)
		glbSet(dst.root, "skins", []any{map[string]any{"joints": []int{1}}})
		glbSet(dst.root, "animations", []any{map[string]string{"name": "motion"}})
		// Unreferenced old animation data must not be carried into another output.
		src.bin = append(src.bin, make([]byte, 4096)...)
		Expect(src.write(source)).To(Succeed())
		Expect(dst.write(output)).To(Succeed())
		Expect(preserveMaterials(source, output)).To(Succeed())
		result, err := readGLB(output)
		Expect(err).NotTo(HaveOccurred())
		for _, key := range []string{"materials", "textures", "samplers"} {
			Expect(result.root[key]).To(MatchJSON(string(src.root[key])))
		}
		Expect(result.root["skins"]).To(MatchJSON(string(dst.root["skins"])))
		Expect(result.root["animations"]).To(MatchJSON(string(dst.root["animations"])))
		Expect(result.bin).To(HaveLen(76))
		Expect(string(result.bin[72:76])).To(Equal("PNG!"))
		p, _, err := singlePrimitive(result)
		Expect(err).NotTo(HaveOccurred())
		attrs, err := glbField[map[string]int](p, "attributes")
		Expect(err).NotTo(HaveOccurred())
		Expect(attrs).To(HaveKey("TEXCOORD_0"))
		Expect(attrs["POSITION"]).To(Equal(0))
	})
	It("preserves vertex colours without multiplying the material factor twice", func() {
		src := materialFixture(true)
		p, meshes, err := singlePrimitive(src)
		Expect(err).NotTo(HaveOccurred())
		attrs, err := glbField[map[string]int](p, "attributes")
		Expect(err).NotTo(HaveOccurred())
		// A second UV set exercises deduplicated accessor/view copying.
		attrs["TEXCOORD_1"] = attrs["TEXCOORD_0"]
		views, err := glbField[[]glbObject](src.root, "bufferViews")
		Expect(err).NotTo(HaveOccurred())
		view := glbObject{}
		glbSet(view, "buffer", 0)
		glbSet(view, "byteOffset", len(src.bin))
		glbSet(view, "byteLength", 36)
		views = append(views, view)
		access, err := glbField[[]glbObject](src.root, "accessors")
		Expect(err).NotTo(HaveOccurred())
		colour := glbObject{}
		glbSet(colour, "bufferView", len(views)-1)
		glbSet(colour, "count", 3)
		glbSet(colour, "componentType", 5126)
		glbSet(colour, "type", "VEC3")
		attrs["COLOR_0"] = len(access)
		access = append(access, colour)
		src.bin = append(src.bin, make([]byte, 36)...)
		glbSet(src.root, "accessors", access)
		glbSet(src.root, "bufferViews", views)
		glbSet(p, "attributes", attrs)
		glbSet(meshes[0], "primitives", []glbObject{p})
		glbSet(src.root, "meshes", meshes)
		Expect(src.write(source)).To(Succeed())
		Expect(materialFixture(false).write(output)).To(Succeed())
		Expect(preserveMaterials(source, output)).To(Succeed())
		result, err := readGLB(output)
		Expect(err).NotTo(HaveOccurred())
		p, _, err = singlePrimitive(result)
		Expect(err).NotTo(HaveOccurred())
		attrs, err = glbField[map[string]int](p, "attributes")
		Expect(err).NotTo(HaveOccurred())
		Expect(attrs).To(HaveKey("COLOR_0"))
		Expect(attrs["TEXCOORD_1"]).To(Equal(attrs["TEXCOORD_0"]))
		Expect(result.root["materials"]).To(MatchJSON(string(src.root["materials"])))
	})
	It("rejects changed topology but accepts winding reversal from mirrored transforms", func() {
		Expect(materialFixture(true).write(source)).To(Succeed())
		dst := materialFixture(false)
		binary.LittleEndian.PutUint32(dst.bin[40:], 2)
		binary.LittleEndian.PutUint32(dst.bin[44:], 1)
		Expect(dst.write(output)).To(Succeed())
		Expect(preserveMaterials(source, output)).To(Succeed())
		binary.LittleEndian.PutUint32(dst.bin[44:], 2)
		Expect(dst.write(output)).To(Succeed())
		Expect(preserveMaterials(source, output)).To(MatchError(ContainSubstring("topology")))
	})
	It("rejects unsupported or unsafe sources without modifying the generated file", func() {
		mutations := []func(*glbDocument){
			func(d *glbDocument) {
				glbSet(d.root, "images", []any{map[string]string{"uri": "file:///private/image.png"}})
			},
			func(d *glbDocument) {
				glbSet(d.root, "nodes", []any{map[string]int{"mesh": 0}, map[string]int{"mesh": 0}})
			},
			func(d *glbDocument) { glbSet(d.root, "extensions", map[string]any{"unknown": true}) },
			func(d *glbDocument) {
				views, _ := glbField[[]glbObject](d.root, "bufferViews")
				glbSet(views[3], "byteLength", 999)
				glbSet(d.root, "bufferViews", views)
			},
			func(d *glbDocument) {
				access, _ := glbField[[]glbObject](d.root, "accessors")
				glbSet(access[0], "count", 4)
				glbSet(d.root, "accessors", access)
			},
		}
		for _, mutate := range mutations {
			src := materialFixture(true)
			mutate(src)
			Expect(src.write(source)).To(Succeed())
			Expect(materialFixture(false).write(output)).To(Succeed())
			before, err := os.ReadFile(output)
			Expect(err).NotTo(HaveOccurred())
			Expect(preserveMaterials(source, output)).NotTo(Succeed())
			after, err := os.ReadFile(output)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(before))
		}
	})
	It("rejects truncated and non-GLB payloads", func() {
		for _, payload := range [][]byte{nil, []byte("not a glb"), make([]byte, 28)} {
			Expect(os.WriteFile(source, payload, 0600)).To(Succeed())
			_, err := readGLB(source)
			Expect(err).To(HaveOccurred())
		}
	})
})
