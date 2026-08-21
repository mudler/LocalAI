package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/protobuf/proto"
)

// ModelConfigRevision returns a stable revision of the persisted semantic
// configuration. ModelConfig's JSON tags exclude runtime-derived state and
// source bookkeeping, while encoding/json orders map keys deterministically.
func ModelConfigRevision(cfg *ModelConfig) (string, error) {
	if cfg == nil {
		return "", errors.New("model config is nil")
	}

	canonical, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}

	return sha256Hex(canonical), nil
}

// EffectiveModelOptionsHash returns a deterministic hash of materialized
// backend options without allowing protobuf marshaling to mutate caller state.
func EffectiveModelOptionsHash(opts *pb.ModelOptions) (string, error) {
	if opts == nil {
		return "", errors.New("model options are nil")
	}

	cloned := proto.Clone(opts).(*pb.ModelOptions)
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(cloned)
	if err != nil {
		return "", err
	}

	return sha256Hex(canonical), nil
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
