package main

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"

	"github.com/philippgille/chromem-go"
)

type Store struct {
	base.SingleThread

	maxId int
	db *chromem.DB
	c *chromem.Collection
}

func NewStore() (*Store, error) {
	db := chromem.NewDB()
	c, err := db.CreateCollection("default", nil, nil)
	if err != nil {
		return nil, err
	}

	return &Store{
		db: db,
		c:  c,
	}, nil
}

func (s *Store) Load(opts *pb.ModelOptions) error {
	return nil
}

func (s *Store) StoresSet(opts *pb.StoresSetOptions) error {
	ids := make([]string, len(opts.Keys))

	for i, _ := range(ids) {
		ids[i] = strconv.Itoa(i)
	}

	embeddings := make([][]float32, len(opts.Keys))

	for i, key := range opts.Keys {
		embeddings[i] = key.Floats
	}

	contents := make([]string, len(opts.Values))

	for i, value := range opts.Values {
		contents[i] = string(value.Bytes)
	}

	return s.c.Add(context.Background(), ids, embeddings, nil, contents)
}

func (s *Store) StoresDelete(opts *pb.StoresDeleteOptions) error {
	return fmt.Errorf("Per document delete not implemented in chromem")
}

func (s *Store) StoresGet(opts *pb.StoresGetOptions) (pb.StoresGetResult, error) {
	return pb.StoresGetResult{}, fmt.Errorf("Get not really implemented in chromem, although query may work")
}

func (s *Store) StoresFind(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	res, err := s.c.QueryEmbedding(context.Background(), opts.Key.Floats, int(opts.TopK), nil, nil)
	if err != nil {
		return pb.StoresFindResult{}, err
	}

	keys := make([]*pb.StoresKey, len(res))
	values := make([]*pb.StoresValue, len(res))
	similarities := make([]float32, len(res))

	for i, r := range(res) {
		keys[i] = &pb.StoresKey{Floats: r.Embedding}
		similarities[i] = r.Similarity
		values[i] = &pb.StoresValue{Bytes: []byte(r.Content)}
	}

	return pb.StoresFindResult{
		Keys: keys,
		Values: values,
		Similarities: similarities,
	}, nil
}
