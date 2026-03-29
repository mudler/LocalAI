package distributed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"

	pgdriver "gorm.io/driver/postgres"
	gormDB "gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testLLM is a minimal AIModel implementation for testing.
// Override methods to write output to Dst so we can test the full
// FileStagingClient round-trip (upload inputs + download outputs).
type testLLM struct {
	base.Base
	loaded    bool
	lastModel string
	// dstOutput is the content written to any Dst path by output-producing methods.
	dstOutput []byte
	// lastSrc records the last Src/input path seen (for verifying staging rewrote it).
	lastSrc string
	// lastAudioDst records the Dst field from AudioTranscription (it's an input, not output).
	lastAudioDst string
	// lastTTSModel records the Model field from TTS requests (for verifying path rewriting).
	lastTTSModel string
}

func (t *testLLM) Load(opts *pb.ModelOptions) error {
	t.loaded = true
	t.lastModel = opts.ModelFile
	return nil
}

func (t *testLLM) Predict(opts *pb.PredictOptions) (string, error) {
	if !t.loaded {
		return "", fmt.Errorf("model not loaded")
	}
	return "test response from remote node", nil
}

func (t *testLLM) GenerateImage(req *pb.GenerateImageRequest) error {
	t.lastSrc = req.Src
	if req.Dst != "" && len(t.dstOutput) > 0 {
		return os.WriteFile(req.Dst, t.dstOutput, 0644)
	}
	return nil
}

func (t *testLLM) GenerateVideo(req *pb.GenerateVideoRequest) error {
	t.lastSrc = req.StartImage
	if req.Dst != "" && len(t.dstOutput) > 0 {
		return os.WriteFile(req.Dst, t.dstOutput, 0644)
	}
	return nil
}

func (t *testLLM) TTS(req *pb.TTSRequest) error {
	t.lastTTSModel = req.Model
	if req.Dst != "" && len(t.dstOutput) > 0 {
		return os.WriteFile(req.Dst, t.dstOutput, 0644)
	}
	return nil
}

func (t *testLLM) SoundGeneration(req *pb.SoundGenerationRequest) error {
	if req.Src != nil {
		t.lastSrc = *req.Src
	}
	if req.Dst != "" && len(t.dstOutput) > 0 {
		return os.WriteFile(req.Dst, t.dstOutput, 0644)
	}
	return nil
}

func (t *testLLM) AudioTranscription(req *pb.TranscriptRequest) (pb.TranscriptResult, error) {
	t.lastAudioDst = req.Dst
	return pb.TranscriptResult{Text: "transcribed text"}, nil
}

// startTestGRPCServer starts a real gRPC backend server on a free port
// and returns the address and cleanup function.
func startTestGRPCServer(llm grpcPkg.AIModel) (string, func(), error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	addr := lis.Addr().String()

	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(50*1024*1024),
		grpc.MaxSendMsgSize(50*1024*1024),
	)
	pb.RegisterBackendServer(s, grpcPkg.NewBackendServer(llm))

	go func() {
		defer GinkgoRecover()
		_ = s.Serve(lis)
	}()

	cleanup := func() {
		s.GracefulStop()
	}
	return addr, cleanup, nil
}

// startTestHTTPFileServer starts a test HTTP file transfer server (mirroring serve_backend_http.go)
// on a free port and returns the address and cleanup function.
func startTestHTTPFileServer(stagingDir string) (string, func(), error) {
	if err := os.MkdirAll(stagingDir, 0750); err != nil {
		return "", nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path[len("/v1/files/"):]
		switch r.Method {
		case http.MethodPut:
			safeName := filepath.Base(key)
			dstPath := filepath.Join(stagingDir, safeName)
			f, err := os.Create(dstPath)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			io.Copy(f, r.Body)
			f.Close()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"local_path":%q}`, dstPath)
		case http.MethodGet:
			safeName := filepath.Base(key)
			srcPath := filepath.Join(stagingDir, safeName)
			if _, statErr := os.Stat(srcPath); os.IsNotExist(statErr) {
				// AllocRemoteTemp creates files under stagingDir/tmp/
				srcPath = filepath.Join(stagingDir, "tmp", safeName)
			}
			f, err := os.Open(srcPath)
			if err != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			defer f.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			io.Copy(w, f)
		case http.MethodPost:
			if key == "temp" {
				tmpDir := filepath.Join(stagingDir, "tmp")
				os.MkdirAll(tmpDir, 0750)
				f, err := os.CreateTemp(tmpDir, "output-*")
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				localPath := f.Name()
				f.Close()
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"local_path":%q}`, localPath)
			} else {
				http.Error(w, "not found", http.StatusNotFound)
			}
		}
	})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	httpAddr := lis.Addr().String()
	srv := &http.Server{Handler: mux}
	go srv.Serve(lis)

	cleanup := func() {
		srv.Close()
	}
	return httpAddr, cleanup, nil
}

var _ = Describe("Full Distributed Inference Flow", Label("Distributed"), func() {
	var (
		infra    *TestInfra
		cancel   context.CancelFunc
		ctx      context.Context
		db       *gormDB.DB
		registry *nodes.NodeRegistry
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_fullflow_test")
		ctx, cancel = context.WithTimeout(infra.Ctx, 2*time.Minute)

		var err error
		db, err = gormDB.Open(pgdriver.Open(infra.PGURL), &gormDB.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		cancel()
	})

	// newTestSmartRouter creates a SmartRouter with NATS wired up and a mock
	// backend.install handler that always replies success for all registered nodes.
	newTestSmartRouter := func(reg *nodes.NodeRegistry, extraOpts ...nodes.SmartRouterOptions) *nodes.SmartRouter {
		unloader := nodes.NewRemoteUnloaderAdapter(reg, infra.NC)

		opts := nodes.SmartRouterOptions{
			Unloader: unloader,
		}
		if len(extraOpts) > 0 {
			o := extraOpts[0]
			if o.FileStager != nil {
				opts.FileStager = o.FileStager
			}
			if o.GalleriesJSON != "" {
				opts.GalleriesJSON = o.GalleriesJSON
			}
			if o.AuthToken != "" {
				opts.AuthToken = o.AuthToken
			}
			if o.DB != nil {
				opts.DB = o.DB
			}
		}

		router := nodes.NewSmartRouter(reg, opts)

		// Subscribe a mock backend.install handler that replies success for any node.
		// We use a wildcard-style approach: subscribe to all nodes' install subjects
		// by registering after each node. In practice, we rely on the test registering
		// nodes before calling Route, so we subscribe to a catch-all pattern.
		infra.NC.Conn().Subscribe("nodes.*.backend.install", func(msg *nats.Msg) {
			reply := messaging.BackendInstallReply{Success: true}
			data, _ := json.Marshal(reply)
			msg.Respond(data)
		})

		return router
	}
	// suppress unused warning in case some tests don't call it
	_ = newTestSmartRouter

	It("should route inference to a registered node with a real gRPC backend", func() {
		// 1. Start a mock gRPC backend
		llm := &testLLM{}
		addr, cleanup, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanup()

		// 2. Register it as a node
		node := &nodes.BackendNode{
			Name:    "test-gpu-1",
			Address: addr,
		}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		// 3. Create SmartRouter and route a request
		router := newTestSmartRouter(registry)

		// The model is not loaded yet, so Route will pick the node and call LoadModel
		result, err := router.Route(ctx, "", "test-model", "llama-cpp", &pb.ModelOptions{
			Model: "test-model",
		}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.Node.Name).To(Equal("test-gpu-1"))

		// 4. Verify the model was loaded on the backend
		Expect(llm.loaded).To(BeTrue())

		// 5. Use the client to call Predict
		reply, err := result.Client.Predict(ctx, &pb.PredictOptions{
			Prompt: "Hello world",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply.Message)).To(Equal("test response from remote node"))

		// 6. Release and verify in-flight decremented
		result.Release()
		models, err := registry.GetNodeModels(context.Background(), node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(HaveLen(1))
		Expect(models[0].InFlight).To(Equal(0))

		// 7. Verify model recorded as "loaded" in registry
		Expect(models[0].State).To(Equal("loaded"))
		Expect(models[0].ModelName).To(Equal("test-model"))
	})

	It("should load-balance across multiple nodes with same model", func() {
		// Start two mock gRPC backends
		llm1 := &testLLM{}
		addr1, cleanup1, err := startTestGRPCServer(llm1)
		Expect(err).ToNot(HaveOccurred())
		defer cleanup1()

		llm2 := &testLLM{}
		addr2, cleanup2, err := startTestGRPCServer(llm2)
		Expect(err).ToNot(HaveOccurred())
		defer cleanup2()

		// Register both nodes
		node1 := &nodes.BackendNode{Name: "node-heavy", Address: addr1}
		node2 := &nodes.BackendNode{Name: "node-light", Address: addr2}
		Expect(registry.Register(context.Background(), node1, true)).To(Succeed())
		Expect(registry.Register(context.Background(), node2, true)).To(Succeed())

		// Set both as having the model loaded
		Expect(registry.SetNodeModel(context.Background(), node1.ID, "test-model", "loaded")).To(Succeed())
		Expect(registry.SetNodeModel(context.Background(), node2.ID, "test-model", "loaded")).To(Succeed())

		// Set node-1 with high in-flight (5), node-2 with low in-flight (1)
		for range 5 {
			Expect(registry.IncrementInFlight(context.Background(), node1.ID, "test-model")).To(Succeed())
		}
		Expect(registry.IncrementInFlight(context.Background(), node2.ID, "test-model")).To(Succeed())

		// Route should pick node-2 (least loaded) thanks to ORDER BY in_flight ASC
		router := newTestSmartRouter(registry)
		result, err := router.Route(ctx, "", "test-model", "llama-cpp", nil, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Node.Name).To(Equal("node-light"))
		result.Release()
	})

	It("should load model on empty node when no node has it", func() {
		// Start a mock gRPC backend
		llm := &testLLM{}
		addr, cleanup, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanup()

		// Register a node with NO models loaded
		node := &nodes.BackendNode{Name: "empty-node", Address: addr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		// Route should pick this node and call LoadModel on it
		router := newTestSmartRouter(registry)
		result, err := router.Route(ctx, "", "new-model", "llama-cpp", &pb.ModelOptions{
			Model: "new-model",
		}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Node.Name).To(Equal("empty-node"))
		Expect(llm.loaded).To(BeTrue())

		// Verify model is now recorded in registry
		models, err := registry.GetNodeModels(context.Background(), node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(HaveLen(1))
		Expect(models[0].ModelName).To(Equal("new-model"))
		Expect(models[0].State).To(Equal("loaded"))

		result.Release()
	})

	It("should unload remote model via NATS", func() {
		// Register a node with a loaded model
		node := &nodes.BackendNode{Name: "gpu-unload", Address: "127.0.0.1:50099"}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())
		Expect(registry.SetNodeModel(context.Background(), node.ID, "old-model", "loaded")).To(Succeed())

		// Subscribe to NATS backend.stop for this node
		stopSubject := messaging.SubjectNodeBackendStop(node.ID)
		received := make(chan struct{}, 1)
		rawConn, err := nats.Connect(infra.NatsURL)
		Expect(err).ToNot(HaveOccurred())
		defer rawConn.Close()

		_, err = rawConn.Subscribe(stopSubject, func(msg *nats.Msg) {
			received <- struct{}{}
		})
		Expect(err).ToNot(HaveOccurred())

		// Create RemoteUnloaderAdapter and unload model
		unloader := nodes.NewRemoteUnloaderAdapter(registry, infra.NC)
		err = unloader.UnloadRemoteModel("old-model")
		Expect(err).ToNot(HaveOccurred())

		// Verify NATS event received
		Eventually(received, 5*time.Second).Should(Receive())

		// Verify model removed from registry
		models, err := registry.GetNodeModels(context.Background(), node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(models).To(BeEmpty())
	})

	It("should integrate ModelRouterAdapter with SmartRouter end-to-end", func() {
		// Start a mock gRPC backend
		llm := &testLLM{}
		addr, cleanup, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanup()

		// Register node
		node := &nodes.BackendNode{Name: "adapter-node", Address: addr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		// Create SmartRouter + ModelRouterAdapter
		router := newTestSmartRouter(registry)
		adapter := nodes.NewModelRouterAdapter(router)

		// Call adapter.Route() (same signature ModelLoader uses)
		m, err := adapter.Route(ctx, "llama-cpp", "test-model-id", "test-model", "",
			&pb.ModelOptions{Model: "test-model"}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).ToNot(BeNil())

		// Verify returned Model has correct ID and nil process (remote)
		Expect(m.ID).To(Equal("test-model-id"))
		Expect(m.Process()).To(BeNil())

		// Verify the model was loaded on the backend
		Expect(llm.loaded).To(BeTrue())

		// Use the Model's GRPC() method to get a client and verify inference works
		client := m.GRPC(false, nil)
		Expect(client).ToNot(BeNil())
		reply, err := client.Predict(ctx, &pb.PredictOptions{Prompt: "test"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply.Message)).To(Equal("test response from remote node"))

		// Release the model via adapter
		adapter.ReleaseModel("test-model-id")
	})

	It("should stage model files via HTTP when routing to a new node", func() {
		// Create a real model file on disk
		modelDir := GinkgoT().TempDir()
		modelContent := []byte("fake GGUF model data — this is test content for file transfer verification")
		modelPath := filepath.Join(modelDir, "model.gguf")
		Expect(os.WriteFile(modelPath, modelContent, 0644)).To(Succeed())

		mmprojContent := []byte("fake mmproj data for multimodal projection")
		mmprojPath := filepath.Join(modelDir, "mmproj.bin")
		Expect(os.WriteFile(mmprojPath, mmprojContent, 0644)).To(Succeed())

		// Start a real gRPC backend server (for AI RPCs) and HTTP server (for file transfer)
		llm := &testLLM{}
		stagingDir := GinkgoT().TempDir()
		addr, cleanupGRPC, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupGRPC()

		httpAddr, cleanupHTTP, err := startTestHTTPFileServer(stagingDir)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupHTTP()

		// Register the node in PostgreSQL
		node := &nodes.BackendNode{Name: "staging-node", Address: addr, HTTPAddress: httpAddr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		// Create HTTPFileStager that resolves node IDs to HTTP addresses
		stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			n, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			return n.HTTPAddress, nil
		}, "")

		// Create SmartRouter with the HTTPFileStager
		router := newTestSmartRouter(registry, nodes.SmartRouterOptions{FileStager: stager})

		// Route with ModelOptions that have file paths — SmartRouter should stage them
		result, err := router.Route(ctx, "", "staged-model", "llama-cpp", &pb.ModelOptions{
			Model:     "staged-model",
			ModelFile: modelPath,
			MMProj:    mmprojPath,
		}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.Node.Name).To(Equal("staging-node"))

		// Verify the model file bytes were transferred to the backend's staging dir
		stagedModelPath := filepath.Join(stagingDir, "model.gguf")
		stagedModelData, err := os.ReadFile(stagedModelPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedModelData).To(Equal(modelContent))

		stagedMMProjPath := filepath.Join(stagingDir, "mmproj.bin")
		stagedMMProjData, err := os.ReadFile(stagedMMProjPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedMMProjData).To(Equal(mmprojContent))

		// Verify LoadModel was called with the rewritten (remote) paths
		Expect(llm.loaded).To(BeTrue())
		Expect(llm.lastModel).To(Equal(stagedModelPath))

		// Verify Predict still works through the FileStagingClient wrapper
		reply, err := result.Client.Predict(ctx, &pb.PredictOptions{
			Prompt: "test via staging client",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply.Message)).To(Equal("test response from remote node"))

		result.Release()
	})

	It("should stage multimodal input files via HTTP through FileStagingClient", func() {
		// Create a real image file on disk
		imageDir := GinkgoT().TempDir()
		imageContent := []byte("fake JPEG image data for multimodal testing")
		imagePath := filepath.Join(imageDir, "photo.jpg")
		Expect(os.WriteFile(imagePath, imageContent, 0644)).To(Succeed())

		// Start gRPC server (AI RPCs) and HTTP server (file transfer)
		llm := &testLLM{}
		stagingDir := GinkgoT().TempDir()
		addr, cleanupGRPC, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupGRPC()

		httpAddr, cleanupHTTP, err := startTestHTTPFileServer(stagingDir)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupHTTP()

		// Register node
		node := &nodes.BackendNode{Name: "mm-node", Address: addr, HTTPAddress: httpAddr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		// Create HTTPFileStager
		stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			n, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			return n.HTTPAddress, nil
		}, "")

		// Create SmartRouter with FileStager
		router := newTestSmartRouter(registry, nodes.SmartRouterOptions{FileStager: stager})

		// Route with ModelOptions — triggers LoadModel on the node
		modelDir := GinkgoT().TempDir()
		modelPath := filepath.Join(modelDir, "vision.gguf")
		Expect(os.WriteFile(modelPath, []byte("vision model data"), 0644)).To(Succeed())

		result, err := router.Route(ctx, "", "vision-model", "llama-cpp", &pb.ModelOptions{
			Model:     "vision-model",
			ModelFile: modelPath,
		}, false)
		Expect(err).ToNot(HaveOccurred())

		// Verify LoadModel was called (model file was staged)
		Expect(llm.loaded).To(BeTrue())

		// Now call Predict with image file paths — FileStagingClient should stage them
		_, err = result.Client.Predict(ctx, &pb.PredictOptions{
			Prompt: "describe this image",
			Images: []string{imagePath},
		})
		Expect(err).ToNot(HaveOccurred())

		// Verify the image file was actually transferred to the backend staging dir
		stagedImagePath := filepath.Join(stagingDir, "photo.jpg")
		stagedImageData, err := os.ReadFile(stagedImagePath)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedImageData).To(Equal(imageContent))

		result.Release()
	})

	It("should transfer output files back via HTTP", func() {
		// Start gRPC server (AI RPCs) and HTTP server (file transfer)
		llm := &testLLM{}
		stagingDir := GinkgoT().TempDir()
		addr, cleanupGRPC, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupGRPC()

		httpAddr, cleanupHTTP, err := startTestHTTPFileServer(stagingDir)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupHTTP()

		// Register node
		node := &nodes.BackendNode{Name: "output-node", Address: addr, HTTPAddress: httpAddr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		// Create HTTPFileStager
		stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			n, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			return n.HTTPAddress, nil
		}, "")

		// Test AllocRemoteTemp + FetchRemote directly (the output retrieval path)
		remoteTmpPath, err := stager.AllocRemoteTemp(ctx, node.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(remoteTmpPath).ToNot(BeEmpty())

		// Simulate backend writing output to the temp path
		outputContent := []byte("generated image output data from the backend")
		Expect(os.WriteFile(remoteTmpPath, outputContent, 0644)).To(Succeed())

		// FetchRemote pulls the file from the backend to a local path
		localOutputDir := GinkgoT().TempDir()
		localOutputPath := filepath.Join(localOutputDir, "output.png")
		err = stager.FetchRemote(ctx, node.ID, remoteTmpPath, localOutputPath)
		Expect(err).ToNot(HaveOccurred())

		// Verify the output file was retrieved with correct content
		retrievedData, err := os.ReadFile(localOutputPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(retrievedData).To(Equal(outputContent))
	})

	// --- Full round-trip tests for every FileStagingClient src/dst path ---

	// Helper: creates an HTTPFileStager + SmartRouter, registers a node,
	// and routes to it. Returns the RouteResult (with FileStagingClient) and cleanup.
	setupStagedRoute := func(llm *testLLM, backendType, modelName string) (
		*nodes.RouteResult, string, func(),
	) {
		stagingDir := GinkgoT().TempDir()
		addr, cleanupGRPC, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())

		httpAddr, cleanupHTTP, err := startTestHTTPFileServer(stagingDir)
		Expect(err).ToNot(HaveOccurred())

		node := &nodes.BackendNode{Name: modelName + "-node", Address: addr, HTTPAddress: httpAddr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			n, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			return n.HTTPAddress, nil
		}, "")

		router := newTestSmartRouter(registry, nodes.SmartRouterOptions{FileStager: stager})

		result, err := router.Route(ctx, "", modelName, backendType, &pb.ModelOptions{
			Model: modelName,
		}, false)
		Expect(err).ToNot(HaveOccurred())

		cleanup := func() {
			cleanupGRPC()
			cleanupHTTP()
		}
		return result, stagingDir, cleanup
	}

	It("should round-trip output via FileStagingClient.GenerateImage (Src + Dst)", func() {
		outputData := []byte("PNG image generated by the backend - 1024x1024 pixels")

		llm := &testLLM{dstOutput: outputData}
		result, stagingDir, cleanup := setupStagedRoute(llm, "diffusers", "sd-model")
		defer cleanup()
		defer result.Release()

		// Create a source image to test input staging (img2img)
		srcDir := GinkgoT().TempDir()
		srcContent := []byte("source image for img2img")
		srcPath := filepath.Join(srcDir, "src.png")
		Expect(os.WriteFile(srcPath, srcContent, 0644)).To(Succeed())

		localOutputDir := GinkgoT().TempDir()
		frontendDst := filepath.Join(localOutputDir, "generated.png")

		genResult, err := result.Client.GenerateImage(ctx, &pb.GenerateImageRequest{
			PositivePrompt: "a cat",
			Src:            srcPath,
			Dst:            frontendDst,
			Height:         1024,
			Width:          1024,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(genResult.Success).To(BeTrue())

		// Verify input: Src was staged to backend — testLLM.lastSrc should be a staging dir path
		Expect(llm.lastSrc).To(ContainSubstring(stagingDir))

		// Verify the staged input file has correct content
		stagedSrcData, err := os.ReadFile(llm.lastSrc)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedSrcData).To(Equal(srcContent))

		// Verify output: the generated file was pulled back to the frontend
		retrievedData, err := os.ReadFile(frontendDst)
		Expect(err).ToNot(HaveOccurred())
		Expect(retrievedData).To(Equal(outputData))
	})

	It("should round-trip output via FileStagingClient.GenerateVideo (StartImage + Dst)", func() {
		outputData := []byte("MP4 video generated by the backend")

		llm := &testLLM{dstOutput: outputData}
		result, stagingDir, cleanup := setupStagedRoute(llm, "diffusers", "vid-model")
		defer cleanup()
		defer result.Release()

		// Create a start image to test input staging
		imgDir := GinkgoT().TempDir()
		startImageContent := []byte("start frame image data")
		startImagePath := filepath.Join(imgDir, "start.png")
		Expect(os.WriteFile(startImagePath, startImageContent, 0644)).To(Succeed())

		localOutputDir := GinkgoT().TempDir()
		frontendDst := filepath.Join(localOutputDir, "generated.mp4")

		genResult, err := result.Client.GenerateVideo(ctx, &pb.GenerateVideoRequest{
			Prompt:     "a flying cat",
			StartImage: startImagePath,
			Dst:        frontendDst,
			NumFrames:  16,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(genResult.Success).To(BeTrue())

		// Verify input: StartImage was staged
		Expect(llm.lastSrc).To(ContainSubstring(stagingDir))
		stagedStartData, err := os.ReadFile(llm.lastSrc)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedStartData).To(Equal(startImageContent))

		// Verify output: video was pulled back
		retrievedData, err := os.ReadFile(frontendDst)
		Expect(err).ToNot(HaveOccurred())
		Expect(retrievedData).To(Equal(outputData))
	})

	It("should round-trip output via FileStagingClient.TTS (Dst only)", func() {
		outputData := []byte("WAV audio generated by TTS backend")

		llm := &testLLM{dstOutput: outputData}
		result, _, cleanup := setupStagedRoute(llm, "piper", "tts-model")
		defer cleanup()
		defer result.Release()

		localOutputDir := GinkgoT().TempDir()
		frontendDst := filepath.Join(localOutputDir, "speech.wav")

		ttsResult, err := result.Client.TTS(ctx, &pb.TTSRequest{
			Text:  "Hello world",
			Model: "tts-model",
			Dst:   frontendDst,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(ttsResult.Success).To(BeTrue())

		// Verify output: audio was pulled back
		retrievedData, err := os.ReadFile(frontendDst)
		Expect(err).ToNot(HaveOccurred())
		Expect(retrievedData).To(Equal(outputData))
	})

	It("should round-trip via FileStagingClient.SoundGeneration (Src + Dst)", func() {
		outputData := []byte("generated sound effect audio data")

		llm := &testLLM{dstOutput: outputData}
		result, stagingDir, cleanup := setupStagedRoute(llm, "bark", "soundgen-model")
		defer cleanup()
		defer result.Release()

		// Create input audio source
		srcDir := GinkgoT().TempDir()
		srcContent := []byte("input audio for sound generation")
		srcPath := filepath.Join(srcDir, "input.wav")
		Expect(os.WriteFile(srcPath, srcContent, 0644)).To(Succeed())

		localOutputDir := GinkgoT().TempDir()
		frontendDst := filepath.Join(localOutputDir, "output.wav")

		sgResult, err := result.Client.SoundGeneration(ctx, &pb.SoundGenerationRequest{
			Text: "explosion sound",
			Src:  &srcPath,
			Dst:  frontendDst,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(sgResult.Success).To(BeTrue())

		// Verify input: Src was staged
		Expect(llm.lastSrc).To(ContainSubstring(stagingDir))
		stagedSrcData, err := os.ReadFile(llm.lastSrc)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedSrcData).To(Equal(srcContent))

		// Verify output: audio was pulled back
		retrievedData, err := os.ReadFile(frontendDst)
		Expect(err).ToNot(HaveOccurred())
		Expect(retrievedData).To(Equal(outputData))
	})

	It("should stage input audio via FileStagingClient.AudioTranscription (Dst is input)", func() {
		llm := &testLLM{}
		result, stagingDir, cleanup := setupStagedRoute(llm, "whisper", "whisper-model")
		defer cleanup()
		defer result.Release()

		// Create input audio file
		audioDir := GinkgoT().TempDir()
		audioContent := []byte("WAV audio data for transcription")
		audioPath := filepath.Join(audioDir, "recording.wav")
		Expect(os.WriteFile(audioPath, audioContent, 0644)).To(Succeed())

		// AudioTranscription uses Dst as the input audio path (confusing naming)
		txResult, err := result.Client.AudioTranscription(ctx, &pb.TranscriptRequest{
			Dst: audioPath,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(txResult.Text).To(Equal("transcribed text"))

		// Verify input: audio file was staged to the backend
		Expect(llm.lastAudioDst).To(ContainSubstring(stagingDir))
		stagedAudioData, err := os.ReadFile(llm.lastAudioDst)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedAudioData).To(Equal(audioContent))
	})

	It("should translate TTS Model path to remote worker path", func() {
		outputData := []byte("WAV audio generated by TTS backend")
		llm := &testLLM{dstOutput: outputData}

		// Set up real file transfer server so model staging preserves directory structure
		modelsDir := GinkgoT().TempDir()
		stagingDir := GinkgoT().TempDir()
		dataDir := GinkgoT().TempDir()
		addr, cleanupGRPC, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupGRPC()

		httpLis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		httpAddr := httpLis.Addr().String()
		httpServer, err := nodes.StartFileTransferServerWithListener(httpLis, stagingDir, modelsDir, dataDir, "", 0)
		Expect(err).ToNot(HaveOccurred())
		defer nodes.ShutdownFileTransferServer(httpServer)

		node := &nodes.BackendNode{Name: "tts-node", Address: addr, HTTPAddress: httpAddr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			n, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			return n.HTTPAddress, nil
		}, "")

		// Create model files on the "frontend"
		frontendModelsDir := GinkgoT().TempDir()
		modelContent := []byte("fake onnx model data")
		configContent := []byte(`{"audio":{"sample_rate":22050}}`)
		modelFile := filepath.Join(frontendModelsDir, "it-paola-medium.onnx")
		configFile := filepath.Join(frontendModelsDir, "it-paola-medium.onnx.json")
		Expect(os.WriteFile(modelFile, modelContent, 0644)).To(Succeed())
		Expect(os.WriteFile(configFile, configContent, 0644)).To(Succeed())

		router := newTestSmartRouter(registry, nodes.SmartRouterOptions{FileStager: stager})

		// Route with ModelFile pointing to the .onnx file (triggers model staging)
		result, err := router.Route(ctx, "voice-it-paola-medium", "it-paola-medium.onnx", "piper", &pb.ModelOptions{
			Model:     "it-paola-medium.onnx",
			ModelFile: modelFile,
		}, false)
		Expect(err).ToNot(HaveOccurred())
		defer result.Release()

		localOutputDir := GinkgoT().TempDir()
		frontendDst := filepath.Join(localOutputDir, "speech.wav")

		// Simulate what core/backend/tts.go does: construct Model path using frontend ModelPath
		frontendModelPath := filepath.Join(frontendModelsDir, "it-paola-medium.onnx")

		ttsResult, err := result.Client.TTS(ctx, &pb.TTSRequest{
			Text:  "Hello world",
			Model: frontendModelPath, // frontend absolute path — should be translated to remote
			Dst:   frontendDst,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(ttsResult.Success).To(BeTrue())

		// Verify: the backend received the remote worker path, NOT the frontend path
		Expect(llm.lastTTSModel).ToNot(Equal(frontendModelPath))
		// The remote path should be under the worker's models dir with the tracking key
		Expect(llm.lastTTSModel).To(ContainSubstring("voice-it-paola-medium"))
		Expect(llm.lastTTSModel).To(HaveSuffix("it-paola-medium.onnx"))

		// Verify the model file exists at the translated path (already staged during LoadModel)
		stagedModelData, err := os.ReadFile(llm.lastTTSModel)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedModelData).To(Equal(modelContent))

		// Verify the companion .onnx.json is next to it (staged during LoadModel)
		companionPath := llm.lastTTSModel + ".json"
		stagedConfigData, err := os.ReadFile(companionPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(stagedConfigData).To(Equal(configContent))

		// Verify output: audio was pulled back
		retrievedData, err := os.ReadFile(frontendDst)
		Expect(err).ToNot(HaveOccurred())
		Expect(retrievedData).To(Equal(outputData))
	})

	It("should stage companion .onnx.json files alongside .onnx model files", func() {
		llm := &testLLM{}
		modelsDir := GinkgoT().TempDir()
		stagingDir := GinkgoT().TempDir()
		dataDir := GinkgoT().TempDir()
		addr, cleanupGRPC, err := startTestGRPCServer(llm)
		Expect(err).ToNot(HaveOccurred())
		defer cleanupGRPC()

		// Use the real file transfer server (preserves directory structure)
		httpLis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		httpAddr := httpLis.Addr().String()
		httpServer, err := nodes.StartFileTransferServerWithListener(httpLis, stagingDir, modelsDir, dataDir, "", 0)
		Expect(err).ToNot(HaveOccurred())
		defer nodes.ShutdownFileTransferServer(httpServer)

		node := &nodes.BackendNode{Name: "companion-node", Address: addr, HTTPAddress: httpAddr}
		Expect(registry.Register(context.Background(), node, true)).To(Succeed())

		stager := nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			n, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			return n.HTTPAddress, nil
		}, "")

		// Create model files: .onnx and .onnx.json in a temp "models" dir
		frontendModelsDir := GinkgoT().TempDir()
		modelContent := []byte("fake onnx model")
		configContent := []byte(`{"audio":{"sample_rate":22050}}`)
		modelFile := filepath.Join(frontendModelsDir, "my-model.onnx")
		configFile := filepath.Join(frontendModelsDir, "my-model.onnx.json")
		Expect(os.WriteFile(modelFile, modelContent, 0644)).To(Succeed())
		Expect(os.WriteFile(configFile, configContent, 0644)).To(Succeed())

		router := newTestSmartRouter(registry, nodes.SmartRouterOptions{FileStager: stager})

		// Route with ModelFile pointing to the .onnx file
		result, err := router.Route(ctx, "piper-companion-test", "my-model.onnx", "piper", &pb.ModelOptions{
			Model:     "my-model.onnx",
			ModelFile: modelFile,
		}, false)
		Expect(err).ToNot(HaveOccurred())
		defer result.Release()

		// Verify: both .onnx and .onnx.json were staged to the worker's models dir
		stagedOnnx := filepath.Join(modelsDir, "piper-companion-test", "my-model.onnx")
		stagedConfig := filepath.Join(modelsDir, "piper-companion-test", "my-model.onnx.json")

		stagedOnnxData, err := os.ReadFile(stagedOnnx)
		Expect(err).ToNot(HaveOccurred(), "companion .onnx model should be staged")
		Expect(stagedOnnxData).To(Equal(modelContent))

		stagedConfigData, err := os.ReadFile(stagedConfig)
		Expect(err).ToNot(HaveOccurred(), "companion .onnx.json config should be staged alongside model")
		Expect(stagedConfigData).To(Equal(configContent))
	})
})
