package vram_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"time"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/pkg/vram"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DefaultGGUFReader", func() {
	It("reads architecture scalars from a valid remote GGUF", func() {
		server := serveGGUF(validRemoteGGUF())

		meta, err := vram.DefaultGGUFReader().ReadMetadata(context.Background(), server.URL+"/model.gguf")

		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(Equal(&vram.GGUFMeta{
			BlockCount:           32,
			EmbeddingLength:      4096,
			HeadCount:            32,
			HeadCountKV:          8,
			MaximumContextLength: 8192,
		}))
	})

	It("rejects an overflowing tokenizer array without allocating it", func() {
		server := serveGGUF(malformedGGUFArray(math.MaxUint64))

		_, err := vram.DefaultGGUFReader().ReadMetadata(context.Background(), server.URL+"/model.gguf")

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("parser panic"),
			"large tokenizer metadata should be skipped with a bounds error")
	})

	It("converts a parser panic from malformed string metadata to an error", func() {
		server := serveGGUF(malformedGGUFString(uint64(math.MaxInt64)))

		_, err := vram.DefaultGGUFReader().ReadMetadata(context.Background(), server.URL+"/model.gguf")

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parser panic"))
	})
})

func serveGGUF(payload []byte) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "model.gguf", time.Time{}, bytes.NewReader(payload))
	}))
	DeferCleanup(server.Close)
	return server
}

func malformedGGUFString(length uint64) []byte {
	payload := ggufHeader(1)
	payload = appendGGUFString(payload, "general.name")
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMetadataValueTypeString))
	payload = binary.LittleEndian.AppendUint64(payload, length)
	return payload
}

func validRemoteGGUF() []byte {
	payload := ggufHeader(6)
	payload = appendGGUFStringValue(payload, "general.architecture", "llama")
	payload = appendGGUFUint32(payload, "llama.block_count", 32)
	payload = appendGGUFUint32(payload, "llama.embedding_length", 4096)
	payload = appendGGUFUint32(payload, "llama.attention.head_count", 32)
	payload = appendGGUFUint32(payload, "llama.attention.head_count_kv", 8)
	payload = appendGGUFUint32(payload, "llama.context_length", 8192)
	return payload
}

func malformedGGUFArray(itemLength uint64) []byte {
	payload := ggufHeader(1)
	payload = appendGGUFString(payload, "tokenizer.ggml.tokens")
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMetadataValueTypeArray))
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMetadataValueTypeString))
	payload = binary.LittleEndian.AppendUint64(payload, 1)
	payload = binary.LittleEndian.AppendUint64(payload, itemLength)
	return payload
}

func ggufHeader(metadataCount uint64) []byte {
	payload := make([]byte, 0, 128)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMagicGGUFLe))
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFVersionV3))
	payload = binary.LittleEndian.AppendUint64(payload, 0)
	payload = binary.LittleEndian.AppendUint64(payload, metadataCount)
	return payload
}

func appendGGUFString(payload []byte, value string) []byte {
	payload = binary.LittleEndian.AppendUint64(payload, uint64(len(value)))
	return append(payload, value...)
}

func appendGGUFStringValue(payload []byte, key, value string) []byte {
	payload = appendGGUFString(payload, key)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMetadataValueTypeString))
	return appendGGUFString(payload, value)
}

func appendGGUFUint32(payload []byte, key string, value uint32) []byte {
	payload = appendGGUFString(payload, key)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(gguf.GGUFMetadataValueTypeUint32))
	return binary.LittleEndian.AppendUint32(payload, value)
}
