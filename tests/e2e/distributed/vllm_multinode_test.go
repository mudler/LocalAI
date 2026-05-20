package distributed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// vLLM data-parallel deployment config served by the head. KV cache is
// trimmed because the CPU smoke runs two engines on one box and the
// prebuilt wheel auto-sizes KV to fill RAM otherwise.
const qwenDPYAML = `name: qwen-dp
backend: vllm
parameters:
  model: Qwen/Qwen2.5-0.5B-Instruct
context_size: 512
trust_remote_code: true
template:
  use_tokenizer_template: true
engine_args:
  data_parallel_size: 2
  data_parallel_size_local: 1
  data_parallel_address: localai-head
  data_parallel_rpc_port: 32100
  enforce_eager: true
  max_model_len: 512
`

// End-to-end smoke for `local-ai p2p-worker vllm`. Two containers from
// the locally-built `local-ai:tests` image — head + headless follower
// — share a docker network and a backend bind-mount (so the cpu-vllm
// backend extracted by `make extract-backend-vllm` is seen as a system
// backend, no gallery fetch). DP=2 on a 0.5B model on CPU; the test
// asserts /readyz comes up across both ranks and a chat completion
// returns non-empty content.
//
// Required preconditions (the `test-e2e-vllm-multinode` Make target
// sets these up):
//   - `local-ai:tests` image built (docker-build-e2e)
//   - `local-backends/vllm/` populated (extract-backend-vllm)
//   - LOCALAI_VLLM_BACKEND_DIR env var pointing at the extracted dir
var _ = Describe("vLLM multi-node DP on CPU", Ordered, Label("Distributed", "VLLMMultinode"), func() {
	var baseURL string

	BeforeAll(func() {
		ctx := context.Background()

		image := vllmEnvOrDefault("LOCALAI_IMAGE", "local-ai")
		tag := vllmEnvOrDefault("LOCALAI_IMAGE_TAG", "tests")
		imageRef := fmt.Sprintf("%s:%s", image, tag)

		// LOCALAI_VLLM_BACKEND_DIR is set by the dedicated
		// `make test-e2e-vllm-multinode` target. The general
		// `make test-e2e` target picks this file up too via
		// `ginkgo -r ./tests/e2e`; in that context skip rather
		// than fail.
		backendDir := os.Getenv("LOCALAI_VLLM_BACKEND_DIR")
		if backendDir == "" {
			Skip("LOCALAI_VLLM_BACKEND_DIR not set — run `make test-e2e-vllm-multinode`")
		}
		Expect(filepath.Join(backendDir, "run.sh")).To(BeAnExistingFile(),
			"extracted backend missing run.sh — check the extract-backend-vllm output")

		// State dir for the head: holds qwen-dp.yaml and is also where
		// LocalAI redirects HF_HOME for backend subprocesses
		// (pkg/model/initializers.go:76), so Qwen weights accumulate
		// here. Stable gitignored path under local-backends/ so the
		// container's root-owned writes don't trip Ginkgo's TempDir
		// cleanup, and successive runs reuse the ~1 GB download.
		configDir := filepath.Join(thisFileDir(), "..", "..", "..", "local-backends", "vllm-multinode-state")
		Expect(os.MkdirAll(configDir, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(configDir, "qwen-dp.yaml"), []byte(qwenDPYAML), 0o644)).To(Succeed())

		net, err := network.New(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = net.Remove(context.Background())
		})

		commonMounts := testcontainers.ContainerMounts{
			{
				Source: testcontainers.DockerBindMountSource{HostPath: backendDir},
				Target: "/var/lib/local-ai/backends/vllm",
			},
		}

		// Head: rank 0, serves the OpenAI API. We wait briefly for the
		// HTTP port to bind (so MappedPort returns), then poll /readyz
		// with a long budget for the model load + DP handshake.
		head, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        imageRef,
				ExposedPorts: []string{"8080/tcp"},
				Cmd:          []string{"run", "/models/qwen-dp.yaml"},
				Env: map[string]string{
					"LOCALAI_ADDRESS": "0.0.0.0:8080",
					// Cap KV cache per rank so two CPU engines fit on
					// one host. The prebuilt wheel auto-sizes from
					// available RAM otherwise and OOM-kills with two
					// ranks sharing a 32 GB box.
					"VLLM_CPU_KVCACHE_SPACE": "1",
					// The backend dir is bind-mounted from the host;
					// without this, Python writes .pyc files into
					// __pycache__ as root and `rm -rf local-backends/`
					// fails on the next `make extract-backend-vllm`.
					"PYTHONDONTWRITEBYTECODE": "1",
				},
				Networks:       []string{net.Name},
				NetworkAliases: map[string][]string{net.Name: {"localai-head"}},
				Mounts: append(commonMounts,
					testcontainers.ContainerMount{
						// Not read-only: LocalAI writes back auto-
						// detected hooks (parser defaults, ...) into
						// the config and HF cache files into this
						// dir.
						Source: testcontainers.DockerBindMountSource{HostPath: configDir},
						Target: "/models",
					}),
				LogConsumerCfg: &testcontainers.LogConsumerConfig{
					Consumers: []testcontainers.LogConsumer{&vllmLogConsumer{prefix: "head"}},
				},
				WaitingFor: wait.ForListeningPort("8080/tcp").WithStartupTimeout(2 * time.Minute),
			},
			Started: true,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = head.Terminate(context.Background())
		})

		// Follower: rank 1, headless. Speaks ZMQ directly to the head
		// rank — no LocalAI gRPC; `p2p-worker vllm` exec's vllm serve.
		follower, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: imageRef,
				Cmd: []string{
					"p2p-worker", "vllm", "Qwen/Qwen2.5-0.5B-Instruct",
					"--data-parallel-size=2",
					"--data-parallel-size-local=1",
					"--start-rank=1",
					"--master-addr=localai-head",
					"--master-port=32100",
					// Mirror max_model_len from qwen-dp.yaml so both
					// ranks agree on the KV cache shape.
					"--vllm-arg=--max-model-len=512",
				},
				Env: map[string]string{
					"VLLM_CPU_KVCACHE_SPACE":  "1",
					"PYTHONDONTWRITEBYTECODE": "1",
				},
				Networks: []string{net.Name},
				Mounts:   commonMounts,
				LogConsumerCfg: &testcontainers.LogConsumerConfig{
					Consumers: []testcontainers.LogConsumer{&vllmLogConsumer{prefix: "follower"}},
				},
			},
			Started: true,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = follower.Terminate(context.Background())
		})

		port, err := head.MappedPort(ctx, "8080/tcp")
		Expect(err).ToNot(HaveOccurred())
		baseURL = fmt.Sprintf("http://localhost:%s", port.Port())

		Eventually(func() (int, error) {
			resp, err := http.Get(baseURL + "/readyz")
			if err != nil {
				return 0, err
			}
			defer func() { _ = resp.Body.Close() }()
			return resp.StatusCode, nil
		}, "20m", "10s").Should(Equal(http.StatusOK), "head /readyz never went green — both ranks need to load the model and complete the ZMQ handshake")
	})

	It("serves a chat completion across both ranks", func() {
		body, err := json.Marshal(map[string]any{
			"model": "qwen-dp",
			"messages": []map[string]string{
				{"role": "user", "content": "Reply with the single word: pong."},
			},
			"max_tokens":  16,
			"temperature": 0,
		})
		Expect(err).ToNot(HaveOccurred())

		resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()

		raw, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK), "non-200 from chat/completions: %s", string(raw))

		var parsed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
		Expect(parsed.Choices).ToNot(BeEmpty())
		Expect(parsed.Choices[0].Message.Content).ToNot(BeEmpty())
	})
})

type vllmLogConsumer struct {
	prefix string
}

func (l *vllmLogConsumer) Accept(log testcontainers.Log) {
	_, _ = GinkgoWriter.Write([]byte("[" + l.prefix + "] " + string(log.Content)))
}

func vllmEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// thisFileDir returns the directory of this test file so the test can
// be run from any working directory (`go test ./...` from the repo
// root is the common case).
func thisFileDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}
