package e2ebackends_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/phayes/freeport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// ─── llama.cpp disk prompt cache (opt-in) ───────────────────────────────────
//
// The disk tier of the llama.cpp prompt cache keeps prompts across a model
// reload. This spec starts the backend several times on one cache directory
// (LOCALAI_PROMPT_CACHE_ROOT, as LocalAI sets it) and checks:
//
//   - load, two turns that share a long prefix, Free(), stop; a new process on
//     the same directory reads the prefix back: fewer prompt tokens processed
//     and a faster first token than the cold run;
//   - a request of another cache namespace (another user) gets no hit;
//   - zero-retention requests (NoPromptCache) write nothing.
//
// It runs only with BACKEND_TEST_PROMPT_CACHE_DISK=1, BACKEND_BINARY (a
// llama-cpp run.sh) and BACKEND_TEST_MODEL_FILE (a small GGUF). CPU is enough.
//
//	BACKEND_TEST_PROMPT_CACHE_DISK   "1" to run it.
//	BACKEND_TEST_PROMPT_CACHE_WORDS  Words of the shared prefix (default 1300, about as many tokens).
//	BACKEND_TEST_CTX_SIZE            Context size (default 4096).

// cacheSalt is the salt LocalAI sends for a cache namespace (core/promptcache).
func cacheSalt(namespace string) string {
	sum := sha256.Sum256([]byte("localai/prompt-cache/v1\x00" + namespace))
	return hex.EncodeToString(sum[:])
}

// privateNSID is the namespace part of the file name of a zero-retention
// request's salt: the salt with every byte XOR 0x5a (prompt_cache_policy.h).
func privateNSID(salt string) string {
	b, _ := hex.DecodeString(salt)
	for i := range b {
		b[i] ^= 0x5a
	}
	return hex.EncodeToString(b[:8])
}

type cacheBackend struct {
	cmd    *exec.Cmd
	conn   *grpc.ClientConn
	client pb.BackendClient
}

func startCacheBackend(binaryDir, root string) *cacheBackend {
	port, err := freeport.GetFreePort()
	Expect(err).NotTo(HaveOccurred())
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.Command(filepath.Join(binaryDir, "run.sh"), "--addr="+addr)
	cmd.Env = append(os.Environ(), "LOCALAI_PROMPT_CACHE_ROOT="+root)
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Start()).To(Succeed())

	Eventually(func() error {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err != nil {
			return err
		}
		return c.Close()
	}, 60*time.Second, 200*time.Millisecond).Should(Succeed(), "backend did not start")

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).NotTo(HaveOccurred())
	return &cacheBackend{cmd: cmd, conn: conn, client: pb.NewBackendClient(conn)}
}

func (b *cacheBackend) load(modelFile string, ctxSize int32, options []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := b.client.LoadModel(ctx, &pb.ModelOptions{
		Model:       filepath.Base(modelFile),
		ModelFile:   modelFile,
		ContextSize: ctxSize,
		Threads:     4,
		NBatch:      512,
		MMap:        true,
		Options:     options,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(res.GetSuccess()).To(BeTrue(), res.GetMessage())
}

func (b *cacheBackend) promptTokens() int32 {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	m, err := b.client.GetMetrics(ctx, &pb.MetricsRequest{})
	Expect(err).NotTo(HaveOccurred())
	return m.GetPromptTokensProcessed()
}

type cacheRun struct {
	processed int32         // prompt tokens the backend computed
	elapsed   time.Duration // time to the one generated token
}

// predict sends one greedy request for one token and reports how many prompt
// tokens were computed (from the backend's counter) and how long it took.
func (b *cacheBackend) predict(prompt, salt string, noPersist bool) cacheRun {
	before := b.promptTokens()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	start := time.Now()
	_, err := b.client.Predict(ctx, &pb.PredictOptions{
		Prompt: prompt, Tokens: 1, Temperature: 0, TopK: 1, Seed: 1,
		PromptCacheAll:  !noPersist, // as LocalAI: zero-retention requests do not reuse the cache
		NoPromptCache:   noPersist,
		PromptCacheSalt: salt,
	})
	elapsed := time.Since(start)
	Expect(err).NotTo(HaveOccurred())
	return cacheRun{processed: b.promptTokens() - before, elapsed: elapsed}
}

// stop does what LocalAI does on unload: Free() (the backend saves the cache
// and answers Unimplemented), then SIGTERM.
func (b *cacheBackend) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	start := time.Now()
	_, err := b.client.Free(ctx, &pb.HealthMessage{})
	cancel()
	GinkgoWriter.Printf("prompt cache disk: Free() took %s\n", time.Since(start))
	Expect(status.Code(err)).To(Equal(codes.Unimplemented), "Free() must tell LocalAI to stop the process")
	Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second), "Free() must return within LocalAI's timeout")
	_ = b.conn.Close()
	_ = b.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = b.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = b.cmd.Process.Kill()
		<-done
	}
}

// cacheFiles lists the cache files under root by namespace id.
func cacheFiles(root string) map[string]int {
	res := map[string]int{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".cache") {
			return nil
		}
		parts := strings.Split(filepath.Base(path), "-")
		if len(parts) == 3 {
			res[parts[1]]++
		}
		return nil
	})
	return res
}

var _ = Describe("llama.cpp disk prompt cache", Ordered, func() {
	var (
		binaryDir string
		modelFile string
		workDir   string
		ctxSize   int32
		words     int
		options   []string
	)

	BeforeAll(func() {
		if os.Getenv("BACKEND_TEST_PROMPT_CACHE_DISK") != "1" {
			Skip("disk prompt cache spec is opt-in; set BACKEND_TEST_PROMPT_CACHE_DISK=1, BACKEND_BINARY and BACKEND_TEST_MODEL_FILE")
		}
		binary := os.Getenv("BACKEND_BINARY")
		Expect(filepath.Base(binary)).To(Equal("run.sh"), "BACKEND_BINARY must point at the llama-cpp run.sh")
		binaryDir = filepath.Dir(binary)
		modelFile = os.Getenv("BACKEND_TEST_MODEL_FILE")
		Expect(modelFile).To(BeAnExistingFile())
		ctxSize = envInt32("BACKEND_TEST_CTX_SIZE", 4096)
		words = int(envInt32("BACKEND_TEST_PROMPT_CACHE_WORDS", 1300))
		// Test hosts are often short of disk: small free-space watermarks.
		options = []string{"prompt_cache_disk_low_free_mib:1024", "prompt_cache_disk_critical_free_mib:512"}
		var err error
		workDir, err = os.MkdirTemp(os.Getenv("TMPDIR"), "prompt-cache-disk-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if workDir != "" {
			_ = os.RemoveAll(workDir)
		}
	})

	It("keeps a shared prefix across a reload, per user", func() {
		root := filepath.Join(workDir, "reload")
		userA := cacheSalt("user:a")
		userB := cacheSalt("user:b")
		prefix := pagedPrompt(1, words)

		By("first process: two turns of user A share a long prefix")
		b := startCacheBackend(binaryDir, root)
		b.load(modelFile, ctxSize, options)
		cold := b.predict(prefix+" Turn one.", userA, false)
		warm := b.predict(prefix+" Turn two.", userA, false)
		GinkgoWriter.Printf("prompt cache disk: cold %d tokens in %s, second turn %d tokens in %s\n",
			cold.processed, cold.elapsed, warm.processed, warm.elapsed)
		Expect(cold.processed).To(BeNumerically(">=", int32(words)), "the first turn computes the whole prompt")
		b.stop()

		files := cacheFiles(root)
		GinkgoWriter.Printf("prompt cache disk: files by namespace after the first process: %v\n", files)
		Expect(files[userA[:16]]).To(BeNumerically(">=", 1), "the shared prefix of user A is on disk")
		Expect(files).To(HaveLen(1), "only user A's namespace has files")

		By("second process: user B gets no hit, user A does")
		b = startCacheBackend(binaryDir, root)
		b.load(modelFile, ctxSize, options)
		other := b.predict(prefix+" Turn three.", userB, false)
		hit := b.predict(prefix+" Turn three.", userA, false)
		GinkgoWriter.Printf("prompt cache disk: after reload, user B %d tokens in %s, user A %d tokens in %s\n",
			other.processed, other.elapsed, hit.processed, hit.elapsed)
		b.stop()

		Expect(other.processed).To(BeNumerically(">=", int32(words)), "another user must not reuse user A's prefix")
		Expect(hit.processed).To(BeNumerically("<=", cold.processed-1024), "user A's prefix is read from disk, not computed")
		Expect(hit.elapsed).To(BeNumerically("<", cold.elapsed*7/10), "the first token comes sooner than in the cold run")
	})

	It("writes a reused prefix in the background, before any unload", func() {
		root := filepath.Join(workDir, "write-behind")
		user := cacheSalt("user:w")
		prefix := pagedPrompt(3, words)

		b := startCacheBackend(binaryDir, root)
		b.load(modelFile, ctxSize, options)
		b.predict(prefix+" One.", user, false)
		b.predict(prefix+" Two.", user, false)
		// No Free(): the second turn makes the prefix worth keeping, and the
		// backend writes it once it is idle.
		Eventually(func() int { return cacheFiles(root)[user[:16]] }, 60*time.Second, 500*time.Millisecond).
			Should(BeNumerically(">=", 1), "the reused prefix reaches disk while the model stays loaded")
		b.stop()
	})

	It("writes nothing for zero-retention requests", func() {
		root := filepath.Join(workDir, "zero-retention")
		user := cacheSalt("user:z")
		prefix := pagedPrompt(2, words)

		b := startCacheBackend(binaryDir, root)
		b.load(modelFile, ctxSize, options)
		// Twice, so the prefix would qualify for a write if it could persist.
		b.predict(prefix+" One.", user, true)
		b.predict(prefix+" Two.", user, true)
		b.stop()

		files := cacheFiles(root)
		GinkgoWriter.Printf("prompt cache disk: files after zero-retention requests: %v\n", files)
		Expect(files).To(BeEmpty(), "a zero-retention request must never reach disk")
		Expect(files[privateNSID(user)]).To(BeZero())
	})
})
