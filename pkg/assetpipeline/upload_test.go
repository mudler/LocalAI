// SPDX-License-Identifier: MIT
package assetpipeline_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/png"
	"io"
	"time"

	"github.com/google/uuid"
	. "github.com/mudler/LocalAI/pkg/assetpipeline"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Durable image uploads", func() {
	It("accepts self-contained GLB uploads and rejects external resources and malformed chunks", func() {
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		ctx := context.Background()
		_, err = s.Update(ctx, "resource", "create", 0, "create", func(w *Workspace) error { *w = New("asset", DefaultGraph(), time.Now()); return nil })
		Expect(err).NotTo(HaveOccurred())

		glb := func(text string) []byte {
			for len(text)%4 != 0 {
				text += " "
			}

			body := make([]byte, 20+len(text))
			copy(body, "glTF")
			binary.LittleEndian.PutUint32(body[4:], 2)
			binary.LittleEndian.PutUint32(body[8:], uint32(len(body)))
			binary.LittleEndian.PutUint32(body[12:], uint32(len(text)))
			binary.LittleEndian.PutUint32(body[16:], 0x4e4f534a)
			copy(body[20:], text)
			return body
		}

		valid := glb(`{"asset":{"version":"2.0"},"nodes":[]}`)
		a, err := s.Upload(ctx, "resource", uuid.NewString(), "motion.glb", "model/gltf-binary", 0, valid, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.ID).NotTo(BeEmpty())

		truncated := append([]byte{}, valid...)
		binary.LittleEndian.PutUint32(truncated[12:], 0xfffffff0)
		for _, invalid := range [][]byte{
			valid[:len(valid)-1], truncated, []byte("not a glb"),
			glb(`{"asset":{"version":"1.0"}}`),
			glb(`{"asset":{"version":"2.0"},"buffers":[{"uri":"/private/model.bin"}]}`),
			glb(`{"asset":{"version":"2.0"},"images":[{"uri":"https://example.com/texture.png"}]}`),
		} {
			_, err := s.Upload(ctx, "resource", uuid.NewString(), "unsafe.glb", "model/gltf-binary", 0, invalid, true)
			Expect(err).To(HaveOccurred())
		}
	})

	It("resumes chunks, deduplicates finalization and isolates artifact resources", func() {
		ctx := context.Background()
		dir := GinkgoT().TempDir()
		s, err := Open(dir)
		Expect(err).NotTo(HaveOccurred())

		_, err = s.Update(ctx, "resource", "create", 0, "create", func(w *Workspace) error { *w = New("asset", DefaultGraph(), time.Now()); return nil })
		Expect(err).NotTo(HaveOccurred())

		var b bytes.Buffer
		Expect(png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 8, 8)))).To(Succeed())

		data, id := b.Bytes(), uuid.NewString()
		split := len(data) / 2
		_, err = s.Upload(ctx, "resource", id, "asset.png", "image/png", 0, data[:split], false)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())

		s, err = Open(dir)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		_, err = s.Upload(ctx, "resource", id, "asset.png", "image/png", 0, data[:split], false)
		Expect(err).NotTo(HaveOccurred())

		_, err = s.Upload(ctx, "resource", id, "asset.png", "image/png", 0, []byte("different"), false)
		Expect(err).To(MatchError(ErrConflict))

		a, err := s.Upload(ctx, "resource", id, "asset.png", "image/png", split, data[split:], true)
		Expect(err).NotTo(HaveOccurred())

		again, err := s.Upload(ctx, "resource", id, "asset.png", "image/png", split, data[split:], true)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(a))

		_, err = s.Artifact(ctx, "another-resource", a.ID)
		Expect(err).To(MatchError(ErrNotFound))

		file, _, err := s.OpenArtifact(ctx, "resource", a.ID)
		Expect(err).NotTo(HaveOccurred())

		defer func() { _ = file.Close() }()
		stored, err := io.ReadAll(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(Equal(data))
	})

	It("rejects invalid formats, missing resources and oversized chunks", func() {
		s, err := Open(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(s.Close)
		ctx := context.Background()
		_, err = s.Upload(ctx, "missing", uuid.NewString(), "a.png", "image/png", 0, []byte("invalid"), true)
		Expect(err).To(MatchError(ErrNotFound))

		_, err = s.Upload(ctx, "missing", uuid.NewString(), "a.svg", "image/svg+xml", 0, nil, true)
		Expect(err).To(HaveOccurred())

		_, err = s.Upload(ctx, "missing", uuid.NewString(), "a.png", "image/png", 0, make([]byte, 513<<10), false)
		Expect(err).To(HaveOccurred())
	})
})
