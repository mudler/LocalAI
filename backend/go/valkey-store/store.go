package main

// Valkey-backed vector store, exposed as a gRPC backend. It mirrors the public
// contract of backend/go/local-store (the four Stores* RPCs + Load) but swaps
// the in-memory sorted slices for Valkey Search (FT.*) so the data persists
// across restarts and can scale beyond an O(N) scan (opt-in HNSW).
//
// Data model — each entry is a Valkey HASH keyed by
//
//	prefix + hex(little-endian float32 bytes of the vector)
//
// with two fields: `vec` (the raw float32 bytes, indexed by a lazily-created
// FT VECTOR index of the discovered dimension) and `val` (the opaque value
// bytes). The vector-IS-the-key encoding (see encoding.go) makes Set an
// HSET upsert, Get an HGET, Delete a DEL, and Find an FT.SEARCH KNN.
//
// Similarity — Valkey returns cosine *distance* (0 = identical, 2 = opposite),
// while local-store returns cosine *similarity* (1 = identical, -1 = opposite).
// We convert sim = 1 - distance for COSINE so the values match local-store's
// integration expectations exactly. For L2/IP the raw score is passed through.
//
// Concurrency — base.SingleThread serialises gRPC calls, so the store's
// scalar bookkeeping (keyLen, indexCreated) needs no extra locking. All Valkey
// commands are synchronous via client.Do and bounded by an explicit
// per-request deadline (cfg.RequestTimeout); there is no background event loop.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/mudler/xlog"
	valkey "github.com/valkey-io/valkey-go"
)

const (
	// Hash field names. `vec` is the indexed vector; `val` is the opaque value.
	_vecField = "vec"
	_valField = "val"
	// _scoreField is the KNN distance alias produced by the query and returned
	// by FT.SEARCH. Double-underscore avoids colliding with a stored field.
	_scoreField = "__score"

	// _keyPrefixPrefix / _indexPrefix namespace the keys and index per model so
	// two namespaces (e.g. a 512-d face store and a 192-d voice store) sharing
	// one Valkey server never collide.
	_keyPrefixPrefix = "vs:"
	_indexPrefix     = "idx:"

	// _maxTopK bounds a Find so an accidental or abusive huge TopK cannot force
	// an unbounded server-side LIMIT / allocation. local-store has no cap, but
	// it is in-memory; a networked backend wants a guard. Callers asking for
	// more than this get the top _maxTopK results.
	_maxTopK = 10000
)

// ValkeyStore implements the gRPC store Backend against Valkey Search.
type ValkeyStore struct {
	base.SingleThread

	client valkey.Client
	cfg    Config

	// prefix is the per-namespace key prefix; indexName is the FT index name.
	prefix    string
	indexName string

	// keyLen is the vector dimension, learned from the first Set. -1 means
	// "no keys yet" — mirrors local-store so dimension-mismatch errors are
	// identical. indexCreated tracks whether FT.CREATE has run (lazy creation).
	keyLen       int
	indexCreated bool
}

// NewValkeyStore returns a store with an open dimension and no index yet. The
// Valkey client is established in Load once the connection config is known.
func NewValkeyStore() *ValkeyStore {
	return &ValkeyStore{keyLen: -1}
}

// newWithClient builds a store around an already-constructed client for a given
// namespace. It exists so unit tests can inject a mock client without a real
// Valkey server; Load is the production path.
func newWithClient(client valkey.Client, cfg Config, namespace string) *ValkeyStore {
	return &ValkeyStore{
		client:    client,
		cfg:       cfg,
		prefix:    keyPrefix(namespace),
		indexName: indexName(namespace),
		keyLen:    -1,
	}
}

// Load reads the VALKEY_* config, connects, and verifies the connection. The
// mandatory ClientName is always set so the connection is identifiable via
// CLIENT LIST. opts.Model is the namespace identifier (one process per
// (backend, model) tuple upstream), so we derive an isolated key prefix and
// index name from it.
func (s *ValkeyStore) Load(opts *pb.ModelOptions) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	s.cfg = cfg

	namespace := ""
	if opts != nil {
		namespace = opts.Model
	}
	s.prefix = keyPrefix(namespace)
	s.indexName = indexName(namespace)

	clientOpt := valkey.ClientOption{
		InitAddress: []string{cfg.Addr},
		Username:    cfg.Username,
		Password:    cfg.Password,
		ClientName:  cfg.ClientName,
		// Disable client-side caching: values are opaque blobs written once and
		// read rarely, so tracking invalidations would only add overhead.
		DisableCache: true,
	}
	if cfg.UseTLS {
		clientOpt.TLSConfig = &tls.Config{}
	}

	// Close any client from a previous Load so a re-entrant Load does not leak
	// the old connection. Not reachable in the one-process-per-namespace model
	// today, but keeps Load idempotent.
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}

	client, err := valkey.NewClient(clientOpt)
	if err != nil {
		return fmt.Errorf("valkey-store: connect to %s: %w", cfg.Addr, err)
	}
	s.client = client

	// Fail fast if the server is unreachable, mirroring how a real vector DB
	// backend would refuse to load against a dead endpoint.
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.client.Do(ctx, s.client.B().Ping().Build()).Error(); err != nil {
		s.client.Close()
		s.client = nil
		return fmt.Errorf("valkey-store: ping %s: %w", cfg.Addr, err)
	}

	// A durable Valkey may already hold this namespace's index from a previous
	// run (this is the persistence capability local-store lacks). Recover both
	// its existence AND its vector dimension so Find works before this fresh
	// process issues its first Set, and — critically — so a post-restart Set
	// validates the incoming dimension against the real persisted DIM instead
	// of silently re-learning a wrong one and dropping mismatched vectors from
	// the index (which would return success while making the entry unsearchable).
	s.loadIndexState(ctx)

	// Log the sanitized index name (which identifies the namespace) rather than
	// the raw model-derived namespace, which could carry control characters.
	xlog.Info("valkey-store loaded", "addr", cfg.Addr, "index", s.indexName, "algo", cfg.IndexAlgo, "metric", cfg.DistanceMetric, "indexExists", s.indexCreated, "keyLen", s.keyLen)
	return nil
}

// loadIndexState issues one FT.INFO at Load to recover the persisted index
// state. FT.INFO returns an error for an unknown index, so a successful reply
// means the index exists (indexCreated=true). We then recover the vector
// dimension from the reply and seed keyLen with it: without this, keyLen would
// stay -1 after a restart and the next Set would blindly re-learn whatever
// dimension the caller happened to send, accepting a mismatched vector that
// FT never indexes (silent search-side data loss). If the dimension can't be
// parsed (e.g. an unexpected FT.INFO layout on some server version), keyLen
// is left at -1 and validation degrades to the pre-restart lazy behaviour.
func (s *ValkeyStore) loadIndexState(ctx context.Context) {
	msg, err := s.client.Do(ctx, s.client.B().FtInfo().Index(s.indexName).Build()).ToMessage()
	if err != nil {
		return
	}
	s.indexCreated = true
	if dim, ok := findDimensions(msg); ok && dim > 0 {
		s.keyLen = dim
	}
}

// findDimensions walks an FT.INFO reply for the vector field's dimension. In
// Valkey Search the VECTOR attribute nests its parameters under an `index`
// array whose `dimensions` key holds the DIM the index was created with. The
// reply is a nested array (RESP2) or map (RESP3), so we search recursively for
// a `dimensions` key/token and read the value that follows it, tolerating both
// integer and string-encoded values.
func findDimensions(m valkey.ValkeyMessage) (int, bool) {
	if m.IsMap() {
		mp, err := m.AsMap()
		if err != nil {
			return 0, false
		}
		for k, v := range mp {
			if strings.EqualFold(k, "dimensions") {
				if n, ok := msgToInt(v); ok {
					return n, true
				}
			}
			if d, ok := findDimensions(v); ok {
				return d, true
			}
		}
		return 0, false
	}
	if m.IsArray() {
		arr, err := m.ToArray()
		if err != nil {
			return 0, false
		}
		for i := range arr {
			if s, err := arr[i].ToString(); err == nil && strings.EqualFold(s, "dimensions") && i+1 < len(arr) {
				if n, ok := msgToInt(arr[i+1]); ok {
					return n, true
				}
			}
			if d, ok := findDimensions(arr[i]); ok {
				return d, true
			}
		}
	}
	return 0, false
}

// msgToInt reads an integer from a ValkeyMessage that may be an integer reply
// or a string-encoded integer (FT.INFO mixes both across fields/versions).
func msgToInt(m valkey.ValkeyMessage) (int, bool) {
	if n, err := m.ToInt64(); err == nil {
		return int(n), true
	}
	if s, err := m.ToString(); err == nil {
		if n, err := strconv.Atoi(s); err == nil {
			return n, true
		}
	}
	return 0, false
}

// Free closes the Valkey client. Called by the gRPC server on shutdown.
func (s *ValkeyStore) Free() error {
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	return nil
}

// ctx returns a request-scoped context bounded by the configured timeout. We
// never rely on the client's built-in write timeout because index back-fill
// and large KNN queries can legitimately exceed a short default.
func (s *ValkeyStore) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.cfg.RequestTimeout)
}

func (s *ValkeyStore) StoresSet(opts *pb.StoresSetOptions) error {
	keys := store.UnwrapKeys(opts.Keys)
	values := store.UnwrapValues(opts.Values)
	if len(keys) == 0 {
		return fmt.Errorf("valkey-store: Set: no keys to add")
	}
	if len(keys) != len(values) {
		return fmt.Errorf("valkey-store: Set: len(keys) = %d, len(values) = %d", len(keys), len(values))
	}

	// Learn the dimension from the first key ever set (mirrors local-store's
	// keyLen == -1 sentinel), then reject anything that disagrees. checkDims is
	// the single source of truth for the per-key length check (shared with
	// Get/Delete/Find) so the four RPCs cannot drift apart.
	if s.keyLen == -1 {
		s.keyLen = len(keys[0])
	}
	if err := s.checkDims("Set", keys); err != nil {
		return err
	}

	// The index needs the dimension up front, but local-store learns it from
	// the first Set — so we create it lazily here, once, before writing.
	if err := s.ensureIndex(s.keyLen); err != nil {
		return err
	}

	// Write each entry with an individual round-trip rather than pipelining the
	// whole batch via DoMulti. Valkey Search indexes every HSET into the
	// FLAT/HNSW index synchronously on the server's main thread; a large
	// pipeline of indexed writes can fill the socket buffers while that
	// indexing keeps the server from draining them, deadlocking the connection
	// (observed as an i/o timeout on high-dimension batches — a single 768-d
	// DoMulti of ~20 vectors hangs, while the same writes issued sequentially
	// complete in milliseconds). Sequential writes keep each command fully
	// round-tripped and stay fast (hundreds of 768-d vectors in a few hundred
	// ms). A single failure fails the whole Set — partial writes are surfaced,
	// not swallowed.
	//
	// The request timeout is applied PER command, not once across the whole
	// loop: an unbounded SetCols against a remote Valkey would otherwise exhaust
	// a single aggregate deadline mid-batch and leave a partial, non-atomic
	// write.
	for i, k := range keys {
		cmd := s.client.B().Hset().Key(encodeKey(s.prefix, k)).
			FieldValue().
			FieldValue(_vecField, valkey.BinaryString(vecToBytes(k))).
			FieldValue(_valField, valkey.BinaryString(values[i])).
			Build()
		ctx, cancel := s.ctx()
		err := s.client.Do(ctx, cmd).Error()
		cancel()
		if err != nil {
			return fmt.Errorf("valkey-store: Set: HSET key %d: %w", i, err)
		}
	}
	return nil
}

// StoresGet fetches values for the given keys. Missing keys are omitted from
// the result (not errored), matching local-store; returned slices are aligned.
func (s *ValkeyStore) StoresGet(opts *pb.StoresGetOptions) (pb.StoresGetResult, error) {
	keys := store.UnwrapKeys(opts.Keys)
	if len(keys) == 0 {
		return pb.StoresGetResult{}, nil
	}
	if err := s.checkDims("Get", keys); err != nil {
		return pb.StoresGetResult{}, err
	}

	ctx, cancel := s.ctx()
	defer cancel()

	cmds := make([]valkey.Completed, len(keys))
	for i, k := range keys {
		cmds[i] = s.client.B().Hget().Key(encodeKey(s.prefix, k)).Field(_valField).Build()
	}

	var foundKeys [][]float32
	var foundValues [][]byte
	for i, res := range s.client.DoMulti(ctx, cmds...) {
		v, err := res.ToString()
		if err != nil {
			// A nil reply means the key/field is absent — omit it, don't error.
			if valkey.IsValkeyNil(err) {
				continue
			}
			return pb.StoresGetResult{}, fmt.Errorf("valkey-store: Get: HGET key %d: %w", i, err)
		}
		// The request vector is exact, so we return it verbatim as the key.
		foundKeys = append(foundKeys, keys[i])
		foundValues = append(foundValues, []byte(v))
	}

	return pb.StoresGetResult{
		Keys:   store.WrapKeys(foundKeys),
		Values: store.WrapValues(foundValues),
	}, nil
}

// StoresDelete removes entries by exact vector. Missing keys are tolerated
// (DEL returns 0), matching local-store.
func (s *ValkeyStore) StoresDelete(opts *pb.StoresDeleteOptions) error {
	keys := store.UnwrapKeys(opts.Keys)
	if len(keys) == 0 {
		return fmt.Errorf("valkey-store: Delete: no keys to delete")
	}
	if err := s.checkDims("Delete", keys); err != nil {
		return err
	}

	// Sequential DELs for the same reason StoresSet avoids DoMulti: a DEL of an
	// indexed key mutates the search index on the server's main thread, and a
	// large pipeline of such mutations can deadlock the connection. Missing
	// keys (DEL returns 0) are tolerated, matching local-store. As in Set, the
	// timeout is per command so a large DeleteCols cannot exhaust one aggregate
	// deadline mid-batch.
	for i, k := range keys {
		ctx, cancel := s.ctx()
		err := s.client.Do(ctx, s.client.B().Del().Key(encodeKey(s.prefix, k)).Build()).Error()
		cancel()
		if err != nil {
			return fmt.Errorf("valkey-store: Delete: DEL key %d: %w", i, err)
		}
	}
	return nil
}

// StoresFind returns the topK nearest entries by the configured distance
// metric, ordered most-similar first. An empty/uncreated index returns empty
// slices and no error, matching local-store's empty-store behaviour.
func (s *ValkeyStore) StoresFind(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	query := opts.Key.Floats
	topK := int(opts.TopK)
	if topK < 1 {
		return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: topK = %d, must be >= 1", topK)
	}
	if topK > _maxTopK {
		xlog.Warn("valkey-store: Find topK clamped", "requested", topK, "max", _maxTopK)
		topK = _maxTopK
	}
	// No index yet means nothing has been Set (and none was found at Load) —
	// an empty result, not an error.
	if !s.indexCreated {
		return pb.StoresFindResult{}, nil
	}
	// Enforce the query dimension against the known keyLen — recovered from
	// FT.INFO at Load after a restart, or learned from the first Set — so a
	// wrong-dimension query gets the clean local-store-style error. keyLen is
	// only -1 in the degraded case where FT.INFO gave no parseable dimension;
	// then we let Valkey's own FT.SEARCH validate the query vector.
	if s.keyLen != -1 && len(query) != s.keyLen {
		return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: query length %d does not match existing %d", len(query), s.keyLen)
	}

	ctx, cancel := s.ctx()
	defer cancel()

	// KNN pre-filter query: match everything, rank by vector distance into the
	// __score alias. A pure KNN query already returns its topK results ordered
	// by distance ascending (nearest-first), so we do NOT add SORTBY __score:
	// Valkey Search rejects sorting on the KNN score alias ("Index field
	// `__score` does not exist" — it is a query-time computed field, not a
	// SORTABLE schema attribute). LIMIT 0 topK caps the result and DIALECT 2 is
	// required for the =>[KNN ...] vector syntax. The __score field is still
	// returned in each document and read back for the similarity conversion.
	q := fmt.Sprintf("*=>[KNN %d @%s $q AS %s]", topK, _vecField, _scoreField)
	cmd := s.client.B().FtSearch().Index(s.indexName).Query(q).
		Return("3").Identifier(_vecField).Identifier(_valField).Identifier(_scoreField).
		Limit().OffsetNum(0, int64(topK)).
		Params().Nargs(2).NameValue().NameValue("q", valkey.VectorString32(query)).
		Dialect(2).
		Build()

	_, docs, err := s.client.Do(ctx, cmd).AsFtSearch()
	if err != nil {
		// The cached indexCreated flag can go stale: an operator runs
		// FT.DROPINDEX out of band, or two processes race on a fresh namespace.
		// If the index is gone, mirror local-store's empty-store behaviour
		// (empty result, no error) and clear the flag so a later Set recreates
		// it, rather than surfacing a hard error for what looks like an empty
		// store to the caller.
		if isNoSuchIndexErr(err) {
			s.indexCreated = false
			return pb.StoresFindResult{}, nil
		}
		return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: FT.SEARCH: %w", err)
	}

	keys := make([][]float32, 0, len(docs))
	values := make([][]byte, 0, len(docs))
	sims := make([]float32, 0, len(docs))
	for _, doc := range docs {
		// Decode the key from the returned `vec` bytes rather than the Valkey
		// key string: this guarantees the exact original float ordering/values
		// without a hex round-trip.
		vecBytes := []byte(doc.Doc[_vecField])
		k, err := bytesToVec(vecBytes)
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: decode vec: %w", err)
		}
		dist, err := strconv.ParseFloat(doc.Doc[_scoreField], 64)
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("valkey-store: Find: parse score %q: %w", doc.Doc[_scoreField], err)
		}
		keys = append(keys, k)
		values = append(values, []byte(doc.Doc[_valField]))
		sims = append(sims, distanceToSimilarity(s.cfg.DistanceMetric, dist))
	}

	return pb.StoresFindResult{
		Keys:         store.WrapKeys(keys),
		Values:       store.WrapValues(values),
		Similarities: sims,
	}, nil
}

// ensureIndex creates the FT vector index once, lazily, on the first Set. The
// dimension is fixed at creation (a second guard on top of the Go-side keyLen
// check). An "already exists" error is treated as success so a restart against
// a persisted index is a no-op.
func (s *ValkeyStore) ensureIndex(dim int) error {
	if s.indexCreated {
		return nil
	}

	// VECTOR attribute tokens. The count that follows the algorithm name is the
	// number of these tokens, so we build the slice and derive the count from
	// it — no hand-maintained magic number that drifts when HNSW knobs change.
	attrs := []string{"TYPE", "FLOAT32", "DIM", strconv.Itoa(dim), "DISTANCE_METRIC", s.cfg.DistanceMetric}
	if s.cfg.IndexAlgo == indexAlgoHNSW {
		attrs = append(attrs,
			"M", strconv.Itoa(s.cfg.HNSW.M),
			"EF_CONSTRUCTION", strconv.Itoa(s.cfg.HNSW.EFConstruction),
			"EF_RUNTIME", strconv.Itoa(s.cfg.HNSW.EFRuntime),
		)
	}

	args := []string{
		s.indexName,
		"ON", "HASH",
		"PREFIX", "1", s.prefix,
		"SCHEMA", _vecField, "VECTOR", s.cfg.IndexAlgo, strconv.Itoa(len(attrs)),
	}
	args = append(args, attrs...)

	// FT.CREATE has no typed builder entry point, so we use the Arbitrary escape
	// hatch. All tokens are non-key args in standalone mode.
	ctx, cancel := s.ctx()
	defer cancel()
	err := s.client.Do(ctx, s.client.B().Arbitrary("FT.CREATE").Args(args...).Build()).Error()
	if err != nil && !isIndexExistsErr(err) {
		return fmt.Errorf("valkey-store: FT.CREATE %s: %w", s.indexName, err)
	}

	s.indexCreated = true
	return nil
}

// checkDims rejects any key whose dimension disagrees with the learned keyLen.
// When keyLen is still open (-1, nothing set yet) there is nothing to check.
func (s *ValkeyStore) checkDims(op string, keys [][]float32) error {
	if s.keyLen == -1 {
		return nil
	}
	for i, k := range keys {
		if len(k) != s.keyLen {
			return fmt.Errorf("valkey-store: %s: key %d length %d does not match existing %d", op, i, len(k), s.keyLen)
		}
	}
	return nil
}

// distanceToSimilarity converts a Valkey distance into local-store's similarity
// convention. Only COSINE has a defined [-1, 1] similarity (sim = 1 - dist);
// for L2/IP the raw score is returned as the "similarity" with a documented
// meaning (smaller L2 = closer; larger IP = closer).
func distanceToSimilarity(metric string, dist float64) float32 {
	if metric == distanceCosine {
		return float32(1 - dist)
	}
	return float32(dist)
}

// isIndexExistsErr reports whether an FT.CREATE error is the benign
// "index already exists" case (e.g. after a restart against a persisted index).
func isIndexExistsErr(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

// isNoSuchIndexErr reports whether an FT.SEARCH error means the index no longer
// exists (dropped out of band, or never really created despite a stale cached
// flag). Valkey Search phrases this differently across versions, so we match
// the common variants rather than one exact string.
func isNoSuchIndexErr(err error) bool {
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "index") {
		return false
	}
	return strings.Contains(msg, "no such index") ||
		strings.Contains(msg, "not exist") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "unknown index")
}

// keyPrefix / indexName derive per-namespace identifiers from the model name so
// entries and indexes never collide across namespaces on a shared server.
func keyPrefix(namespace string) string {
	return _keyPrefixPrefix + nsToken(namespace) + ":"
}

func indexName(namespace string) string {
	return _indexPrefix + nsToken(namespace)
}

// nsToken maps a namespace to a collision-resistant, printable token. sanitize()
// alone is lossy (many distinct characters all fold to '_'), so namespaces like
// "a b", "a/b" and "a:b" would otherwise share one keyspace and FT index — a
// silent data-isolation bug (one store reading/clobbering another). We append a
// short hash of the ORIGINAL namespace so distinct names never collide, while
// the sanitized part keeps the token human-readable. It is deterministic, so a
// persisted index is found again after a restart.
func nsToken(namespace string) string {
	sum := sha256.Sum256([]byte(namespace))
	return sanitize(namespace) + "-" + hex.EncodeToString(sum[:])[:8]
}

// sanitize maps a namespace to a safe token for keys/index names: alphanumeric,
// '_', '-' and '.' pass through; everything else becomes '_'. An empty
// namespace becomes "default" so the key/index names stay well-formed.
func sanitize(namespace string) string {
	if namespace == "" {
		return "default"
	}
	var b strings.Builder
	b.Grow(len(namespace))
	for _, r := range namespace {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
