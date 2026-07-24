package backend

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Go-side embedding pooling schemes. The canonical strings live on the
// config package (config.Pooling* — mirroring the ScoreNormalization*
// pattern) so ModelConfig.Validate can reject unknown values without
// importing this package; these aliases are the API the backend layer
// (and the HTTP endpoints) program against.
const (
	// PoolingBackend leaves pooling to the inference backend — the exact
	// pre-existing behavior of the embeddings path (also selected by "").
	PoolingBackend = config.PoolingBackend
	// PoolingMean averages the per-token vectors.
	PoolingMean = config.PoolingMean
	// PoolingLast selects the last token's vector.
	PoolingLast = config.PoolingLast
	// PoolingDecayedMean is a mean weighted toward the most recent tokens:
	// w_i = 2^(-(T-1-i)/H) for token i of T, with half-life H tokens.
	PoolingDecayedMean = config.PoolingDecayedMean

	// DefaultPoolingHalfLifeTokens is the half-life used by
	// PoolingDecayedMean when the model config / request doesn't set one.
	DefaultPoolingHalfLifeTokens = 256
)

// reshapeEmbeddings views the flat float payload of an EmbeddingResult as
// tokens rows of dim columns. The gRPC contract packs vectors row-major
// (vector 0 first), so row i aliases flat[i*dim : (i+1)*dim].
func reshapeEmbeddings(flat []float32, tokens, dim int) ([][]float32, error) {
	if tokens <= 0 || dim <= 0 {
		return nil, fmt.Errorf("invalid embedding shape: %d vectors x %d dims", tokens, dim)
	}
	if len(flat) != tokens*dim {
		return nil, fmt.Errorf("embedding payload of %d floats does not match reported shape %d vectors x %d dims", len(flat), tokens, dim)
	}
	vecs := make([][]float32, tokens)
	for i := range vecs {
		vecs[i] = flat[i*dim : (i+1)*dim]
	}
	return vecs, nil
}

// poolMean averages the per-token vectors with float64 accumulators.
func poolMean(vecs [][]float32) []float32 {
	dim := len(vecs[0])
	acc := make([]float64, dim)
	for _, v := range vecs {
		for j, x := range v {
			acc[j] += float64(x)
		}
	}
	out := make([]float32, dim)
	for j := range out {
		out[j] = float32(acc[j] / float64(len(vecs)))
	}
	return out
}

// poolLast returns (a copy of) the last token's vector.
func poolLast(vecs [][]float32) []float32 {
	last := vecs[len(vecs)-1]
	out := make([]float32, len(last))
	copy(out, last)
	return out
}

// poolDecayedMean computes a weighted mean over the per-token vectors with
// exponentially decaying weights anchored at the last token: token i of T
// gets w_i = 2^(-(T-1-i)/halfLife), so the last token always weighs 1 and a
// token halfLife positions earlier weighs 0.5. Accumulation is in float64.
func poolDecayedMean(vecs [][]float32, halfLife float64) []float32 {
	dim := len(vecs[0])
	T := len(vecs)
	acc := make([]float64, dim)
	wsum := 0.0
	for i, v := range vecs {
		w := math.Exp2(-float64(T-1-i) / halfLife)
		wsum += w
		for j, x := range v {
			acc[j] += w * float64(x)
		}
	}
	out := make([]float32, dim)
	for j := range out {
		out[j] = float32(acc[j] / wsum)
	}
	return out
}

// normalizeEmbedding is an exact port of llama.cpp's common_embd_normalize
// (backend/cpp/llama-cpp/llama.cpp/common/common.cpp), applied after Go-side
// pooling because per-token vectors arrive RAW from llama.cpp with
// pooling:none (the server only normalizes vectors it pooled itself).
// embdNorm: <0 none, 0 max-abs scaled to int16 range (/32760.0), 2 L2
// (llama.cpp default), anything else p-norm with p=embdNorm (1 = taxicab).
func normalizeEmbedding(v []float32, embdNorm int) []float32 {
	sum := 0.0
	switch {
	case embdNorm < 0: // no normalisation
		sum = 1.0
	case embdNorm == 0: // max absolute
		for _, x := range v {
			if a := math.Abs(float64(x)); sum < a {
				sum = a
			}
		}
		sum /= 32760.0 // make an int16 range
	case embdNorm == 2: // euclidean
		for _, x := range v {
			sum += float64(x) * float64(x)
		}
		sum = math.Sqrt(sum)
	default: // p-norm (euclidean is p-norm p=2)
		for _, x := range v {
			sum += math.Pow(math.Abs(float64(x)), float64(embdNorm))
		}
		sum = math.Pow(sum, 1.0/float64(embdNorm))
	}

	// llama.cpp computes the reciprocal as a float32 and multiplies in
	// float32; mirror that so both paths yield bit-identical vectors.
	var norm float32
	if sum > 0.0 {
		norm = float32(1.0 / sum)
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * norm
	}
	return out
}

// embdNormalizeFromOptions extracts the load-time embd_normalize backend
// option ("embd_normalize:<n>", alias "embedding_normalize:<n>") the same
// way the llama-cpp gRPC server parses it, so Go-side pooling normalizes
// with the exact norm the backend would have applied had it pooled
// server-side. Defaults to 2 (L2) like llama.cpp; unparsable values are
// ignored (llama.cpp swallows std::stoi failures).
func embdNormalizeFromOptions(options []string) int {
	embdNorm := 2
	for _, opt := range options {
		name, val, found := strings.Cut(opt, ":")
		if !found || (name != "embd_normalize" && name != "embedding_normalize") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			embdNorm = n
		}
	}
	return embdNorm
}

// PoolEmbeddingResult reduces a per-token EmbeddingResult (the backend ran
// with pooling:none) to a single vector using scheme, then normalizes it
// with llama.cpp's common_embd_normalize semantics. halfLife only applies
// to PoolingDecayedMean; non-positive values fall back to
// DefaultPoolingHalfLifeTokens.
func PoolEmbeddingResult(res *proto.EmbeddingResult, scheme string, halfLife float64, embdNorm int) ([]float32, error) {
	vecs, err := reshapeEmbeddings(res.GetEmbeddings(), int(res.GetTokens()), int(res.GetDim()))
	if err != nil {
		return nil, err
	}
	var pooled []float32
	switch scheme {
	case PoolingMean:
		pooled = poolMean(vecs)
	case PoolingLast:
		pooled = poolLast(vecs)
	case PoolingDecayedMean:
		if halfLife <= 0 {
			halfLife = DefaultPoolingHalfLifeTokens
		}
		pooled = poolDecayedMean(vecs, halfLife)
	default:
		return nil, fmt.Errorf("unknown Go-side pooling scheme %q (expected %q, %q or %q)", scheme, PoolingMean, PoolingLast, PoolingDecayedMean)
	}
	return normalizeEmbedding(pooled, embdNorm), nil
}
