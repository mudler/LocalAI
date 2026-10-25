// SPDX-License-Identifier: MIT
package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Real-model multimodal embeddings and rerank through the registered routes and
// the real llama-cpp gRPC backend. It is opt-in: it needs model files and a
// built backend, so it never runs in the default suite (see MULTIMODAL.md).
//
//	MM_E2E_BACKEND            path of the llama-cpp grpc-server binary
//	MM_E2E_EMBED_MODEL        EmbeddingGemma 2 GGUF
//	MM_E2E_EMBED_PROJECTOR    its projector GGUF
//	MM_E2E_RERANK_MODEL       Qwen3-VL reranker GGUF   (rerank specs only)
//	MM_E2E_RERANK_PROJECTOR   its projector GGUF       (rerank specs only)
//	MM_E2E_FIXTURES           directory of the deterministic fixtures (default test/multimodal/fixtures)
//	MM_E2E_VECTORS_OUT        optional: write the raw vectors as JSON for the proof checker
//
// The rerank specs skip with a reason when their model files are absent.

type realEmbeddingFixture struct {
	dir string
}

func (f realEmbeddingFixture) bytes(name string) []byte {
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	Expect(err).NotTo(HaveOccurred(), name)
	return data
}

func (f realEmbeddingFixture) text(name string) string {
	return string(bytes.TrimRight(f.bytes(name), "\n"))
}

func (f realEmbeddingFixture) image(name string) map[string]any {
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(f.bytes(name))}}
}

func (f realEmbeddingFixture) audio(name string) map[string]any {
	return map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": base64.StdEncoding.EncodeToString(f.bytes(name)), "format": "wav"}}
}

func textPart(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func contentItem(parts ...map[string]any) map[string]any {
	list := make([]any, len(parts))
	for i, p := range parts {
		list[i] = p
	}
	return map[string]any{"content": list}
}

func requireEnvFile(name, reason string) string {
	path := os.Getenv(name)
	if path == "" {
		Skip(reason + " (set " + name + ")")
	}
	if _, err := os.Stat(path); err != nil {
		Skip(reason + ": " + name + " does not point at a file")
	}
	return path
}

func linkModel(dir, src string) string {
	name := filepath.Base(src)
	Expect(os.Symlink(src, filepath.Join(dir, name))).To(Succeed())
	return name
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

type embeddingsReply struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func embedAt(url string, input any) (int, embeddingsReply, []byte) {
	code, data := decisionPostAt(url, "/embeddings", map[string]any{"model": "embeddinggemma-2", "input": input})
	var out embeddingsReply
	if code == 200 {
		Expect(json.Unmarshal(data, &out)).To(Succeed(), string(data))
	}
	return code, out, data
}

func rerankAt(url string, body map[string]any) (int, []byte) {
	body["model"] = "qwen3-vl-reranker"
	return decisionPostAt(url, "/rerank", body)
}

type rerankReply struct {
	Results []struct {
		Index    int     `json:"index"`
		Score    float64 `json:"relevance_score"`
		Document struct {
			Text string `json:"text"`
		} `json:"document"`
	} `json:"results"`
}

var _ = Describe("Multimodal embeddings and rerank with the real llama.cpp backend", Label("MultimodalEmbeddingReal", "real-models"), Ordered, func() {
	var (
		url        string
		fx         realEmbeddingFixture
		haveEmbed  bool
		haveRerank bool
	)

	BeforeAll(func() {
		if os.Getenv("MM_E2E_REAL") != "1" {
			Skip("set MM_E2E_REAL=1 with MM_E2E_BACKEND and the model files (see the comment at the top of this file)")
		}
		binary := requireEnvFile("MM_E2E_BACKEND", "needs the built llama-cpp grpc-server")
		fx = realEmbeddingFixture{dir: os.Getenv("MM_E2E_FIXTURES")}
		if fx.dir == "" {
			fx.dir = filepath.Join("..", "..", "test", "multimodal", "fixtures")
		}
		dir := decisionIsolatedPath()
		present := func(model, projector string) bool {
			for _, name := range []string{model, projector} {
				if path := os.Getenv(name); path == "" {
					return false
				} else if _, err := os.Stat(path); err != nil {
					return false
				}
			}
			return true
		}
		if haveEmbed = present("MM_E2E_EMBED_MODEL", "MM_E2E_EMBED_PROJECTOR"); haveEmbed {
			model, projector := os.Getenv("MM_E2E_EMBED_MODEL"), os.Getenv("MM_E2E_EMBED_PROJECTOR")
			writeDecisionConfigAt(dir, map[string]any{
				"name":         "embeddinggemma-2",
				"backend":      "llama-cpp",
				"embeddings":   true,
				"mmproj":       linkModel(dir, projector),
				"context_size": 16384,
				"batch":        16384,
				"threads":      8,
				"gpu_layers":   0,
				"parameters":   map[string]any{"model": linkModel(dir, model)},
				"options":      []string{"parallel:1"},
			})
			// The same files used on the rerank route but loaded for pooled embeddings,
			// not as a ranker. Its backend publishes no rerank capability, which proves
			// that a request with media answers 501 and is never scored as text.
			writeDecisionConfigAt(dir, map[string]any{
				"name":           "not-a-reranker",
				"backend":        "llama-cpp",
				"embeddings":     true,
				"known_usecases": []string{"rerank"},
				"mmproj":         filepath.Base(projector),
				"context_size":   4096,
				"batch":          4096,
				"threads":        8,
				"gpu_layers":     0,
				"parameters":     map[string]any{"model": filepath.Base(model)},
				"options":        []string{"parallel:1"},
			})
		}
		if haveRerank = present("MM_E2E_RERANK_MODEL", "MM_E2E_RERANK_PROJECTOR"); haveRerank {
			model, projector := os.Getenv("MM_E2E_RERANK_MODEL"), os.Getenv("MM_E2E_RERANK_PROJECTOR")
			writeDecisionConfigAt(dir, map[string]any{
				"name":         "qwen3-vl-reranker",
				"backend":      "llama-cpp",
				"reranking":    true,
				"mmproj":       linkModel(dir, projector),
				"context_size": 8192,
				"batch":        8192,
				"threads":      8,
				"gpu_layers":   0,
				"parameters":   map[string]any{"model": linkModel(dir, model)},
				"options":      []string{"parallel:1"},
			})
		}
		_, url, _ = decisionIsolatedApp(dir, binary)
	})
	needEmbed := func() {
		if !haveEmbed {
			Skip("needs MM_E2E_EMBED_MODEL and MM_E2E_EMBED_PROJECTOR (EmbeddingGemma 2 GGUF and projector)")
		}
	}
	needRerank := func() {
		if !haveRerank {
			Skip("needs MM_E2E_RERANK_MODEL and MM_E2E_RERANK_PROJECTOR (Qwen3-VL reranker GGUF and projector)")
		}
	}

	It("keeps the old text-only request exactly as before", func() {
		needEmbed()
		code, legacy, body := embedAt(url, "A hand rings a brass bell.")
		Expect(code).To(Equal(200), string(body))
		Expect(legacy.Data).To(HaveLen(1))
		Expect(legacy.Data[0].Embedding).To(HaveLen(768))

		// A batch of strings, and the same text as structured parts, give the same vector.
		code, batch, body := embedAt(url, []string{"A hand rings a brass bell.", "A drum is struck."})
		Expect(code).To(Equal(200), string(body))
		Expect(batch.Data).To(HaveLen(2))
		Expect(cosine(batch.Data[0].Embedding, legacy.Data[0].Embedding)).To(BeNumerically(">", 0.9999))

		code, structured, body := embedAt(url, []any{contentItem(textPart("A hand rings a brass bell."))})
		Expect(code).To(Equal(200), string(body))
		Expect(structured.Data[0].Embedding).To(HaveLen(768))
		Expect(cosine(structured.Data[0].Embedding, legacy.Data[0].Embedding)).To(BeNumerically(">", 0.9999))
	})

	It("embeds text, an image and an audio clip as structured input with 768 dimensions", func() {
		needEmbed()
		code, out, body := embedAt(url, []any{
			"a bell",
			contentItem(fx.image("image-bell.png")),
			contentItem(fx.audio("audio-bell.wav")),
			contentItem(textPart("Find related evidence"), fx.image("image-bell.png"), fx.audio("audio-bell.wav")),
		})
		Expect(code).To(Equal(200), string(body))
		Expect(out.Data).To(HaveLen(4))
		for i, d := range out.Data {
			Expect(d.Index).To(Equal(i))
			Expect(d.Embedding).To(HaveLen(768))
			var norm float64
			for _, v := range d.Embedding {
				Expect(math.IsNaN(float64(v)) || math.IsInf(float64(v), 0)).To(BeFalse())
				norm += float64(v) * float64(v)
			}
			Expect(norm).To(BeNumerically(">", 0))
		}
	})

	It("changes the vector when the media changes and keeps it when the media is the same", func() {
		needEmbed()
		_, a, _ := embedAt(url, []any{contentItem(textPart("Find related evidence"), fx.image("image-bell.png"))})
		_, b, _ := embedAt(url, []any{contentItem(textPart("Find related evidence"), fx.image("image-drum.png"))})
		_, again, _ := embedAt(url, []any{contentItem(textPart("Find related evidence"), fx.image("image-bell.png"))})
		Expect(1-cosine(a.Data[0].Embedding, b.Data[0].Embedding)).To(BeNumerically(">", 0.001), "image change")
		Expect(cosine(a.Data[0].Embedding, again.Data[0].Embedding)).To(BeNumerically(">", 0.99999), "same input")

		_, c, _ := embedAt(url, []any{contentItem(fx.audio("audio-bell.wav"))})
		_, d, _ := embedAt(url, []any{contentItem(fx.audio("audio-drum.wav"))})
		Expect(1-cosine(c.Data[0].Embedding, d.Data[0].Embedding)).To(BeNumerically(">", 0.001), "audio change")
	})

	It("reports the error paths: video 501, malformed 400, wrong media 415", func() {
		needEmbed()
		video := map[string]any{"type": "input_video", "input_video": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"))}}
		code, _, body := embedAt(url, []any{contentItem(video)})
		Expect(code).To(Equal(501), string(body))

		code, _, body = embedAt(url, []any{contentItem()})
		Expect(code).To(Equal(400), string(body))

		// A PNG signature followed by nothing useful: the container header check refuses it.
		bad := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nxxxx"))}}
		code, _, body = embedAt(url, []any{contentItem(bad)})
		Expect(code).To(Equal(400), string(body))

		// A tiny PNG that claims a 60000 x 60000 image is refused from its header, without decoding it.
		ihdr := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR\x00\x00\xea\x60\x00\x00\xea\x60\x08\x02\x00\x00\x00\x00\x00\x00\x00")
		bomb := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(ihdr)}}
		code, _, body = embedAt(url, []any{contentItem(bomb)})
		Expect(code).To(Equal(413), string(body))
	})

	It("ranks image and text documents for a text query", func() {
		needRerank()
		query := fx.text("text-bell.txt")
		code, data := rerankAt(url, map[string]any{
			"query": query,
			"documents": []any{
				contentItem(fx.image("image-drum.png")),
				contentItem(fx.image("image-bell.png")),
				contentItem(fx.image("image-water.png")),
				fx.text("text-water.txt"),
			},
		})
		Expect(code).To(Equal(200), string(data))
		var out rerankReply
		Expect(json.Unmarshal(data, &out)).To(Succeed())
		Expect(out.Results).To(HaveLen(4))
		seen := map[int]bool{}
		for i, r := range out.Results {
			seen[r.Index] = true
			if i > 0 {
				Expect(r.Score).To(BeNumerically("<=", out.Results[i-1].Score), "sorted by score")
			}
		}
		Expect(seen).To(HaveLen(4))
		_, _ = fmt.Fprintf(GinkgoWriter, "RERANK %s\n", data)
		Expect(out.Results[0].Index).To(Equal(1), "the bell picture answers a query about a bell: %s", data)
		for _, r := range out.Results {
			if r.Index == 3 {
				Expect(r.Document.Text).To(Equal(fx.text("text-water.txt")), "a text document shows the text that was sent")
			} else {
				Expect(r.Document.Text).To(BeEmpty(), "an image document shows no text")
			}
		}
	})

	It("keeps the text-only rerank request working", func() {
		needRerank()
		code, data := rerankAt(url, map[string]any{
			"query":     fx.text("text-bell.txt"),
			"documents": []string{fx.text("text-water.txt"), fx.text("text-bell.txt")},
			"top_n":     1,
		})
		Expect(code).To(Equal(200), string(data))
		var out rerankReply
		Expect(json.Unmarshal(data, &out)).To(Succeed())
		Expect(out.Results).To(HaveLen(1))
		Expect(out.Results[0].Index).To(Equal(1))
		Expect(out.Results[0].Document.Text).To(Equal(fx.text("text-bell.txt")))
	})

	It("reports the rerank error paths: 400, 422 and 501", func() {
		needRerank()
		code, data := rerankAt(url, map[string]any{"query": "q", "documents": []any{map[string]any{"content": []any{}}}})
		Expect(code).To(Equal(400), string(data))
		code, data = rerankAt(url, map[string]any{"query": "q", "documents": []any{contentItem(fx.image("image-bell.png"), textPart("text after an image"))}})
		Expect(code).To(Equal(400), string(data))
		code, data = rerankAt(url, map[string]any{"query": "q", "documents": []any{contentItem(fx.audio("audio-bell.wav"))}})
		Expect(code).To(Equal(422), string(data))
		code, data = decisionPostAt(url, "/rerank", map[string]any{"model": "not-a-reranker", "query": "q", "documents": []any{contentItem(fx.image("image-bell.png"))}})
		Expect(code).To(Equal(501), string(data))
	})

	It("embeds every item of a frozen contract and writes the raw vectors for the proof checker", func() {
		needEmbed()
		out := os.Getenv("MM_E2E_VECTORS_OUT")
		contractPath := os.Getenv("MM_E2E_CONTRACT")
		if out == "" || contractPath == "" {
			Skip("set MM_E2E_CONTRACT and MM_E2E_VECTORS_OUT to score a frozen acceptance contract")
		}
		writeProofVectors(url, fx, contractPath, out)
	})
})

// proofVector is one raw float32 vector, bit-exact: the checker recomputes every
// similarity from these bytes and never trusts a reported number.
type proofVector struct {
	Dimensions int    `json:"dimensions"`
	Base64     string `json:"base64"`
	SHA256     string `json:"sha256"`
}

func newProofVector(v []float32) proofVector {
	raw := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(f))
	}
	sum := sha256.Sum256(raw)
	return proofVector{Dimensions: len(v), Base64: base64.StdEncoding.EncodeToString(raw), SHA256: hex.EncodeToString(sum[:])}
}

func fileSHA(path string) string {
	f, err := os.Open(path)
	Expect(err).NotTo(HaveOccurred(), path)
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	Expect(err).NotTo(HaveOccurred())
	return hex.EncodeToString(h.Sum(nil))
}

// writeProofVectors embeds, one item per request, every fixture and query text
// the contract names and every media-change pair. Items of a kind the backend
// does not publish (video) are listed as skipped, not scored.
func writeProofVectors(url string, fx realEmbeddingFixture, contractPath, out string) {
	raw, err := os.ReadFile(contractPath)
	Expect(err).NotTo(HaveOccurred())
	var contract struct {
		Version string `json:"contract_version"`
		Queries []struct {
			ID           string   `json:"id"`
			QueryText    *string  `json:"query_text"`
			QueryFixture *string  `json:"query_fixture"`
			Target       string   `json:"target"`
			Distractors  []string `json:"distractors"`
		} `json:"queries"`
		Pairs map[string]struct {
			Text  string `json:"surrounding_text"`
			Left  string `json:"left"`
			Right string `json:"right"`
		} `json:"pairs"`
	}
	Expect(json.Unmarshal(raw, &contract)).To(Succeed())

	find := func(id string) string {
		matches, err := filepath.Glob(filepath.Join(fx.dir, id+".*"))
		Expect(err).NotTo(HaveOccurred())
		Expect(matches).To(HaveLen(1), "fixture %s in %s", id, fx.dir)
		return filepath.Base(matches[0])
	}
	partOf := func(id string) map[string]any {
		file := find(id)
		switch {
		case strings.HasPrefix(id, "image-"):
			return fx.image(file)
		case strings.HasPrefix(id, "audio-"):
			return fx.audio(file)
		}
		Fail("unsupported fixture kind " + id)
		return nil
	}
	embedOne := func(input any) []float32 {
		code, reply, body := embedAt(url, input)
		Expect(code).To(Equal(200), string(body))
		Expect(reply.Data).To(HaveLen(1))
		return reply.Data[0].Embedding
	}

	vectors := map[string]proofVector{}
	skipped := []string{}
	fixtureIDs := map[string]bool{}
	for _, q := range contract.Queries {
		if q.QueryText != nil {
			vectors["query:"+q.ID] = newProofVector(embedOne(*q.QueryText))
		}
		ids := append([]string{q.Target}, q.Distractors...)
		if q.QueryFixture != nil {
			ids = append(ids, *q.QueryFixture)
		}
		for _, id := range ids {
			fixtureIDs[id] = true
		}
	}
	names := make([]string, 0, len(fixtureIDs))
	for id := range fixtureIDs {
		names = append(names, id)
	}
	sort.Strings(names)
	for _, id := range names {
		switch {
		case strings.HasPrefix(id, "text-"):
			vectors["fixture:"+id] = newProofVector(embedOne(fx.text(find(id))))
		case strings.HasPrefix(id, "image-"), strings.HasPrefix(id, "audio-"):
			vectors["fixture:"+id] = newProofVector(embedOne([]any{contentItem(partOf(id))}))
		default:
			skipped = append(skipped, id)
		}
	}
	pairNames := make([]string, 0, len(contract.Pairs))
	for name := range contract.Pairs {
		pairNames = append(pairNames, name)
	}
	sort.Strings(pairNames)
	for _, name := range pairNames {
		p := contract.Pairs[name]
		if strings.HasPrefix(p.Left, "video-") {
			skipped = append(skipped, "pair:"+name)
			continue
		}
		vectors["pair:"+name+":left"] = newProofVector(embedOne([]any{contentItem(textPart(p.Text), partOf(p.Left))}))
		vectors["pair:"+name+":right"] = newProofVector(embedOne([]any{contentItem(textPart(p.Text), partOf(p.Right))}))
	}
	sort.Strings(skipped)

	identity := map[string]string{
		"model_sha256":     fileSHA(os.Getenv("MM_E2E_EMBED_MODEL")),
		"projector_sha256": fileSHA(os.Getenv("MM_E2E_EMBED_PROJECTOR")),
		"backend_sha256":   fileSHA(os.Getenv("MM_E2E_BACKEND")),
		"llama_cpp":        os.Getenv("MM_E2E_LLAMA_REVISION"),
		"localai":          os.Getenv("MM_E2E_LOCALAI_REVISION"),
		"model_file":       filepath.Base(os.Getenv("MM_E2E_EMBED_MODEL")),
		"projector_file":   filepath.Base(os.Getenv("MM_E2E_EMBED_PROJECTOR")),
	}
	sum := sha256.Sum256(raw)
	doc := map[string]any{
		"kind":             "multimodal-adapter-vectors",
		"contract_version": contract.Version,
		"contract_sha256":  hex.EncodeToString(sum[:]),
		"identity":         identity,
		"request":          "POST /v1/embeddings, one input item per request, through the llama-cpp gRPC backend",
		"vectors":          vectors,
		"skipped":          skipped,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(out, append(data, '\n'), 0o644)).To(Succeed())
}
