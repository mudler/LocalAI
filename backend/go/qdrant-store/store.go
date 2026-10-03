package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	payloadKey   = "key"
	payloadValue = "value"

	// Results carry up to 50 MiB of base64-encoded values.
	maxRecvMsgBytes = 128 << 20
)

var pointNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://localai.io/qdrant-store"))

func newClient(cfg Config) (*qdrant.Client, error) {
	qcfg := &qdrant.Config{
		Host:                   cfg.Host,
		Port:                   cfg.Port,
		APIKey:                 cfg.APIKey,
		UseTLS:                 cfg.UseTLS,
		SkipCompatibilityCheck: true,
		GrpcOptions:            []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgBytes))},
	}
	if cfg.UseTLS {
		qcfg.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: cfg.TLSSkipVerify}
		if cfg.TLSCACert != "" {
			pem, err := os.ReadFile(cfg.TLSCACert)
			if err != nil {
				return nil, fmt.Errorf("read tls_ca_cert: %w", err)
			}
			qcfg.TLSConfig.RootCAs = x509.NewCertPool()
			if !qcfg.TLSConfig.RootCAs.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("tls_ca_cert %q has no valid certificate", cfg.TLSCACert)
			}
		}
	}
	return qdrant.NewClient(qcfg)
}

type QdrantStore struct {
	base.SingleThread

	client     *qdrant.Client
	cfg        Config
	collection string
	checked    bool
}

func (s *QdrantStore) Load(opts *pb.ModelOptions) error {
	// The loader's greedy autoload probes every backend with real model names.
	if !strings.HasPrefix(opts.GetModel(), store.NamespacePrefix) {
		return fmt.Errorf("qdrant-store: refusing to load %q: not a store namespace", opts.GetModel())
	}
	cfg, err := loadConfig(opts)
	if err != nil {
		return err
	}
	client, err := newClient(cfg)
	if err != nil {
		return fmt.Errorf("qdrant-store: connect to %s: %w", cfg.Addr(), err)
	}
	s.client, s.cfg, s.collection = client, cfg, collectionName(cfg, opts.GetModel())

	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.checkCollection(ctx); err != nil && !isNotFound(err) {
		_ = client.Close()
		return fmt.Errorf("qdrant-store: %s: %w", cfg.Addr(), err)
	}
	return nil
}

func (s *QdrantStore) checkCollection(ctx context.Context) error {
	info, err := s.client.GetCollectionInfo(ctx, s.collection)
	if err != nil {
		return err
	}
	params := info.GetConfig().GetParams().GetVectorsConfig().GetParams()
	if params == nil {
		return fmt.Errorf("collection %s does not use a single unnamed vector", s.collection)
	}
	if params.GetDistance() != s.cfg.Distance {
		return fmt.Errorf("collection %s uses distance %s but distance_metric is %s", s.collection, params.GetDistance(), s.cfg.Distance)
	}
	s.checked = true
	return nil
}

func (s *QdrantStore) Free() error {
	if s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *QdrantStore) StoresSet(opts *pb.StoresSetOptions) error {
	if len(opts.Keys) == 0 || len(opts.Keys) != len(opts.Values) {
		return fmt.Errorf("qdrant-store: Set: need matching keys and values, got %d and %d", len(opts.Keys), len(opts.Values))
	}
	points := make([]*qdrant.PointStruct, len(opts.Keys))
	for i, k := range opts.Keys {
		if err := checkFinite(k.Floats); err != nil {
			return fmt.Errorf("qdrant-store: Set: key %d: %w", i, err)
		}
		points[i] = &qdrant.PointStruct{
			Id:      pointID(k.Floats),
			Vectors: qdrant.NewVectorsDense(k.Floats),
			// Qdrant normalises COSINE vectors, so the exact key goes in the payload.
			Payload: qdrant.NewValueMap(map[string]any{
				payloadKey:   base64.StdEncoding.EncodeToString(vecToBytes(k.Floats)),
				payloadValue: base64.StdEncoding.EncodeToString(opts.Values[i].Bytes),
			}),
		}
	}

	err := s.upsert(points)
	if isNotFound(err) {
		if err := s.createCollection(len(opts.Keys[0].Floats)); err != nil {
			return fmt.Errorf("qdrant-store: Set: %w", err)
		}
		err = s.upsert(points)
	}
	if err != nil {
		return fmt.Errorf("qdrant-store: Set: %w", err)
	}
	return nil
}

func (s *QdrantStore) upsert(points []*qdrant.PointStruct) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: s.collection,
		Wait:           qdrant.PtrOf(true),
		Points:         points,
	})
	return err
}

func (s *QdrantStore) createCollection(dim int) error {
	ctx, cancel := s.ctx()
	defer cancel()
	err := s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: s.collection,
		VectorsConfig:  qdrant.NewVectorsConfig(&qdrant.VectorParams{Size: uint64(dim), Distance: s.cfg.Distance}),
	})
	if err != nil && status.Code(err) != codes.AlreadyExists {
		return fmt.Errorf("create collection %s: %w", s.collection, err)
	}
	return nil
}

func (s *QdrantStore) StoresGet(opts *pb.StoresGetOptions) (pb.StoresGetResult, error) {
	if len(opts.Keys) == 0 {
		return pb.StoresGetResult{}, nil
	}
	ctx, cancel := s.ctx()
	defer cancel()
	points, err := s.client.Get(ctx, &qdrant.GetPoints{
		CollectionName: s.collection,
		Ids:            pointIDs(opts.Keys),
		WithPayload:    qdrant.NewWithPayloadInclude(payloadKey, payloadValue),
	})
	if isNotFound(err) {
		return pb.StoresGetResult{}, nil
	}
	if err != nil {
		return pb.StoresGetResult{}, fmt.Errorf("qdrant-store: Get: %w", err)
	}

	var keys [][]float32
	var values [][]byte
	for _, p := range points {
		key, value, err := decodePayload(p.GetPayload())
		if err != nil {
			return pb.StoresGetResult{}, fmt.Errorf("qdrant-store: Get: %w", err)
		}
		keys = append(keys, key)
		values = append(values, value)
	}
	return pb.StoresGetResult{Keys: store.WrapKeys(keys), Values: store.WrapValues(values)}, nil
}

func (s *QdrantStore) StoresDelete(opts *pb.StoresDeleteOptions) error {
	if len(opts.Keys) == 0 {
		return fmt.Errorf("qdrant-store: Delete: no keys to delete")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: s.collection,
		Wait:           qdrant.PtrOf(true),
		Points:         qdrant.NewPointsSelector(pointIDs(opts.Keys)...),
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("qdrant-store: Delete: %w", err)
	}
	return nil
}

func (s *QdrantStore) StoresFind(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	query := opts.GetKey().GetFloats()
	if opts.TopK < 1 {
		return pb.StoresFindResult{}, fmt.Errorf("qdrant-store: Find: topK = %d, must be >= 1", opts.TopK)
	}
	if err := checkFinite(query); err != nil {
		return pb.StoresFindResult{}, fmt.Errorf("qdrant-store: Find: %w", err)
	}

	ctx, cancel := s.ctx()
	defer cancel()
	if !s.checked {
		err := s.checkCollection(ctx)
		if isNotFound(err) {
			return pb.StoresFindResult{}, nil
		}
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("qdrant-store: Find: %w", err)
		}
	}
	points, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: s.collection,
		Query:          qdrant.NewQueryDense(query),
		Limit:          qdrant.PtrOf(uint64(opts.TopK)),
		WithPayload:    qdrant.NewWithPayloadInclude(payloadKey, payloadValue),
	})
	if isNotFound(err) {
		return pb.StoresFindResult{}, nil
	}
	if err != nil {
		return pb.StoresFindResult{}, fmt.Errorf("qdrant-store: Find: %w", err)
	}

	var keys [][]float32
	var values [][]byte
	var sims []float32
	for _, p := range points {
		key, value, err := decodePayload(p.GetPayload())
		if err != nil {
			return pb.StoresFindResult{}, fmt.Errorf("qdrant-store: Find: %w", err)
		}
		keys = append(keys, key)
		values = append(values, value)
		sims = append(sims, p.GetScore())
	}
	return pb.StoresFindResult{Keys: store.WrapKeys(keys), Values: store.WrapValues(values), Similarities: sims}, nil
}

func (s *QdrantStore) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.cfg.RequestTimeout)
}

func pointID(vec []float32) *qdrant.PointId {
	return qdrant.NewID(uuid.NewSHA1(pointNamespace, vecToBytes(vec)).String())
}

func pointIDs(keys []*pb.StoresKey) []*qdrant.PointId {
	ids := make([]*qdrant.PointId, len(keys))
	for i, k := range keys {
		ids[i] = pointID(k.Floats)
	}
	return ids
}

func vecToBytes(vec []float32) []byte {
	b := make([]byte, 4*len(vec))
	for i, x := range vec {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func decodePayload(payload map[string]*qdrant.Value) ([]float32, []byte, error) {
	k, okKey := payload[payloadKey]
	v, okValue := payload[payloadValue]
	if !okKey || !okValue {
		return nil, nil, fmt.Errorf("point has no %q/%q payload; not written by qdrant-store", payloadKey, payloadValue)
	}
	keyBytes, err := base64.StdEncoding.DecodeString(k.GetStringValue())
	if err != nil {
		return nil, nil, fmt.Errorf("decode key payload: %w", err)
	}
	if len(keyBytes)%4 != 0 {
		return nil, nil, fmt.Errorf("key payload of %d bytes is not a float32 vector", len(keyBytes))
	}
	value, err := base64.StdEncoding.DecodeString(v.GetStringValue())
	if err != nil {
		return nil, nil, fmt.Errorf("decode value payload: %w", err)
	}
	key := make([]float32, len(keyBytes)/4)
	for i := range key {
		key[i] = math.Float32frombits(binary.LittleEndian.Uint32(keyBytes[4*i:]))
	}
	return key, value, nil
}

func collectionName(cfg Config, model string) string {
	if cfg.Collection != "" {
		return cfg.Collection
	}
	ns := strings.TrimPrefix(model, store.NamespacePrefix)
	readable := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, ns)
	if len(readable) > 64 {
		readable = readable[:64]
	}
	sum := sha256.Sum256([]byte(ns))
	return "localai_" + readable + "-" + hex.EncodeToString(sum[:4])
}

func checkFinite(vec []float32) error {
	for i, x := range vec {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return fmt.Errorf("component %d is %v; vectors must be finite", i, x)
		}
	}
	return nil
}

func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}
