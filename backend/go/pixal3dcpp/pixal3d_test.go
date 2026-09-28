package main

import (
	"bytes"
	"fmt"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestPixal3D(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Pixal3D backend") }

var _ = Describe("Pixal3D backend", func() {
	It("rejects missing or TRELLIS-only weights", func() {
		p := &Pixal3D{}
		Expect(p.Load(&pb.ModelOptions{ModelFile: GinkgoT().TempDir()})).NotTo(Succeed())
	})
	It("uses the canonical multiview CLI without shell interpretation", func() {
		req := &pb.Generate3DRequest{Images: []string{"a", "b", "c", "d"}, MeshScale: 0.8, Dst: "/tmp/out; touch bad.glb"}
		args, err := commandArgs("/models", "/views", req.Dst, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(args).To(Equal([]string{"--views", "/views", "--models", "/models", "--res", "1024", "--mesh-scale", "0.8", "--output", req.Dst}))
		req.Src = "single.png"
		_, err = commandArgs("/models", "/views", req.Dst, req)
		Expect(err).To(HaveOccurred())
	})
	It("requires every MV model component", func() {
		dir := GinkgoT().TempDir()
		for _, name := range modelFiles {
			Expect(os.WriteFile(filepath.Join(dir, name), []byte("GGUF"), 0600)).To(Succeed())
		}
		Expect(validateModels(dir)).NotTo(Succeed())
		manifest := `{"schema_version":1,"model_family":"mv","files":[`
		for i, name := range modelFiles {
			if i > 0 {
				manifest += ","
			}
			manifest += fmt.Sprintf(`{"name":%q,"size_bytes":4}`, name)
		}
		manifest += "]}"
		Expect(os.WriteFile(filepath.Join(dir, "pixal3d-models.json"), []byte(manifest), 0600)).To(Succeed())
		Expect(validateModels(dir)).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "pixal3d_naf.gguf"))).To(Succeed())
		Expect(validateModels(dir)).NotTo(Succeed())
	})
})

var _ = Describe("Pixal3D native process", func() {
	DescribeTable("checks the process status and GLB output, then removes all staged files", func(script string, wantError bool) {
		dir := GinkgoT().TempDir()
		var data bytes.Buffer
		Expect(png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2)))).To(Succeed())
		views := []string{}
		for i := range 4 {
			path := filepath.Join(dir, fmt.Sprintf("view-%d.png", i))
			Expect(os.WriteFile(path, data.Bytes(), 0600)).To(Succeed())
			views = append(views, path)
		}
		cli := filepath.Join(dir, "trellis-cli")
		// Native generation emits sidecars even if the process later fails.
		script = `#!/bin/sh
printf '%s' "$2" > "$PIXAL_CAPTURE"
for arg do dst=$arg; done
printf '%s' "$dst" > "$PIXAL_CAPTURE.output"
printf texture > "${dst%.glb}_base.png"
printf mesh > "${dst%.glb}.ply"
` + script
		GinkgoT().Setenv("PIXAL_CAPTURE", filepath.Join(dir, "capture"))
		Expect(os.WriteFile(cli, []byte(script), 0700)).To(Succeed())
		p := &Pixal3D{models: dir, cli: cli}
		destination := filepath.Join(dir, "destination")
		Expect(os.Mkdir(destination, 0700)).To(Succeed())
		err := p.Generate3D(&pb.Generate3DRequest{Images: views, MeshScale: 1, Dst: filepath.Join(destination, "out.glb")})
		Expect(err != nil).To(Equal(wantError))
		staged, readErr := os.ReadFile(filepath.Join(dir, "capture"))
		Expect(readErr).NotTo(HaveOccurred())
		_, statErr := os.Stat(string(staged))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		entries, readErr := os.ReadDir(destination)
		Expect(readErr).NotTo(HaveOccurred())
		if wantError {
			Expect(entries).To(BeEmpty())
		} else {
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Name()).To(Equal("out.glb"))
			output, readErr := os.ReadFile(filepath.Join(destination, "out.glb"))
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(output)).To(Equal("glTFpayload"))
		}
		stagedOutput, readErr := os.ReadFile(filepath.Join(dir, "capture.output"))
		Expect(readErr).NotTo(HaveOccurred())
		_, statErr = os.Stat(filepath.Dir(string(stagedOutput)))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		Expect(p.Free()).To(Succeed())
		Expect(p.models).To(BeEmpty())
	},
		Entry("valid output", "test -f \"$2/0_front.png\" && test -f \"$2/3_left.png\" || exit 9\nprintf glTFpayload > \"$dst\"\n", false),
		Entry("process failure", "printf glTFpartial > \"$dst\"\nexit 7\n", true),
		Entry("bad output", "for arg do dst=$arg; done\nprintf nope > \"$dst\"\n", true),
	)
})
