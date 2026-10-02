package nodes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func failQuantizationStaging(format string, args ...any) {
	GinkgoHelper()
	Fail(fmt.Sprintf(format, args...))
}

type quantizationTestBackend struct {
	lifecycleBackend
	mu                  sync.Mutex
	model               string
	progress            *pb.QuantizationProgressUpdate
	readModelOnProgress bool
	startErr            error
	stopErr             error
}

type exportDirectoryBackend struct {
	lifecycleBackend
	writeOutput bool
}

type successfulExportBackend struct{ lifecycleBackend }

func (b *successfulExportBackend) ExportModel(context.Context, *pb.ExportModelRequest, ...ggrpc.CallOption) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
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
	if b.startErr != nil {
		return nil, b.startErr
	}
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
	if b.stopErr != nil {
		return nil, b.stopErr
	}
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
	releaseDirErr error
	listedFiles   []string
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
	return s.releaseDirErr
}

func (s *quantizationTestStager) ListRemoteDir(_ context.Context, _ string, key string) ([]string, error) {
	if s.listedFiles != nil {
		return append([]string(nil), s.listedFiles...), nil
	}
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

func openQuantizationStagingTestDB() *gorm.DB {
	GinkgoHelper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "quantization.db")), &gorm.Config{})
	if err != nil {
		failQuantizationStaging("%v", err)
	}
	if err := db.AutoMigrate(&QuantizationStagingRecord{}); err != nil {
		failQuantizationStaging("%v", err)
	}
	if err := db.Exec("DELETE FROM quantization_staging_records").Error; err != nil {
		failQuantizationStaging("%v", err)
	}
	return db
}

var _ = Describe("File staging quantization", func() {
	It("QuantizationStagingSurvivesClientRecreation", func() {
		db := openQuantizationStagingTestDB()
		frontendA := GinkgoT().TempDir()
		frontendB := GinkgoT().TempDir()
		worker := GinkgoT().TempDir()
		input := filepath.Join(frontendA, "model.gguf")
		if err := osWriteFile(input, []byte("model")); err != nil {
			failQuantizationStaging("%v", err)
		}
		if err := osWriteFile(filepath.Join(worker, "result.gguf"), []byte("result")); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend := &quantizationTestBackend{readModelOnProgress: true}
		stager := &quantizationTestStager{remoteDir: worker}
		first := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendA})
		job, err := first.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "job-1", Model: input, OutputDir: filepath.Join(frontendA, "quantization", "job-1")})
		if err != nil || !job.Success {
			failQuantizationStaging("start: %#v %v", job, err)
		}
		if len(stager.releasedKeys) != 0 {
			failQuantizationStaging("async input released at start: %v", stager.releasedKeys)
		}
		var record QuantizationStagingRecord
		if err := db.First(&record, "node_id = ? AND job_id = ?", "worker-1", "job-1").Error; err != nil {
			failQuantizationStaging("%v", err)
		}
		if record.FrontendDir != "" || record.DataRelativeDir != filepath.Join("quantization", "job-1") {
			failQuantizationStaging("durable state contains a replica-local destination: %#v", record)
		}
		backend.progress = &pb.QuantizationProgressUpdate{JobId: "job-1", Status: "completed", OutputFile: filepath.Join(worker, filepath.FromSlash(stager.allocatedKeys[0]), "nested", "result.gguf")}
		second := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendB})
		var got *pb.QuantizationProgressUpdate
		if err := second.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "job-1"}, func(update *pb.QuantizationProgressUpdate) { got = update }); err != nil {
			failQuantizationStaging("%v", err)
		}
		want := filepath.Join(frontendB, "quantization", "job-1", "nested", "result.gguf")
		if got == nil || got.Status != "completed" || got.OutputFile != want {
			failQuantizationStaging("progress = %#v, want completed output %q", got, want)
		}
		if len(stager.releasedKeys) == 0 || len(stager.releasedDirs) != 1 {
			failQuantizationStaging("terminal cleanup missing: keys=%v dirs=%v", stager.releasedKeys, stager.releasedDirs)
		}
	})

	It("QuantizationProgressRejectsReplicaSymlinkEscape", func() {
		db := openQuantizationStagingTestDB()
		frontendA := GinkgoT().TempDir()
		frontendB := GinkgoT().TempDir()
		outside := GinkgoT().TempDir()
		worker := GinkgoT().TempDir()
		input := filepath.Join(frontendA, "model.gguf")
		if err := osWriteFile(input, []byte("model")); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend := &quantizationTestBackend{}
		stager := &quantizationTestStager{remoteDir: worker}
		first := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendA})
		if _, err := first.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "replica-escape", Model: input, OutputDir: filepath.Join(frontendA, "quantization", "replica-escape")}); err != nil {
			failQuantizationStaging("%v", err)
		}
		if err := os.MkdirAll(filepath.Join(frontendB, "quantization"), 0o750); err != nil {
			failQuantizationStaging("%v", err)
		}
		if err := os.Symlink(outside, filepath.Join(frontendB, "quantization", "replica-escape")); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend.progress = &pb.QuantizationProgressUpdate{JobId: "replica-escape", Status: "completed", OutputFile: filepath.Join(worker, filepath.FromSlash(stager.allocatedKeys[0]), "result.gguf")}
		second := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: frontendB})
		var got *pb.QuantizationProgressUpdate
		if err := second.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "replica-escape"}, func(update *pb.QuantizationProgressUpdate) { got = update }); err != nil {
			failQuantizationStaging("%v", err)
		}
		if got == nil || got.Status == "completed" || got.OutputFile != "" {
			failQuantizationStaging("replica symlink escape reported success: %#v", got)
		}
		if stager.fetchCalls != 0 || len(stager.releasedKeys) != 0 || len(stager.releasedDirs) != 0 {
			failQuantizationStaging("unsafe destination fetched or destroyed retry state: fetches=%d keys=%v dirs=%v", stager.fetchCalls, stager.releasedKeys, stager.releasedDirs)
		}
	})

	It("QuantizationFetchFailureIsRetryableAndTruthful", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		worker := GinkgoT().TempDir()
		input := filepath.Join(root, "model.gguf")
		if err := osWriteFile(input, []byte("model")); err != nil {
			failQuantizationStaging("%v", err)
		}
		if err := osWriteFile(filepath.Join(worker, "result.gguf"), []byte("result")); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend := &quantizationTestBackend{}
		stager := &quantizationTestStager{remoteDir: worker, fetchErr: errors.New("first fetch failed")}
		client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		_, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "job-2", Model: input, OutputDir: filepath.Join(root, "quantization", "job-2")})
		if err != nil {
			failQuantizationStaging("%v", err)
		}
		backend.progress = &pb.QuantizationProgressUpdate{JobId: "job-2", Status: "completed", OutputFile: filepath.Join(worker, filepath.FromSlash(stager.allocatedKeys[0]), "nested", "result.gguf")}
		var first *pb.QuantizationProgressUpdate
		if err := client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "job-2"}, func(update *pb.QuantizationProgressUpdate) { first = update }); err != nil {
			failQuantizationStaging("%v", err)
		}
		if first == nil || first.Status == "completed" || first.OutputFile != "" {
			failQuantizationStaging("fetch failure reported success: %#v", first)
		}
		if len(stager.releasedKeys) != 0 || len(stager.releasedDirs) != 0 {
			failQuantizationStaging("failed fetch destroyed retry state: keys=%v dirs=%v", stager.releasedKeys, stager.releasedDirs)
		}
		stager.mu.Lock()
		stager.fetchErr = nil
		stager.mu.Unlock()
		var retry *pb.QuantizationProgressUpdate
		if err := client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "job-2"}, func(update *pb.QuantizationProgressUpdate) { retry = update }); err != nil {
			failQuantizationStaging("%v", err)
		}
		if retry == nil || retry.Status != "completed" {
			failQuantizationStaging("retry did not complete: %#v", retry)
		}
	})

	It("ExportAllocatesUniqueRemoteDirectories", func() {
		backend := &exportDirectoryBackend{}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir()}
		client := NewFileStagingClient(backend, stager, "worker-1")
		outputs := []string{filepath.Join(GinkgoT().TempDir(), "same-name"), filepath.Join(GinkgoT().TempDir(), "same-name")}
		errs := make(chan error, len(outputs))
		for _, out := range outputs {
			go func() {
				_, err := client.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: out})
				errs <- err
			}()
		}
		for range outputs {
			if err := <-errs; err != nil {
				failQuantizationStaging("%v", err)
			}
		}
		stager.mu.Lock()
		defer stager.mu.Unlock()
		if len(stager.allocatedKeys) != 2 || stager.allocatedKeys[0] == stager.allocatedKeys[1] {
			failQuantizationStaging("remote export directories collided: %v", stager.allocatedKeys)
		}
	})

	It("ExportExcludesStaleContentsFromPreviousDirectory", func() {
		remote := GinkgoT().TempDir()
		if err := osWriteFile(filepath.Join(remote, "models", "exports", "same-name", "stale.bin"), []byte("stale")); err != nil {
			failQuantizationStaging("%v", err)
		}
		stager := &quantizationTestStager{remoteDir: remote}
		client := NewFileStagingClient(&exportDirectoryBackend{writeOutput: true}, stager, "worker-1")
		output := filepath.Join(GinkgoT().TempDir(), "same-name")
		result, err := client.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: output})
		if err != nil || result == nil || !result.Success {
			failQuantizationStaging("export: result=%#v err=%v", result, err)
		}
		if got, err := os.ReadFile(filepath.Join(output, "nested", "fresh.bin")); err != nil || string(got) != "fresh" {
			failQuantizationStaging("fresh output: %q err=%v", got, err)
		}
		if _, err := os.Stat(filepath.Join(output, "stale.bin")); !errors.Is(err, os.ErrNotExist) {
			failQuantizationStaging("stale output was fetched: %v", err)
		}
	})

	It("ConcurrentQuantizationsAllocateUniqueRemoteDirectories", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		backend := &quantizationTestBackend{}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir()}
		client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		outputs := []string{filepath.Join(root, "first"), filepath.Join(root, "second")}
		errs := make(chan error, len(outputs))
		for _, output := range outputs {
			go func() {
				_, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "same-job", OutputDir: output})
				errs <- err
			}()
		}
		successes := 0
		for range outputs {
			if err := <-errs; err == nil {
				successes++
			}
		}
		if successes != 1 {
			failQuantizationStaging("duplicate starts succeeded %d times, want 1", successes)
		}
		stager.mu.Lock()
		defer stager.mu.Unlock()
		if len(stager.allocatedKeys) != 2 || stager.allocatedKeys[0] == stager.allocatedKeys[1] {
			failQuantizationStaging("remote quantization directories collided: %v", stager.allocatedKeys)
		}
		if len(stager.releasedDirs) != 1 {
			failQuantizationStaging("duplicate loser cleanup = %v, want exactly one directory", stager.releasedDirs)
		}
		var record QuantizationStagingRecord
		if err := db.First(&record, "node_id = ? AND job_id = ?", "worker-1", "same-job").Error; err != nil {
			failQuantizationStaging("%v", err)
		}
		if record.KeyPrefix == stager.releasedDirs[0] {
			failQuantizationStaging("duplicate loser removed winner directory %q", record.KeyPrefix)
		}
	})

	It("QuantizationValidatesDurableOutputBeforeMutation", func() {
		db := openQuantizationStagingTestDB()
		dataPath := GinkgoT().TempDir()
		outsideParent := GinkgoT().TempDir()
		outside := filepath.Join(outsideParent, "must-not-exist")
		client := NewFileStagingClientWithOptions(&quantizationTestBackend{}, &quantizationTestStager{remoteDir: GinkgoT().TempDir()}, "worker-1", FileStagingClientOptions{DB: db, DataPath: dataPath})
		if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "outside", OutputDir: outside}); err == nil {
			failQuantizationStaging("accepted outside durable destination")
		}
		if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
			failQuantizationStaging("rejected destination was mutated: %v", err)
		}

		emptyDataPath := NewFileStagingClientWithOptions(&quantizationTestBackend{}, &quantizationTestStager{remoteDir: GinkgoT().TempDir()}, "worker-1", FileStagingClientOptions{DB: db})
		if _, err := emptyDataPath.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "absolute", OutputDir: filepath.Join(GinkgoT().TempDir(), "out")}); err == nil {
			failQuantizationStaging("durable staging accepted an empty DataPath")
		}
	})

	It("StartFailureCleanupFailureRetainsDurableState", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir(), releaseDirErr: errors.New("rmdir unavailable")}
		client := NewFileStagingClientWithOptions(&quantizationTestBackend{startErr: errors.New("start failed")}, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		_, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "start-failed", OutputDir: filepath.Join(root, "out")})
		if err == nil {
			failQuantizationStaging("expected start failure")
		}
		if _, ok, lookupErr := client.lookupQuantization(context.Background(), "start-failed"); lookupErr != nil || !ok {
			failQuantizationStaging("cleanup failure lost durable state: ok=%v err=%v", ok, lookupErr)
		}
		stager.releaseDirErr = nil
		if _, err := client.StopQuantization(context.Background(), &pb.QuantizationStopRequest{JobId: "start-failed"}); err != nil {
			failQuantizationStaging("retrying cleanup: %v", err)
		}
		if _, ok, lookupErr := client.lookupQuantization(context.Background(), "start-failed"); lookupErr != nil || ok {
			failQuantizationStaging("cleanup retry retained durable state: ok=%v err=%v", ok, lookupErr)
		}
	})

	It("QuantizationStagingGenerationRejectsStaleReplicaMutation", func() {
		db := openQuantizationStagingTestDB()
		store := gormQuantizationStagingStore{db: db}
		old := &QuantizationStagingRecord{NodeID: "worker-1", JobID: "reused", Generation: "old", KeyPrefix: "data/old"}
		if err := store.Create(context.Background(), old); err != nil {
			failQuantizationStaging("%v", err)
		}
		if err := store.Delete(context.Background(), old.NodeID, old.JobID, old.Generation); err != nil {
			failQuantizationStaging("%v", err)
		}
		newRecord := &QuantizationStagingRecord{NodeID: "worker-1", JobID: "reused", Generation: "new", KeyPrefix: "data/new"}
		if err := store.Create(context.Background(), newRecord); err != nil {
			failQuantizationStaging("%v", err)
		}
		old.OutputReleased = true
		if err := store.Update(context.Background(), old); !errors.Is(err, ErrQuantizationStagingOwnership) {
			failQuantizationStaging("stale update error = %v", err)
		}
		if err := store.Delete(context.Background(), old.NodeID, old.JobID, old.Generation); !errors.Is(err, ErrQuantizationStagingOwnership) {
			failQuantizationStaging("stale delete error = %v", err)
		}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir()}
		client := NewFileStagingClientWithOptions(&quantizationTestBackend{}, stager, old.NodeID, FileStagingClientOptions{DB: db, DataPath: GinkgoT().TempDir()})
		client.cleanupQuantization(old.JobID, quantizationOutput{
			generation: old.Generation, keyPrefix: old.KeyPrefix, inputsReleased: true,
		})
		got, ok, err := store.Get(context.Background(), newRecord.NodeID, newRecord.JobID)
		if err != nil || !ok || got.Generation != "new" || got.KeyPrefix != "data/new" {
			failQuantizationStaging("new generation changed by stale owner: got=%#v ok=%v err=%v", got, ok, err)
		}
		if len(stager.releasedDirs) != 1 || stager.releasedDirs[0] != "data/old" {
			failQuantizationStaging("stale owner released another generation's resources: %v", stager.releasedDirs)
		}
	})

	It("RetryStartCleansFailedStartWithoutBackendJob", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		backend := &quantizationTestBackend{startErr: errors.New("start failed"), stopErr: errors.New("job does not exist")}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir(), releaseDirErr: errors.New("first cleanup failed")}
		client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		request := &pb.QuantizationRequest{JobId: "retry-start", OutputDir: filepath.Join(root, "out")}
		if _, err := client.StartQuantization(context.Background(), request); err == nil {
			failQuantizationStaging("expected backend start failure")
		}
		old, ok, err := client.lookupQuantization(context.Background(), request.JobId)
		if err != nil || !ok || !old.cleanupPending {
			failQuantizationStaging("failed start did not retain cleanup ownership: %#v ok=%v err=%v", old, ok, err)
		}
		if _, err := client.StopQuantization(context.Background(), &pb.QuantizationStopRequest{JobId: request.JobId}); err == nil {
			failQuantizationStaging("test backend unexpectedly accepted stop for nonexistent job")
		}
		stager.releaseDirErr = nil
		backend.startErr = nil
		job, err := client.StartQuantization(context.Background(), request)
		if err != nil || job == nil || !job.Success {
			failQuantizationStaging("retry start did not clean old resources and start: job=%#v err=%v", job, err)
		}
		current, ok, err := client.lookupQuantization(context.Background(), request.JobId)
		if err != nil || !ok || current.generation == old.generation || current.cleanupPending {
			failQuantizationStaging("retry did not establish a fresh owner: old=%#v current=%#v ok=%v err=%v", old, current, ok, err)
		}
		if len(stager.releasedDirs) != 2 || stager.releasedDirs[0] != old.keyPrefix || stager.releasedDirs[1] != old.keyPrefix {
			failQuantizationStaging("retry released wrong generation resources: %v, old=%#v", stager.releasedDirs, old)
		}
	})

	It("QuantizationDeleteFailureRetriesWithoutRefetch", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		worker := GinkgoT().TempDir()
		ExpectDeleteFailure := errors.New("delete unavailable")
		if err := db.Callback().Delete().Before("gorm:delete").Register("test:fail-delete", func(tx *gorm.DB) { _ = tx.AddError(ExpectDeleteFailure) }); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend := &quantizationTestBackend{}
		stager := &quantizationTestStager{remoteDir: worker}
		client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "delete-retry", OutputDir: filepath.Join(root, "out")}); err != nil {
			failQuantizationStaging("%v", err)
		}
		if err := osWriteFile(filepath.Join(worker, "result.gguf"), []byte("result")); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend.progress = &pb.QuantizationProgressUpdate{JobId: "delete-retry", Status: "completed", OutputFile: filepath.Join(worker, filepath.FromSlash(stager.allocatedKeys[0]), "result.gguf")}
		progress := func() *pb.QuantizationProgressUpdate {
			var got *pb.QuantizationProgressUpdate
			if err := client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "delete-retry"}, func(update *pb.QuantizationProgressUpdate) { got = update }); err != nil {
				failQuantizationStaging("%v", err)
			}
			return got
		}
		if got := progress(); got == nil || got.Status != "completed" {
			failQuantizationStaging("first progress: %#v", got)
		}
		if stager.fetchCalls != 1 {
			failQuantizationStaging("fetch calls = %d", stager.fetchCalls)
		}
		Expect(db.Callback().Delete().Remove("test:fail-delete")).To(Succeed())
		if got := progress(); got == nil || got.Status != "completed" {
			failQuantizationStaging("retry progress: %#v", got)
		}
		if stager.fetchCalls != 1 {
			failQuantizationStaging("cleanup retry refetched deleted output: %d", stager.fetchCalls)
		}
	})

	It("ExportRejectsUntrustedRemoteEntries", func() {
		for _, malicious := range []string{
			"../escape", "/absolute", `C:\\absolute`, `nested\\..\\escape`,
			"nested/../../escape", "nested//escape", "nested/./escape",
		} {
			root := GinkgoT().TempDir()
			outside := filepath.Join(filepath.Dir(root), "escape")
			stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir(), listedFiles: []string{malicious}}
			client := NewFileStagingClient(&exportDirectoryBackend{writeOutput: true}, stager, "worker-1")
			result, err := client.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: root})
			if err != nil {
				failQuantizationStaging("%v", err)
			}
			if result == nil || result.Success {
				failQuantizationStaging("accepted remote entry %q: %#v", malicious, result)
			}
			if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
				failQuantizationStaging("entry %q escaped: %v", malicious, err)
			}
		}
	})

	It("ExportReportsNestedDestinationCreationFailure", func() {
		output := GinkgoT().TempDir()
		if err := os.WriteFile(filepath.Join(output, "nested"), []byte("not a directory"), 0o600); err != nil {
			failQuantizationStaging("%v", err)
		}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir(), listedFiles: []string{"nested/file.bin"}}
		client := NewFileStagingClient(&successfulExportBackend{}, stager, "worker-1")
		result, err := client.ExportModel(context.Background(), &pb.ExportModelRequest{OutputPath: output})
		if err != nil {
			failQuantizationStaging("%v", err)
		}
		if result == nil || result.Success || !strings.Contains(result.Message, "creating export destination") {
			failQuantizationStaging("directory creation failure was not reported: %#v", result)
		}
	})

	It("QuantizationWithoutOutputRetainsInputUntilTerminalProgress", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		input := filepath.Join(root, "model.gguf")
		if err := osWriteFile(input, []byte("model")); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend := &quantizationTestBackend{
			readModelOnProgress: true,
			progress:            &pb.QuantizationProgressUpdate{JobId: "input-only", Status: "failed"},
		}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir()}
		client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "input-only", Model: input}); err != nil {
			failQuantizationStaging("%v", err)
		}
		if len(stager.releasedKeys) != 0 {
			failQuantizationStaging("async input released at start: %v", stager.releasedKeys)
		}
		if err := client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "input-only"}, func(*pb.QuantizationProgressUpdate) {}); err != nil {
			failQuantizationStaging("%v", err)
		}
		if len(stager.releasedKeys) == 0 {
			failQuantizationStaging("terminal progress did not release staged input")
		}
		if _, ok, err := client.lookupQuantization(context.Background(), "input-only"); err != nil || ok {
			failQuantizationStaging("terminal progress retained staging record: ok=%v err=%v", ok, err)
		}
	})

	It("QuantizationCleanupFailureRetainsDurableStateForRetry", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		input := filepath.Join(root, "model.gguf")
		if err := osWriteFile(input, []byte("model")); err != nil {
			failQuantizationStaging("%v", err)
		}
		backend := &quantizationTestBackend{progress: &pb.QuantizationProgressUpdate{JobId: "cleanup-retry", Status: "failed"}}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir()}
		stager.releaseErr = errors.New("temporary cleanup failure")
		client := NewFileStagingClientWithOptions(backend, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "cleanup-retry", Model: input}); err != nil {
			failQuantizationStaging("%v", err)
		}
		progress := func() error {
			return client.QuantizationProgress(context.Background(), &pb.QuantizationProgressRequest{JobId: "cleanup-retry"}, func(*pb.QuantizationProgressUpdate) {})
		}
		if err := progress(); err != nil {
			failQuantizationStaging("%v", err)
		}
		if _, ok, err := client.lookupQuantization(context.Background(), "cleanup-retry"); err != nil || !ok {
			failQuantizationStaging("cleanup failure discarded durable state: ok=%v err=%v", ok, err)
		}
		stager.releaseErr = nil
		if err := progress(); err != nil {
			failQuantizationStaging("%v", err)
		}
		if _, ok, err := client.lookupQuantization(context.Background(), "cleanup-retry"); err != nil || ok {
			failQuantizationStaging("cleanup retry retained durable state: ok=%v err=%v", ok, err)
		}
	})

	It("StopQuantizationCleansStagedInputAndDurableState", func() {
		db := openQuantizationStagingTestDB()
		root := GinkgoT().TempDir()
		input := filepath.Join(root, "model.gguf")
		if err := osWriteFile(input, []byte("model")); err != nil {
			failQuantizationStaging("%v", err)
		}
		stager := &quantizationTestStager{remoteDir: GinkgoT().TempDir()}
		client := NewFileStagingClientWithOptions(&quantizationTestBackend{}, stager, "worker-1", FileStagingClientOptions{DB: db, DataPath: root})
		if _, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "stop-cleanup", Model: input, OutputDir: filepath.Join(root, "quantization", "stop-cleanup")}); err != nil {
			failQuantizationStaging("%v", err)
		}
		result, err := client.StopQuantization(context.Background(), &pb.QuantizationStopRequest{JobId: "stop-cleanup"})
		if err != nil || result == nil || !result.Success {
			failQuantizationStaging("stop: result=%#v err=%v", result, err)
		}
		if len(stager.releasedKeys) == 0 || len(stager.releasedDirs) != 1 {
			failQuantizationStaging("stop cleanup missing: keys=%v dirs=%v", stager.releasedKeys, stager.releasedDirs)
		}
		if _, ok, err := client.lookupQuantization(context.Background(), "stop-cleanup"); err != nil || ok {
			failQuantizationStaging("stop retained durable staging state: ok=%v err=%v", ok, err)
		}
	})

	It("QuantizationRejectsOutputOutsideReplicaDataPath", func() {
		db := openQuantizationStagingTestDB()
		dataPath := GinkgoT().TempDir()
		outside := GinkgoT().TempDir()
		if err := os.Symlink(outside, filepath.Join(dataPath, "escape")); err != nil {
			failQuantizationStaging("%v", err)
		}
		client := NewFileStagingClientWithOptions(&quantizationTestBackend{}, &quantizationTestStager{remoteDir: GinkgoT().TempDir()}, "worker-1", FileStagingClientOptions{DB: db, DataPath: dataPath})
		for _, output := range []string{outside, filepath.Join(dataPath, "..", filepath.Base(outside)), filepath.Join(dataPath, "escape", "job")} {
			_, err := client.StartQuantization(context.Background(), &pb.QuantizationRequest{JobId: "unsafe-" + filepath.Base(output), OutputDir: output})
			if err == nil {
				failQuantizationStaging("accepted output outside data path: %q", output)
			}
		}
	})
})

// osWriteFile keeps setup errors explicit while using production-safe modes.
func osWriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
