package main

// Valkey-backed vector store, exposed as a gRPC backend. Realizes
// option (2) of the vector-search design discussion (#1792) through
// the same pluggable seam as local-store: core selects it via the
// `backend` field on /stores/* requests, no proto or HTTP change.
//
// Compared to local-store this trades in-process speed for
// durability (Valkey RDB/AOF persistence) and a scaling path
// (opt-in HNSW). Vectors live in the Valkey Search module
// (`FT.*` on the valkey/valkey-bundle image).
//
// Data model: the vector IS the key in the store contract, so each
// entry is a HASH at `<prefix><hex of float32 LE bytes>` with fields
// `vec` (raw float32 bytes, the indexed VECTOR attribute) and `val`
// (opaque payload). Set is an upsert for free — same vector, same
// hash key. A `<prefix>meta` HASH records the index dimension so a
// restarted backend process can validate dimensions without parsing
// FT.INFO ("meta" is outside the hex alphabet, so it can never
// collide with an encoded vector).
//
// Search parity: the index defaults to FLAT with COSINE distance, so
// Find is an exact scan whose ranking matches local-store; Valkey
// returns cosine *distance* and similarity is derived as
// `1 - distance`. Set VALKEY_INDEX_ALGO=HNSW (or the valkey_index_algo
// model option) for approximate ANN on large corpora.
//
// Connection settings resolve per-model first, then fall back to the
// process-wide env vars: `valkey_addr` / VALKEY_ADDR, `valkey_index_algo`
// / VALKEY_INDEX_ALGO, and — mirroring cloud-proxy's api_key_env —
// `valkey_username_env` / `valkey_password_env` name the env vars that
// actually hold the credential, so distinct model configs can each
// point at a different Valkey server with its own credentials.
//
// Concurrency: base.SingleThread serialises gRPC calls, matching
// local-store; Valkey itself is the durable shared state.

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/valkey-io/valkey-go"
)

const (
	keyspacePrefix = "localai:store:"
	vecField       = "vec"
	valField       = "val"
	scoreAlias     = "__score"
)

type Store struct {
	base.SingleThread

	client valkey.Client

	// prefix namespaces every hash key and the index name, derived
	// from the store namespace core sends via opts.Model. Two
	// namespaces on the same Valkey server never see each other's
	// vectors because each index is created with PREFIX <prefix>.
	prefix string
	index  string

	// keyLen mirrors local-store: the dimension of every stored key,
	// -1 while unknown. It becomes known at index creation (first
	// Set) or at Load time from the meta hash of a pre-existing
	// index, so dimension mismatches fail loudly instead of
	// producing bogus cosine matches.
	keyLen int

	// indexAlgo is resolved once at Load (model option, else env var,
	// else FLAT) and reused by createIndex, which runs later from
	// StoresSet and has no access to the ModelOptions that carried it.
	indexAlgo string
}

func NewStore() *Store {
	return &Store{keyLen: -1}
}

// optString reads a model option (key:value form) from ModelOptions,
// returning def when absent. Mirrors optInt in sibling backends
// (e.g. moss-transcribe-cpp) for the model YAML's `options:` list.
func optString(opts *pb.ModelOptions, key, def string) string {
	for _, o := range opts.GetOptions() {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return def
}

// optEnvIndirect resolves a credential the way cloud-proxy's
// api_key_env does: the model option names an env var to read from,
// so distinct model configs can each reference a different credential
// pair without putting secrets in the YAML. Falls back to a single
// process-wide env var when the model doesn't set the option, keeping
// the single-Valkey-server default working unchanged.
func optEnvIndirect(opts *pb.ModelOptions, optKey, fallbackEnvVar string) string {
	if envVar := optString(opts, optKey, ""); envVar != "" {
		return os.Getenv(envVar)
	}
	return os.Getenv(fallbackEnvVar)
}

// Load validates the namespace, connects to Valkey and, when the
// namespace was already populated by a previous process, restores the
// index dimension from the meta hash. The namespace-prefix gate keeps
// the model loader's greedy autoload probing (real LLM names) from
// binding an LLM to the vector store — same contract as local-store.
func (s *Store) Load(opts *pb.ModelOptions) error {
	if !strings.HasPrefix(opts.GetModel(), store.NamespacePrefix) {
		return fmt.Errorf("valkey-store: refusing to load %q: not a store namespace (expected %q prefix)", opts.GetModel(), store.NamespacePrefix)
	}
	ns := strings.TrimPrefix(opts.GetModel(), store.NamespacePrefix)
	if ns == "" {
		ns = "default"
	}
	s.prefix = keyspacePrefix + ns + ":"
	s.index = s.prefix + "idx"

	addr := optString(opts, "valkey_addr", os.Getenv("VALKEY_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	s.indexAlgo = optString(opts, "valkey_index_algo", os.Getenv("VALKEY_INDEX_ALGO"))
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{addr},
		Username:    optEnvIndirect(opts, "valkey_username_env", "VALKEY_USERNAME"),
		Password:    optEnvIndirect(opts, "valkey_password_env", "VALKEY_PASSWORD"),
		// The store contract is request/response; the client-side
		// cache would only add invalidation traffic.
		DisableCache: true,
	})
	if err != nil {
		return fmt.Errorf("valkey-store: connect %s: %w", addr, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return fmt.Errorf("valkey-store: ping %s: %w", addr, err)
	}
	s.client = client

	dim, err := client.Do(ctx, client.B().Hget().Key(s.prefix+"meta").Field("dim").Build()).AsInt64()
	if err == nil && dim > 0 {
		s.keyLen = int(dim)
	}
	return nil
}

func (s *Store) StoresSet(opts *pb.StoresSetOptions) error {
	keys := store.UnwrapKeys(opts.Keys)
	values := store.UnwrapValues(opts.Values)
	if len(keys) == 0 {
		return fmt.Errorf("valkey-store: Set: no keys to add")
	}
	if len(keys) != len(values) {
		return fmt.Errorf("valkey-store: Set: len(keys) = %d, len(values) = %d", len(keys), len(values))
	}
	if s.keyLen == -1 {
		if err := s.createIndex(len(keys[0])); err != nil {
			return err
		}
	}
	for i, k := range keys {
		if len(k) != s.keyLen {
			return fmt.Errorf("valkey-store: Set: key %d length %d does not match existing %d", i, len(k), s.keyLen)
		}
	}

	ctx := context.Background()
	cmds := make(valkey.Commands, 0, len(keys))
	for i, k := range keys {
		vec := encodeVector(k)
		cmds = append(cmds, s.client.B().Hset().Key(s.prefix+hex.EncodeToString(vec)).
			FieldValue().
			FieldValue(vecField, valkey.BinaryString(vec)).
			FieldValue(valField, valkey.BinaryString(values[i])).
			Build())
	}
	for _, resp := range s.client.DoMulti(ctx, cmds...) {
		if err := resp.Error(); err != nil {
			return fmt.Errorf("valkey-store: Set: %w", err)
		}
	}
	return nil
}

// createIndex creates the vector index for this namespace with the
// given dimension and records the dimension in the meta hash. If the
// index already exists (created by another process after our Load
// checked), adopt its recorded dimension instead of failing the Set.
func (s *Store) createIndex(dim int) error {
	if dim == 0 {
		return fmt.Errorf("valkey-store: Set: cannot index zero-length vectors")
	}
	algo := strings.ToUpper(s.indexAlgo)
	if algo == "" {
		algo = "FLAT"
	}
	if algo != "FLAT" && algo != "HNSW" {
		return fmt.Errorf("valkey-store: valkey_index_algo/VALKEY_INDEX_ALGO %q not supported (FLAT or HNSW)", algo)
	}

	ctx := context.Background()
	err := s.client.Do(ctx, s.client.B().Arbitrary("FT.CREATE").Args(
		s.index, "ON", "HASH", "PREFIX", "1", s.prefix, "SCHEMA",
		vecField, "VECTOR", algo, "6",
		"TYPE", "FLOAT32",
		"DIM", strconv.Itoa(dim),
		"DISTANCE_METRIC", "COSINE",
	).Build()).Error()
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("valkey-store: FT.CREATE %s: %w", s.index, err)
		}
		existing, herr := s.client.Do(ctx, s.client.B().Hget().Key(s.prefix+"meta").Field("dim").Build()).AsInt64()
		if herr != nil || existing <= 0 {
			return fmt.Errorf("valkey-store: index %s exists but its dimension is unknown: %v", s.index, herr)
		}
		s.keyLen = int(existing)
		return nil
	}
	if err := s.client.Do(ctx, s.client.B().Hset().Key(s.prefix+"meta").
		FieldValue().FieldValue("dim", strconv.Itoa(dim)).Build()).Error(); err != nil {
		return fmt.Errorf("valkey-store: record index dimension: %w", err)
	}
	s.keyLen = dim
	return nil
}

func (s *Store) StoresDelete(opts *pb.StoresDeleteOptions) error {
	keys := store.UnwrapKeys(opts.Keys)
	if len(keys) == 0 {
		return fmt.Errorf("valkey-store: Delete: no keys to delete")
	}
	if s.keyLen != -1 {
		for i, k := range keys {
			if len(k) != s.keyLen {
				return fmt.Errorf("valkey-store: Delete: key %d length %d does not match existing %d", i, len(k), s.keyLen)
			}
		}
	}
	hashKeys := make([]string, len(keys))
	for i, k := range keys {
		hashKeys[i] = s.prefix + hex.EncodeToString(encodeVector(k))
	}
	if err := s.client.Do(context.Background(), s.client.B().Del().Key(hashKeys...).Build()).Error(); err != nil {
		return fmt.Errorf("valkey-store: Delete: %w", err)
	}
	return nil
}

// StoresGet fetches values for the given keys. Missing keys are
// omitted from the result rather than reported as an error — callers
// compare returned-key length against requested-key length to detect
// them. Returned slices are aligned.
func (s *Store) StoresGet(opts *pb.StoresGetOptions) (pb.StoresGetResult, error) {
	keys := store.UnwrapKeys(opts.Keys)
	if s.keyLen == -1 {
		return pb.StoresGetResult{}, nil
	}
	for i, k := range keys {
		if len(k) != s.keyLen {
			return pb.StoresGetResult{}, fmt.Errorf("valkey-store: Get: key %d length %d does not match existing %d", i, len(k), s.keyLen)
		}
	}

	cmds := make(valkey.Commands, 0, len(keys))
	for _, k := range keys {
		cmds = append(cmds, s.client.B().Hget().
			Key(s.prefix+hex.EncodeToString(encodeVector(k))).Field(valField).Build())
	}
	var foundKeys [][]float32
	var foundValues [][]byte
	for i, resp := range s.client.DoMulti(context.Background(), cmds...) {
		val, err := resp.AsBytes()
		if err != nil {
			if valkey.IsValkeyNil(err) {
				continue
			}
			return pb.StoresGetResult{}, fmt.Errorf("valkey-store: Get: %w", err)
		}
		foundKeys = append(foundKeys, keys[i])
		foundValues = append(foundValues, val)
	}
	return pb.StoresGetResult{
		Keys:   store.WrapKeys(foundKeys),
		Values: store.WrapValues(foundValues),
	}, nil
}

// StoresFind returns the topK nearest stored entries by cosine
// similarity, ordered most-similar first. An empty store returns
// empty slices and no error.
func (s *Store) StoresFind(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	query := opts.Key.Floats
	topK := int(opts.TopK)
	if topK < 1 {
		return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: topK = %d, must be >= 1", topK)
	}
	if s.keyLen == -1 {
		return pb.StoresFindResult{}, nil
	}
	if len(query) != s.keyLen {
		return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: query length %d does not match existing %d", len(query), s.keyLen)
	}

	// No SORTBY: Valkey Search rejects sorting on the KNN score alias
	// (it is not an indexed field); results are re-ordered client-side.
	resp := s.client.Do(context.Background(), s.client.B().Arbitrary("FT.SEARCH").Args(
		s.index,
		fmt.Sprintf("*=>[KNN %d @%s $query_vector AS %s]", topK, vecField, scoreAlias),
		"PARAMS", "2", "query_vector", valkey.BinaryString(encodeVector(query)),
		"RETURN", "2", scoreAlias, valField,
		"LIMIT", "0", strconv.Itoa(topK),
		"DIALECT", "2",
	).Build())
	rows, err := resp.ToArray()
	if err != nil {
		return pb.StoresFindResult{}, fmt.Errorf("valkey-store: FT.SEARCH %s: %w", s.index, err)
	}
	// Reply shape: [count, key1, [field, value, ...], key2, ...].
	var keys [][]float32
	var values [][]byte
	var sims []float32
	for i := 1; i+1 < len(rows); i += 2 {
		hashKey, err := rows[i].ToString()
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: parse key: %w", err)
		}
		key, err := decodeVectorKey(strings.TrimPrefix(hashKey, s.prefix))
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: %q: %w", hashKey, err)
		}
		fields, err := rows[i+1].AsStrMap()
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: parse fields of %q: %w", hashKey, err)
		}
		dist, err := strconv.ParseFloat(fields[scoreAlias], 32)
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: parse score of %q: %w", hashKey, err)
		}
		keys = append(keys, key)
		values = append(values, []byte(fields[valField]))
		sims = append(sims, 1-float32(dist))
	}
	order := make([]int, len(sims))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return sims[order[a]] > sims[order[b]] })
	sortedKeys := make([][]float32, len(order))
	sortedValues := make([][]byte, len(order))
	sortedSims := make([]float32, len(order))
	for i, j := range order {
		sortedKeys[i] = keys[j]
		sortedValues[i] = values[j]
		sortedSims[i] = sims[j]
	}
	keys, values, sims = sortedKeys, sortedValues, sortedSims
	return pb.StoresFindResult{
		Keys:         store.WrapKeys(keys),
		Values:       store.WrapValues(values),
		Similarities: sims,
	}, nil
}

// encodeVector serialises a vector as little-endian float32 bytes —
// the wire format Valkey Search expects for VECTOR fields and the
// basis of the hex hash-key encoding.
func encodeVector(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
	}
	return out
}

func decodeVectorKey(hexKey string) ([]float32, error) {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("not a vector hash key: %w", err)
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("not a vector hash key: %d bytes", len(raw))
	}
	v := make([]float32, len(raw)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return v, nil
}
