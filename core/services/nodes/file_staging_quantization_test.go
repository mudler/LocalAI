package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	ggrpc "google.golang.org/grpc"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type quantizationTestBackend struct {
	lifecycleBackend
	mu                  sync.Mutex
	model               string
	progress            *pb.QuantizationProgressUpdate
	readModelOnProgress bool
}

type exportDirectoryBackend struct {
	lifecycleBackend
	writeOutput bool
}

func (b *exportDirectoryBackend) ExportModel(_ context.Context, in *pb.ExportModelRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	if b.writeOutput {
		if err := osWriteFile(filepath.Join(in.OutputPath, "nested", "fresh.bin"), []byte("fresh")); err != nil {
			return nil, err
		}
		return &pb.Result{Success: true}, nil
	}
	return &pb.Result{Success: false, Message: "stop after allocation"}, nil
}

func (b *quantizationTestBackend) StartQuantization(_ context.Context, in *pb.QuantizationRequest, _ ...ggrpc.CallOption) (*pb.QuantizationJobResult, error) {
	b.mu.Lock()
	b.model = in.Model
	b.mu.Unlock()
	return &pb.QuantizationJobResult{JobId: in.JobId, Success: true}, nil
}

func (b *quantizationTestBackend) QuantizationProgress(_ context.Context, _ *pb.QuantizationProgressRequest, fn func(*pb.QuantizationProgressUpdate), _ ...ggrpc.CallOption) error {
	if b.readModelOnProgress {
		if _, err := os.ReadFile(b.model); err != nil {
			return err
		}
	}
	fn(b.progress)
	return nil
}

func (b *quantizationTestBackend) StopQuantization(_ context.Context, _ *pb.QuantizationStopRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

type quantizationTestStager struct {
	lifecycleStager
	mu            sync.Mutex
	remoteDir     string
	allocatedKeys []string
	fetchErr      error
	fetchCalls    int
	releasedDirs  []string
}

func (s *quantizationTestStager) EnsureRemote(_ context.Context, _, localPath, key string) (string, error) {
	s.ensureCalls = append(s.ensureCalls, ensureCall{key: key})
	dst := filepath.Join(s.remoteDir, filepath.FromSlash(key))
	if err := copyFile(localPath, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func (s *quantizationTestStager) AllocRemoteDir(_ context.Context, _ string, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allocatedKeys = append(s.allocatedKeys, key)
	return filepath.Join(s.remoteDir, filepath.FromSlash(key)), nil
}

func (s *quantizationTestStager) FetchRemoteByKey(_ context.Context, _, key, dst string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchCalls++
	if s.fetchErr != nil {
		return s.fetchErr
	}
	src := filepath.Join(s.remoteDir, filepath.FromSlash(key))
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		src = filepath.Join(s.remoteDir, "result.gguf")
	}
	return copyFile(src, dst)
}

func (s *quantizationTestStager) ReleaseRemoteDir(_ context.Context, _ string, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releasedDirs = append(s.releasedDirs, key)
	return nil
}

func (s *quantizationTestStager) ListRemoteDir(_ context.Context, _ string, key string) ([]string, error) {
	root := filepath.Join(s.remoteDir, filepath.FromSlash(key))
	var files []string
	err := filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

func openQuantizationStagingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&QuantizationStagingRecord{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestQuantizationStagingSurvivesClientRecreation(t *testing.T) {
	db := openQuantizationStagingTestDB(t)
	frontendA := t.TempDir()
	frontendB := t.TempDir()
	worker := t.TempDir()
	input := filepath.Join(frontendA, "model.gguf")
	if err := osWriteFile(input, []byte("model")); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(filepath.Join(worker, "result.gguf"), []byte("result")); err != nil {
		t.Fatal(err)
	}
	backend := &quantizationTestBackend{readModelOnProgress: true}
	stager := &quantizationTestStager{remoteDir: worker}
	first := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendA})
	job, err := first.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "job-1", Model: input, OutputDir: filepath.Join(frontendA, "quantization", "job-1")})
	if err != nil || !job.Success {
		t.Fatalf("start: %#v %v", job, err)
	}
	if len(stager.releasedKeys) != 0 {
		t.Fatalf("async input released at start: %v", stager.releasedKeys)
	}
	var record QuantizationStagingRecord
	if err := db.First(&record, "node_id = ? AND job_id = ?", "worker-1", "job-1").Error; err != nil {
		t.Fatal(err)
	}
	if record.FrontendDir != "" || record.DataRelativeDir != filepath.Join("quantization", "job-1") {
		t.Fatalf("durable state contains a replica-local destination: %#v", record)
	}
	backend.progress = &pb.QuantizationProgressUpdate{JobId: "job-1", Status: "completed", OutputFile: filepath.Join(worker, filepath.FromSlash(stager.allocatedKeys[0]), "nested", "result.gguf")}
	second := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendB})
	var got *pb.QuantizationProgressUpdate
	if err := second.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "job-1"}, func(update *pb.QuantizationProgressUpdate) { got = update }); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(frontendB, "quantization", "job-1", "nested", "result.gguf")
	if got == nil || got.Status != "completed" || got.OutputFile != want {
		t.Fatalf("progress = %#v, want completed output %q", got, want)
	}
	if len(stager.releasedKeys) == 0 || len(stager.releasedDirs) != 1 {
		t.Fatalf("terminal cleanup missing: keys=%v dirs=%v", stager.releasedKeys, stager.releasedDirs)
	}
}

func TestQuantizationProgressRejectsReplicaSymlinkEscape(t *testing.T) {
	db := openQuantizationStagingTestDB(t)
	frontendA := t.TempDir()
	frontendB := t.TempDir()
	outside := t.TempDir()
	worker := t.TempDir()
	input := filepath.Join(frontendA, "model.gguf")
	if err := osWriteFile(input, []byte("model")); err != nil {
		t.Fatal(err)
	}
	backend := &quantizationTestBackend{}
	stager := &quantizationTestStager{remoteDir: worker}
	first := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendA})
	if _, err := first.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "replica-escape", Model: input, OutputDir: filepath.Join(frontendA, "quantization", "replica-escape")}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(frontendB, "quantization"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(frontendB, "quantization", "replica-escape")); err != nil {
		t.Fatal(err)
	}
	backend.progress = &pb.QuantizationProgressUpdate{JobId: "replica-escape", Status: "completed", OutputFile: filepath.Join(worker, filepath.FromSlash(stager.allocatedKeys[0]), "result.gguf")}
	second := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendB})
	var got *pb.QuantizationProgressUpdate
	if err := second.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "replica-escape"}, func(update *pb.QuantizationProgressUpdate) { got = update }); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Status == "completed" || got.OutputFile != "" {
		t.Fatalf("replica symlink escape reported success: %#v", got)
	}
	if stager.fetchCalls != 0 || len(stager.releasedKeys) != 0 || len(stager.releasedDirs) != 0 {
		t.Fatalf("unsafe destination fetched or destroyed retry state: fetches=%d keys=%v dirs=%v", stager.fetchCalls, stager.releasedKeys, stager.releasedDirs)
	}
}

func TestQuantizationFetchFailureIsRetryableAndTruthful(t *testing.T) {
	db := openQuantizationStagingTestDB(t)
	root := t.TempDir()
	worker := t.TempDir()
	input := filepath.Join(root, "model.gguf")
	if err := osWriteFile(input, []byte("model")); err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(filepath.Join(worker, "result.gguf"), []byte("result")); err != nil {
		t.Fatal(err)
	}
	backend := &quantizationTestBackend{}
	stager := &quantizationTestStager{remoteDir: worker, fetchErr: errors.New("first fetch failed")}
	client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
	_, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "job-2", Model: input, OutputDir: filepath.Join(root, "quantization", "job-2")})
	if err != nil {
		t.Fatal(err)
	}
	backend.progress = &pb.QuantizationProgressUpdate{JobId: "job-2", Status: "completed", OutputFile: filepath.Join(worker, filepath.FromSlash(stager.allocatedKeys[0]), "nested", "result.gguf")}
	var first *pb.QuantizationProgressUpdate
	if err := client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "job-2"}, func(update *pb.QuantizationProgressUpdate) { first = update }); err != nil {
		t.Fatal(err)
	}
	if first == nil || first.Status == "completed" || first.OutputFile != "" {
		t.Fatalf("fetch failure reported success: %#v", first)
	}
	if len(stager.releasedKeys) != 0 || len(stager.releasedDirs) != 0 {
		t.Fatalf("failed fetch destroyed retry state: keys=%v dirs=%v", stager.releasedKeys, stager.releasedDirs)
	}
	stager.mu.Lock()
	stager.fetchErr = nil
	stager.mu.Unlock()
	var retry *pb.QuantizationProgressUpdate
	if err := client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "job-2"}, func(update *pb.QuantizationProgressUpdate) { retry = update }); err != nil {
		t.Fatal(err)
	}
	if retry == nil || retry.Status != "completed" {
		t.Fatalf("retry did not complete: %#v", retry)
	}
}

func TestExportAllocatesUniqueRemoteDirectories(t *testing.T) {
	backend := &exportDirectoryBackend{}
	stager := &quantizationTestStager{remoteDir: t.TempDir()}
	client := NewFileStagingClient(backend, stager, "worker-1")
	outputs := []string{filepath.Join(t.TempDir(), "same-name"), filepath.Join(t.TempDir(), "same-name")}
	errs := make(chan error, len(outputs))
	for _, out := range outputs {
		go func() {
			_, err := client.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: out})
			errs <- err
		}()
	}
	for range outputs {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	stager.mu.Lock()
	defer stager.mu.Unlock()
	if len(stager.allocatedKeys) != 2 || stager.allocatedKeys[0] == stager.allocatedKeys[1] {
		t.Fatalf("remote export directories collided: %v", stager.allocatedKeys)
	}
}

func TestExportExcludesStaleContentsFromPreviousDirectory(t *testing.T) {
	remote := t.TempDir()
	if err := osWriteFile(filepath.Join(remote, "models", "exports", "same-name", "stale.bin"), []byte("stale")); err != nil {
		t.Fatal(err)
	}
	stager := &quantizationTestStager{remoteDir: remote}
	client := NewFileStagingClient(&exportDirectoryBackend{writeOutput: true}, stager, "worker-1")
	output := filepath.Join(t.TempDir(), "same-name")
	result, err := client.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: output})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("export: result=%#v err=%v", result, err)
	}
	if got, err := os.ReadFile(filepath.Join(output, "nested", "fresh.bin")); err != nil || string(got) != "fresh" {
		t.Fatalf("fresh output: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(output, "stale.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale output was fetched: %v", err)
	}
}

func TestConcurrentQuantizationsAllocateUniqueRemoteDirectories(t *testing.T) {
	backend := &quantizationTestBackend{}
	stager := &quantizationTestStager{remoteDir: t.TempDir()}
	client := NewFileStagingClient(backend, stager, "worker-1")
	outputs := []string{filepath.Join(t.TempDir(), "same-name"), filepath.Join(t.TempDir(), "same-name")}
	errs := make(chan error, len(outputs))
	for _, output := range outputs {
		go func() {
			_, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "same-job", OutputDir: output})
			errs <- err
		}()
	}
	for range outputs {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	stager.mu.Lock()
	defer stager.mu.Unlock()
	if len(stager.allocatedKeys) != 2 || stager.allocatedKeys[0] == stager.allocatedKeys[1] {
		t.Fatalf("remote quantization directories collided: %v", stager.allocatedKeys)
	}
}

func TestQuantizationWithoutOutputRetainsInputUntilTerminalProgress(t *testing.T) {
	db := openQuantizationStagingTestDB(t)
	root := t.TempDir()
	input := filepath.Join(root, "model.gguf")
	if err := osWriteFile(input, []byte("model")); err != nil {
		t.Fatal(err)
	}
	backend := &quantizationTestBackend{
		readModelOnProgress: true,
		progress:            &pb.QuantizationProgressUpdate{JobId: "input-only", Status: "failed"},
	}
	stager := &quantizationTestStager{remoteDir: t.TempDir()}
	client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
	if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "input-only", Model: input}); err != nil {
		t.Fatal(err)
	}
	if len(stager.releasedKeys) != 0 {
		t.Fatalf("async input released at start: %v", stager.releasedKeys)
	}
	if err := client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "input-only"}, func(*pb.QuantizationProgressUpdate) {}); err != nil {
		t.Fatal(err)
	}
	if len(stager.releasedKeys) == 0 {
		t.Fatal("terminal progress did not release staged input")
	}
	if _, ok, err := client.lookupQuantization(context.Background(), "input-only"); err != nil || ok {
		t.Fatalf("terminal progress retained staging record: ok=%v err=%v", ok, err)
	}
}

func TestQuantizationCleanupFailureRetainsDurableStateForRetry(t *testing.T) {
	db := openQuantizationStagingTestDB(t)
	root := t.TempDir()
	input := filepath.Join(root, "model.gguf")
	if err := osWriteFile(input, []byte("model")); err != nil {
		t.Fatal(err)
	}
	backend := &quantizationTestBackend{progress: &pb.QuantizationProgressUpdate{JobId: "cleanup-retry", Status: "failed"}}
	stager := &quantizationTestStager{remoteDir: t.TempDir()}
	stager.releaseErr = errors.New("temporary cleanup failure")
	client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
	if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "cleanup-retry", Model: input}); err != nil {
		t.Fatal(err)
	}
	progress := func() error {
		return client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "cleanup-retry"}, func(*pb.QuantizationProgressUpdate) {})
	}
	if err := progress(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := client.lookupQuantization(context.Background(), "cleanup-retry"); err != nil || !ok {
		t.Fatalf("cleanup failure discarded durable state: ok=%v err=%v", ok, err)
	}
	stager.releaseErr = nil
	if err := progress(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := client.lookupQuantization(context.Background(), "cleanup-retry"); err != nil || ok {
		t.Fatalf("cleanup retry retained durable state: ok=%v err=%v", ok, err)
	}
}

func TestStopQuantizationCleansStagedInputAndDurableState(t *testing.T) {
	db := openQuantizationStagingTestDB(t)
	root := t.TempDir()
	input := filepath.Join(root, "model.gguf")
	if err := osWriteFile(input, []byte("model")); err != nil {
		t.Fatal(err)
	}
	stager := &quantizationTestStager{remoteDir: t.TempDir()}
	client := NewFileStagingClientWithOptions(&quantizationTestBackend{}, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
	if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "stop-cleanup", Model: input, OutputDir: filepath.Join(root, "quantization", "stop-cleanup")}); err != nil {
		t.Fatal(err)
	}
	result, err := client.StopQuantization(context.Background(), &pb.QuantizationStopRequest{JobId: "stop-cleanup"})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("stop: result=%#v err=%v", result, err)
	}
	if len(stager.releasedKeys) == 0 || len(stager.releasedDirs) != 1 {
		t.Fatalf("stop cleanup missing: keys=%v dirs=%v", stager.releasedKeys, stager.releasedDirs)
	}
	if _, ok, err := client.lookupQuantization(context.Background(), "stop-cleanup"); err != nil || ok {
		t.Fatalf("stop retained durable staging state: ok=%v err=%v", ok, err)
	}
}

func TestQuantizationRejectsOutputOutsideReplicaDataPath(t *testing.T) {
	db := openQuantizationStagingTestDB(t)
	dataPath := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataPath, "escape")); err != nil {
		t.Fatal(err)
	}
	client := NewFileStagingClientWithOptions(&quantizationTestBackend{}, &quantizationTestStager{remoteDir: t.TempDir()}, "worker-1", FileStagingClientOptions{DB: db, DataPath: dataPath})
	for _, output := range []string{outside, filepath.Join(dataPath, "..", filepath.Base(outside)), filepath.Join(dataPath, "escape", "job")} {
		_, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "unsafe-" + filepath.Base(output), OutputDir: output})
		if err == nil {
			t.Fatalf("accepted output outside data path: %q", output)
		}
	}
}

// osWriteFile keeps setup errors explicit while using production-safe modes.
func osWriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
