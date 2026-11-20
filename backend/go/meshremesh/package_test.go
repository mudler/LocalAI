// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func tetrahedronGLB() []byte {
	positions := []float32{1, 1, 1, -1, -1, 1, -1, 1, -1, 1, -1, -1}
	indices := []uint32{0, 2, 1, 0, 1, 3, 0, 3, 2, 1, 2, 3}
	bin := make([]byte, 96)
	for i, v := range positions {
		binary.LittleEndian.PutUint32(bin[4*i:], math.Float32bits(v))
	}
	for i, v := range indices {
		binary.LittleEndian.PutUint32(bin[48+4*i:], v)
	}
	doc := []byte(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"mesh":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0},"indices":1,"material":0}]}],"materials":[{"pbrMetallicRoughness":{"baseColorFactor":[1,0,0,1],"metallicFactor":0,"roughnessFactor":0.5}}],"accessors":[{"bufferView":0,"componentType":5126,"count":4,"type":"VEC3","min":[-1,-1,-1],"max":[1,1,1]},{"bufferView":1,"componentType":5125,"count":12,"type":"SCALAR"}],"bufferViews":[{"buffer":0,"byteLength":48},{"buffer":0,"byteOffset":48,"byteLength":48}],"buffers":[{"byteLength":96}]}`)
	for len(doc)%4 != 0 {
		doc = append(doc, ' ')
	}
	out := make([]byte, 28+len(doc)+len(bin))
	for i, v := range []uint32{glbMagic, 2, uint32(len(out)), uint32(len(doc)), glbJSONChunk} {
		binary.LittleEndian.PutUint32(out[i*4:], v)
	}
	copy(out[20:], doc)
	binary.LittleEndian.PutUint32(out[20+len(doc):], uint32(len(bin)))
	binary.LittleEndian.PutUint32(out[24+len(doc):], glbBINChunk)
	copy(out[28+len(doc):], bin)
	return out
}

var _ = Describe("Packaged remesher", func() {
	It("loads and remeshes through gRPC using only the distributed package", func() {
		pkg := os.Getenv("MESHREMESH_PACKAGE")
		if pkg == "" {
			Skip("set MESHREMESH_PACKAGE to test the installable backend")
		}
		pkg, err := filepath.Abs(pkg)
		Expect(err).NotTo(HaveOccurred())
		notice, err := os.ReadFile(filepath.Join(pkg, "NOTICE"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(notice)).To(ContainSubstring("GPL-3.0-or-later"))
		dir := GinkgoT().TempDir()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		DeferCleanup(cancel)
		log, err := os.Create(filepath.Join(dir, "backend.log"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(log.Close()).To(Succeed())
			if CurrentSpecReport().Failed() {
				data, _ := os.ReadFile(log.Name())
				_, _ = GinkgoWriter.Write(data)
			}
		})
		command := exec.CommandContext(ctx, filepath.Join(pkg, "run.sh"), "--addr", addr)
		command.Stdout, command.Stderr = log, log
		Expect(command.Start()).To(Succeed())
		DeferCleanup(func() { cancel(); _ = command.Wait() })
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conn.Close)
		client := pb.NewBackendClient(conn)
		Eventually(func() error {
			probe, stop := context.WithTimeout(ctx, time.Second)
			defer stop()
			_, err := client.Health(probe, &pb.HealthMessage{})
			return err
		}, 20*time.Second, 100*time.Millisecond).Should(Succeed())
		loaded, err := client.LoadModel(ctx, &pb.ModelOptions{Model: "meshremesh", ModelFile: "/models/meshremesh"})
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded.Success).To(BeTrue(), loaded.Message)
		src := filepath.Join(dir, "source.glb")
		Expect(os.WriteFile(src, tetrahedronGLB(), 0600)).To(Succeed())
		for _, mode := range []string{"wrap", "quad", "wrap_quad"} {
			dst := filepath.Join(dir, mode+".glb")
			result, err := client.Generate3D(ctx, &pb.Generate3DRequest{Src: src, Dst: dst, Params: map[string]string{"operation": "remesh", "mode": mode, "alpha_ratio": "0.02", "target_quads": "100", "texture_size": "256"}})
			Expect(err).NotTo(HaveOccurred(), mode)
			Expect(result.Success).To(BeTrue(), mode+": "+result.Message)
			out, err := os.ReadFile(dst)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(out)).To(BeNumerically(">", 20))
			Expect(binary.LittleEndian.Uint32(out[8:])).To(Equal(uint32(len(out))))
			var doc struct {
				Extras struct {
					Stats map[string]any `json:"localai_remesh"`
				} `json:"extras"`
				Images []any `json:"images"`
			}
			Expect(json.Unmarshal(out[20:20+int(binary.LittleEndian.Uint32(out[12:]))], &doc)).To(Succeed())
			Expect(doc.Extras.Stats["mode"]).To(Equal(mode))
			Expect(doc.Extras.Stats["output_triangles"]).To(BeNumerically(">", 0))
			Expect(doc.Images).NotTo(BeEmpty())
		}
	})
})
