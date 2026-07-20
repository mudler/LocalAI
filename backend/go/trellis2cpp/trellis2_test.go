package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestTrellis2Cpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "trellis2cpp backend suite")
}

// touch creates empty files — resolveModels only checks existence, so the
// tests never need real GGUF weights.
func touch(dir string, names ...string) {
	for _, name := range names {
		Expect(os.WriteFile(filepath.Join(dir, name), nil, 0o600)).To(Succeed())
	}
}

var requiredFiles = []string{"dino_f16.gguf", "ss_flow_f16.gguf", "ss_dec_f16.gguf"}

var fullSet = append(append([]string{}, requiredFiles...),
	"slat_flow_f16.gguf", "slat_flow_1024_f16.gguf", "shape_dec_f16.gguf",
	"shape_enc_f16.gguf", "tex_dec_f16.gguf",
	"tex_slat_flow_512_f16.gguf", "tex_slat_flow_1024_f16.gguf")

var _ = Describe("resolveModels", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	It("refuses a directory without the trellis2 component files", func() {
		touch(dir, "some-llm.gguf")

		_, err := resolveModels("some-llm.gguf", dir, nil)
		Expect(err).To(MatchError(ContainSubstring("not a trellis2 model set")))
	})

	It("resolves every role from the full default-named set", func() {
		touch(dir, fullSet...)

		set, err := resolveModels("ss_flow_f16.gguf", dir, nil)
		Expect(err).NotTo(HaveOccurred())
		for name, path := range map[string]string{
			"dino":               set.dino,
			"ss_flow":            set.ssFlow,
			"ss_dec":             set.ssDec,
			"slat_flow":          set.slatFlow,
			"slat_flow_1024":     set.slatFlow1024,
			"shape_dec":          set.shapeDec,
			"shape_enc":          set.shapeEnc,
			"tex_dec":            set.texDec,
			"tex_slat_flow_512":  set.texSlatFlow512,
			"tex_slat_flow_1024": set.texSlatFlow1024,
		} {
			Expect(path).NotTo(BeEmpty(), "role %s", name)
		}
	})

	It("degrades to coarse-only without the 512 pair, even when texture files exist", func() {
		touch(dir, requiredFiles...)
		touch(dir, "shape_enc_f16.gguf", "tex_dec_f16.gguf", "tex_slat_flow_512_f16.gguf", "slat_flow_1024_f16.gguf")

		set, err := resolveModels("ss_flow_f16.gguf", dir, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(set.slatFlow).To(BeEmpty())
		Expect(set.shapeDec).To(BeEmpty())
		Expect(set.slatFlow1024).To(BeEmpty())
		Expect(set.shapeEnc).To(BeEmpty())
		Expect(set.texDec).To(BeEmpty())
		Expect(set.texSlatFlow512).To(BeEmpty())
		Expect(set.texSlatFlow1024).To(BeEmpty())
	})

	It("disables texturing but keeps fine geometry when the texture set is incomplete", func() {
		touch(dir, requiredFiles...)
		touch(dir, "slat_flow_f16.gguf", "slat_flow_1024_f16.gguf", "shape_dec_f16.gguf", "tex_dec_f16.gguf")

		set, err := resolveModels("ss_flow_f16.gguf", dir, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(set.slatFlow).NotTo(BeEmpty())
		Expect(set.shapeDec).NotTo(BeEmpty())
		Expect(set.slatFlow1024).NotTo(BeEmpty())
		Expect(set.shapeEnc).To(BeEmpty())
		Expect(set.texDec).To(BeEmpty())
		Expect(set.texSlatFlow512).To(BeEmpty())
		Expect(set.texSlatFlow1024).To(BeEmpty())
	})

	It("drops the 1024 cascade when texturing lacks the HR texture flow", func() {
		touch(dir, fullSet...)
		Expect(os.Remove(filepath.Join(dir, "tex_slat_flow_1024_f16.gguf"))).To(Succeed())

		set, err := resolveModels("ss_flow_f16.gguf", dir, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(set.slatFlow1024).To(BeEmpty())
		Expect(set.shapeEnc).NotTo(BeEmpty())
		Expect(set.texDec).NotTo(BeEmpty())
		Expect(set.texSlatFlow512).NotTo(BeEmpty())
	})

	It("honors explicit *_path option overrides", func() {
		touch(dir, fullSet...)
		custom := filepath.Join(dir, "custom")
		Expect(os.Mkdir(custom, 0o750)).To(Succeed())
		touch(custom, "my-dino.gguf")

		set, err := resolveModels("ss_flow_f16.gguf", dir, []string{"dino_path:custom/my-dino.gguf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(set.dino).To(Equal(filepath.Join(custom, "my-dino.gguf")))
	})

	It("fails when an explicitly configured file is missing", func() {
		touch(dir, fullSet...)

		_, err := resolveModels("ss_flow_f16.gguf", dir, []string{"tex_dec_path:nope.gguf"})
		Expect(err).To(MatchError(ContainSubstring("missing file")))
	})

	It("rejects option paths escaping the model directory", func() {
		touch(dir, fullSet...)

		_, err := resolveModels("ss_flow_f16.gguf", dir, []string{"dino_path:../outside.gguf"})
		Expect(err).To(HaveOccurred())
	})
})

var _ = DescribeTable("request parameter mapping",
	func(got, want int32) {
		Expect(got).To(Equal(want))
	},
	Entry("quality empty", pipelineForQuality(""), int32(pipeAuto)),
	Entry("quality auto", pipelineForQuality("auto"), int32(pipeAuto)),
	Entry("quality coarse", pipelineForQuality("coarse"), int32(pipeCoarse)),
	Entry("quality 512", pipelineForQuality("512"), int32(pipe512)),
	Entry("quality 1024", pipelineForQuality("1024"), int32(pipe1024)),
	Entry("background empty", backgroundForMode(""), int32(backgroundAuto)),
	Entry("background auto", backgroundForMode("auto"), int32(backgroundAuto)),
	Entry("background keep", backgroundForMode("keep"), int32(backgroundKeep)),
	Entry("background black", backgroundForMode("black"), int32(backgroundBlack)),
	Entry("background white", backgroundForMode("white"), int32(backgroundWhite)),
	Entry("components default", componentFilterFor(""), int32(2)),
	Entry("components all", componentFilterFor("all"), int32(2)),
	Entry("components largest", componentFilterFor("largest"), int32(1)),
	Entry("components tiny", componentFilterFor("tiny"), int32(0)),
	Entry("atoi empty", atoiOr("", 0), int32(0)),
	Entry("atoi value", atoiOr("2048", 0), int32(2048)),
	Entry("atoi junk", atoiOr("junk", 7), int32(7)),
)

var _ = Describe("print remesh parameters", func() {
	It("parses the print_remesh toggle", func() {
		Expect(boolParam("1")).To(BeTrue())
		Expect(boolParam("true")).To(BeTrue())
		Expect(boolParam("TRUE")).To(BeTrue())
		Expect(boolParam("")).To(BeFalse())
		Expect(boolParam("0")).To(BeFalse())
		Expect(boolParam("no")).To(BeFalse())
	})

	It("parses ratios and clamps junk to the fallback", func() {
		Expect(ratioOr("", 0.005)).To(BeNumerically("~", 0.005, 1e-6))
		Expect(ratioOr("0.01", 0.005)).To(BeNumerically("~", 0.01, 1e-6))
		Expect(ratioOr("junk", 0.005)).To(BeNumerically("~", 0.005, 1e-6))
		Expect(ratioOr("-1", 0.005)).To(BeNumerically("~", 0.005, 1e-6))
		Expect(ratioOr("0.9", 0.005)).To(BeNumerically("~", 0.005, 1e-6), "above the demo's 50% cap")
	})
})

var _ = Describe("packaged backend", func() {
	It("starts and answers Health without loading model weights", func() {
		runScript := os.Getenv("TRELLIS2CPP_SMOKE_RUN")
		if runScript == "" {
			runScript = filepath.Join("package", "run.sh")
		}
		if _, err := os.Stat(runScript); os.IsNotExist(err) {
			Skip("packaged backend is not present; run make before the smoke test")
		}

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())

		cmd := exec.Command("bash", runScript, "--addr="+addr)
		cmd.Stdout = GinkgoWriter
		cmd.Stderr = GinkgoWriter
		Expect(cmd.Start()).To(Succeed())
		processDone := make(chan error, 1)
		go func() { processDone <- cmd.Wait() }()
		processExited := false
		DeferCleanup(func() {
			if cmd.Process != nil && !processExited {
				_ = cmd.Process.Kill()
				<-processDone
			}
		})

		Eventually(func() error {
			select {
			case err := <-processDone:
				processExited = true
				if err != nil {
					return StopTrying("backend exited before Health succeeded").Wrap(err)
				}
				return StopTrying("backend exited before Health succeeded")
			default:
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			conn, err := grpc.DialContext(ctx, addr,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()

			reply, err := pb.NewBackendClient(conn).Health(ctx, &pb.HealthMessage{})
			if err != nil {
				return err
			}
			if string(reply.GetMessage()) != "OK" {
				return fmt.Errorf("unexpected Health reply %q", reply.GetMessage())
			}
			return nil
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())
	})
})
