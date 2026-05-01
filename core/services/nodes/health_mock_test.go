package nodes

import (
	"context"
	"fmt"
	"sync"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
)

// --- fakeNodeHealthStore ---

type fakeNodeHealthStore struct {
	mu     sync.Mutex
	nodes  map[string]*BackendNode
	models map[string][]NodeModel // nodeID -> models
	calls  []string               // track method calls
}

func newFakeNodeHealthStore() *fakeNodeHealthStore {
	return &fakeNodeHealthStore{
		nodes:  make(map[string]*BackendNode),
		models: make(map[string][]NodeModel),
	}
}

func (f *fakeNodeHealthStore) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeNodeHealthStore) getCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeNodeHealthStore) addNode(n *BackendNode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes[n.ID] = n
}

func (f *fakeNodeHealthStore) addNodeModel(nodeID string, nm NodeModel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.models[nodeID] = append(f.models[nodeID], nm)
}

func (f *fakeNodeHealthStore) getNode(id string) *BackendNode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nodes[id]
}

func (f *fakeNodeHealthStore) List(_ context.Context) ([]BackendNode, error) {
	f.record("List")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []BackendNode
	for _, n := range f.nodes {
		out = append(out, *n)
	}
	return out, nil
}

func (f *fakeNodeHealthStore) GetNodeModels(_ context.Context, nodeID string) ([]NodeModel, error) {
	f.record("GetNodeModels:" + nodeID)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.models[nodeID], nil
}

func (f *fakeNodeHealthStore) MarkOffline(_ context.Context, nodeID string) error {
	f.record("MarkOffline:" + nodeID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.nodes[nodeID]; ok {
		n.Status = StatusOffline
	}
	return nil
}

func (f *fakeNodeHealthStore) MarkUnhealthy(_ context.Context, nodeID string) error {
	f.record("MarkUnhealthy:" + nodeID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.nodes[nodeID]; ok {
		n.Status = StatusUnhealthy
	}
	return nil
}

func (f *fakeNodeHealthStore) MarkHealthy(_ context.Context, nodeID string) error {
	f.record("MarkHealthy:" + nodeID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.nodes[nodeID]; ok {
		n.Status = StatusHealthy
	}
	return nil
}

func (f *fakeNodeHealthStore) Heartbeat(_ context.Context, nodeID string, _ *HeartbeatUpdate) error {
	f.record("Heartbeat:" + nodeID)
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.nodes[nodeID]; ok {
		n.Status = StatusHealthy
		n.LastHeartbeat = time.Now()
	}
	return nil
}

func (f *fakeNodeHealthStore) FindStaleNodes(_ context.Context, _ time.Duration) ([]BackendNode, error) {
	f.record("FindStaleNodes")
	return nil, nil
}

func (f *fakeNodeHealthStore) RemoveNodeModel(_ context.Context, nodeID, modelName string, replicaIndex int) error {
	f.record(fmt.Sprintf("RemoveNodeModel:%s:%s:%d", nodeID, modelName, replicaIndex))
	return nil
}

// --- fakeBackendClient ---

type fakeBackendClient struct {
	healthy bool
	err     error
}

func (c *fakeBackendClient) IsBusy() bool { return false }
func (c *fakeBackendClient) HealthCheck(_ context.Context) (bool, error) {
	return c.healthy, c.err
}
func (c *fakeBackendClient) Embeddings(_ context.Context, _ *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) LoadModel(_ context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) PredictStream(_ context.Context, _ *pb.PredictOptions, _ func(*pb.Reply), _ ...ggrpc.CallOption) error {
	return nil
}
func (c *fakeBackendClient) Predict(_ context.Context, _ *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.Reply, error) {
	return nil, nil
}
func (c *fakeBackendClient) GenerateImage(_ context.Context, _ *pb.GenerateImageRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) GenerateVideo(_ context.Context, _ *pb.GenerateVideoRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) TTS(_ context.Context, _ *pb.TTSRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) TTSStream(_ context.Context, _ *pb.TTSRequest, _ func(*pb.Reply), _ ...ggrpc.CallOption) error {
	return nil
}
func (c *fakeBackendClient) SoundGeneration(_ context.Context, _ *pb.SoundGenerationRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) Detect(_ context.Context, _ *pb.DetectOptions, _ ...ggrpc.CallOption) (*pb.DetectResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) FaceVerify(_ context.Context, _ *pb.FaceVerifyRequest, _ ...ggrpc.CallOption) (*pb.FaceVerifyResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) FaceAnalyze(_ context.Context, _ *pb.FaceAnalyzeRequest, _ ...ggrpc.CallOption) (*pb.FaceAnalyzeResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) VoiceVerify(_ context.Context, _ *pb.VoiceVerifyRequest, _ ...ggrpc.CallOption) (*pb.VoiceVerifyResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) VoiceAnalyze(_ context.Context, _ *pb.VoiceAnalyzeRequest, _ ...ggrpc.CallOption) (*pb.VoiceAnalyzeResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) VoiceEmbed(_ context.Context, _ *pb.VoiceEmbedRequest, _ ...ggrpc.CallOption) (*pb.VoiceEmbedResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) AudioTranscription(_ context.Context, _ *pb.TranscriptRequest, _ ...ggrpc.CallOption) (*pb.TranscriptResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) AudioTranscriptionStream(_ context.Context, _ *pb.TranscriptRequest, _ func(chunk *pb.TranscriptStreamResponse), _ ...ggrpc.CallOption) error {
	return nil
}
func (c *fakeBackendClient) TokenizeString(_ context.Context, _ *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.TokenizationResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) Status(_ context.Context) (*pb.StatusResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) StoresSet(_ context.Context, _ *pb.StoresSetOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) StoresDelete(_ context.Context, _ *pb.StoresDeleteOptions, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) StoresGet(_ context.Context, _ *pb.StoresGetOptions, _ ...ggrpc.CallOption) (*pb.StoresGetResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) StoresFind(_ context.Context, _ *pb.StoresFindOptions, _ ...ggrpc.CallOption) (*pb.StoresFindResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) Rerank(_ context.Context, _ *pb.RerankRequest, _ ...ggrpc.CallOption) (*pb.RerankResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) GetTokenMetrics(_ context.Context, _ *pb.MetricsRequest, _ ...ggrpc.CallOption) (*pb.MetricsResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) VAD(_ context.Context, _ *pb.VADRequest, _ ...ggrpc.CallOption) (*pb.VADResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) AudioEncode(_ context.Context, _ *pb.AudioEncodeRequest, _ ...ggrpc.CallOption) (*pb.AudioEncodeResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) AudioDecode(_ context.Context, _ *pb.AudioDecodeRequest, _ ...ggrpc.CallOption) (*pb.AudioDecodeResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) AudioTransform(_ context.Context, _ *pb.AudioTransformRequest, _ ...ggrpc.CallOption) (*pb.AudioTransformResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) AudioTransformStream(_ context.Context, _ ...ggrpc.CallOption) (grpc.AudioTransformStreamClient, error) {
	return nil, nil
}
func (c *fakeBackendClient) ModelMetadata(_ context.Context, _ *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) StartFineTune(_ context.Context, _ *pb.FineTuneRequest, _ ...ggrpc.CallOption) (*pb.FineTuneJobResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) FineTuneProgress(_ context.Context, _ *pb.FineTuneProgressRequest, _ func(*pb.FineTuneProgressUpdate), _ ...ggrpc.CallOption) error {
	return nil
}
func (c *fakeBackendClient) StopFineTune(_ context.Context, _ *pb.FineTuneStopRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) ListCheckpoints(_ context.Context, _ *pb.ListCheckpointsRequest, _ ...ggrpc.CallOption) (*pb.ListCheckpointsResponse, error) {
	return nil, nil
}
func (c *fakeBackendClient) ExportModel(_ context.Context, _ *pb.ExportModelRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) StartQuantization(_ context.Context, _ *pb.QuantizationRequest, _ ...ggrpc.CallOption) (*pb.QuantizationJobResult, error) {
	return nil, nil
}
func (c *fakeBackendClient) QuantizationProgress(_ context.Context, _ *pb.QuantizationProgressRequest, _ func(*pb.QuantizationProgressUpdate), _ ...ggrpc.CallOption) error {
	return nil
}
func (c *fakeBackendClient) StopQuantization(_ context.Context, _ *pb.QuantizationStopRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return nil, nil
}
func (c *fakeBackendClient) Free(_ context.Context) error {
	return nil
}

// --- fakeBackendClientFactory ---

type fakeBackendClientFactory struct {
	mu      sync.Mutex
	clients map[string]*fakeBackendClient
	// default client returned when address not in clients map
	defaultClient *fakeBackendClient
}

func newFakeBackendClientFactory() *fakeBackendClientFactory {
	return &fakeBackendClientFactory{
		clients:       make(map[string]*fakeBackendClient),
		defaultClient: &fakeBackendClient{healthy: true},
	}
}

func (f *fakeBackendClientFactory) setClient(address string, c *fakeBackendClient) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients[address] = c
}

func (f *fakeBackendClientFactory) NewClient(address string, _ bool) grpc.Backend {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[address]; ok {
		return c
	}
	return f.defaultClient
}

// helper to make a BackendNode with given properties
func makeTestNode(id, name, address string, status string, lastHeartbeat time.Time) *BackendNode {
	return &BackendNode{
		ID:            id,
		Name:          name,
		Address:       address,
		Status:        status,
		LastHeartbeat: lastHeartbeat,
		NodeType:      NodeTypeBackend,
	}
}

// makeTestNodeWithHTTP creates a BackendNode with HTTPAddress set
func makeTestNodeWithHTTP(id, name, address, httpAddress string, status string, lastHeartbeat time.Time) *BackendNode {
	n := makeTestNode(id, name, address, status, lastHeartbeat)
	n.HTTPAddress = httpAddress
	return n
}

// helper to build a HealthMonitor with fakes
func newTestHealthMonitor(store NodeHealthStore, factory BackendClientFactory, autoOffline bool, staleThreshold time.Duration) *HealthMonitor {
	return &HealthMonitor{
		registry:       store,
		checkInterval:  15 * time.Second,
		staleThreshold: staleThreshold,
		autoOffline:    autoOffline,
		clientFactory:  factory,
	}
}

// staleTime returns a time well past the given threshold.
func staleTime(threshold time.Duration) time.Time {
	return time.Now().Add(-2 * threshold)
}

// freshTime returns a time well within any reasonable threshold.
func freshTime() time.Time {
	return time.Now()
}

// Compile-time interface checks
var _ NodeHealthStore = (*fakeNodeHealthStore)(nil)
var _ BackendClientFactory = (*fakeBackendClientFactory)(nil)
var _ grpc.Backend = (*fakeBackendClient)(nil)
