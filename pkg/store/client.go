package store

import (
	"context"
	"fmt"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Wrapper for the GRPC client so that simple use cases are handled without verbosity

// SetCols sets multiple key-value pairs in the store
// It's in columnar format so that keys[i] is associated with values[i]
func SetCols(ctx context.Context, c grpc.Backend, keys [][]float32, values [][]byte) error {
	protoKeys := make([]*proto.StoresKey, len(keys))
	for i, k := range keys {
		protoKeys[i] = &proto.StoresKey{
			Floats: k,
		}
	}
	protoValues := make([]*proto.StoresValue, len(values))
	for i, v := range values {
		protoValues[i] = &proto.StoresValue{
			Bytes: v,
		}
	}
	setOpts := &proto.StoresSetOptions{
		Keys:   protoKeys,
		Values: protoValues,
	}

	res, err := c.StoresSet(ctx, setOpts)
	if err != nil {
		return err
	}

	if res.Success {
		return nil
	}

	return fmt.Errorf("failed to set keys: %v", res.Message)
}

// SetSingle sets a single key-value pair in the store
// Don't call this in a tight loop, instead use SetCols
func SetSingle(ctx context.Context, c grpc.Backend, key []float32, value []byte) error {
	return SetCols(ctx, c, [][]float32{key}, [][]byte{value})
}

// DeleteCols deletes multiple key-value pairs from the store
// It's in columnar format so that keys[i] is associated with values[i]
func DeleteCols(ctx context.Context, c grpc.Backend, keys [][]float32) error {
	protoKeys := make([]*proto.StoresKey, len(keys))
	for i, k := range keys {
		protoKeys[i] = &proto.StoresKey{
			Floats: k,
		}
	}
	deleteOpts := &proto.StoresDeleteOptions{
		Keys: protoKeys,
	}

	res, err := c.StoresDelete(ctx, deleteOpts)
	if err != nil {
		return err
	}

	if res.Success {
		return nil
	}

	return fmt.Errorf("failed to delete keys: %v", res.Message)
}

// DeleteSingle deletes a single key-value pair from the store
// Don't call this in a tight loop, instead use DeleteCols
func DeleteSingle(ctx context.Context, c grpc.Backend, key []float32) error {
	return DeleteCols(ctx, c, [][]float32{key})
}

// GetCols gets multiple key-value pairs from the store
// It's in columnar format so that keys[i] is associated with values[i]
// Be warned the keys are sorted and will be returned in a different order than they were input
// There is no guarantee as to how the keys are sorted
func GetCols(ctx context.Context, c grpc.Backend, keys [][]float32) ([][]float32, [][]byte, error) {
	protoKeys := make([]*proto.StoresKey, len(keys))
	for i, k := range keys {
		protoKeys[i] = &proto.StoresKey{
			Floats: k,
		}
	}
	getOpts := &proto.StoresGetOptions{
		Keys: protoKeys,
	}

	res, err := c.StoresGet(ctx, getOpts)
	if err != nil {
		return nil, nil, err
	}

	ks := make([][]float32, len(res.Keys))
	for i, k := range res.Keys {
		ks[i] = k.Floats
	}
	vs := make([][]byte, len(res.Values))
	for i, v := range res.Values {
		vs[i] = v.Bytes
	}

	return ks, vs, nil
}

// GetSingle gets a single key-value pair from the store
// Don't call this in a tight loop, instead use GetCols
func GetSingle(ctx context.Context, c grpc.Backend, key []float32) ([]byte, error) {
	_, values, err := GetCols(ctx, c, [][]float32{key})
	if err != nil {
		return nil, err
	}

	if len(values) > 0 {
		return values[0], nil
	}

	return nil, fmt.Errorf("failed to get key")
}

// Find similar keys to the given key. Returns the keys, values, and similarities
func Find(ctx context.Context, c grpc.Backend, key []float32, topk int) ([][]float32, [][]byte, []float32, error) {
	findOpts := &proto.StoresFindOptions{
		Key: &proto.StoresKey{
			Floats: key,
		},
		TopK: int32(topk),
	}

	res, err := c.StoresFind(ctx, findOpts)
	if err != nil {
		return nil, nil, nil, err
	}

	ks := make([][]float32, len(res.Keys))
	vs := make([][]byte, len(res.Values))

	for i, k := range res.Keys {
		ks[i] = k.Floats
	}

	for i, v := range res.Values {
		vs[i] = v.Bytes
	}

	return ks, vs, res.Similarities, nil
}
