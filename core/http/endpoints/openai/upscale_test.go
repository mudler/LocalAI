package openai

import (
	"context"
	"encoding/json"
	"errors"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	ggrpc "google.golang.org/grpc"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Image upscaling", func() {
	var (
		appConfig *config.ApplicationConfig
		tmpDir    string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "upscale")
		Expect(err).ToNot(HaveOccurred())
		appConfig = config.NewApplicationConfig(config.WithGeneratedContentDir(tmpDir), config.WithSystemState(&system.SystemState{}))
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("stores the result in the directory served by /generated-images", func() {
		original := backend.ImageUpscaleFunc
		backend.ImageUpscaleFunc = func(_ context.Context, _, dst string, scale int, _ *model.ModelLoader, _ config.ModelConfig, _ *config.ApplicationConfig) (func() error, error) {
			Expect(scale).To(Equal(3))
			return func() error {
				return os.WriteFile(dst, []byte("PNGDATA"), 0o644)
			}, nil
		}
		DeferCleanup(func() { backend.ImageUpscaleFunc = original })

		req, _ := makeMultipartRequest(
			map[string]string{"model": "stable-diffusion-x4-upscaler", "scale": "3"},
			map[string][]byte{"image": []byte("IMAGEDATA")},
		)
		rec := httptest.NewRecorder()
		ctx := echo.New().NewContext(req, rec)
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Backend: "diffusers"})

		Expect(UpscaleEndpoint(nil, nil, appConfig)(ctx)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))

		var response schema.OpenAIResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Data).To(HaveLen(1))
		Expect(response.Data[0].URL).To(ContainSubstring("/generated-images/upscale_"))

		filename := filepath.Base(response.Data[0].URL)
		contents, err := os.ReadFile(filepath.Join(tmpDir, "images", filename))
		Expect(err).ToNot(HaveOccurred())
		Expect(contents).To(Equal([]byte("PNGDATA")))
		entries, err := os.ReadDir(filepath.Join(tmpDir, "images"))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})

	DescribeTable("rejects missing or invalid scales", func(scale string) {
		fields := map[string]string{"model": "upscaler"}
		if scale != "missing" {
			fields["scale"] = scale
		}
		req, _ := makeMultipartRequest(fields, map[string][]byte{"image": []byte("IMAGE")})
		rec := httptest.NewRecorder()
		e := echo.New()
		ctx := e.NewContext(req, rec)
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{})
		err := UpscaleEndpoint(nil, nil, appConfig)(ctx)
		Expect(err).To(HaveOccurred())
		e.HTTPErrorHandler(err, ctx)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	},
		Entry("missing", "missing"), Entry("empty", ""), Entry("text", "abc"),
		Entry("fraction", "2.5"), Entry("zero", "0"), Entry("negative", "-1"),
		// 16 caps area amplification at 256x; int32 overflow must also fail.
		Entry("above resource bound", "17"), Entry("int32 overflow", "2147483648"),
		Entry("integer overflow", "999999999999999999999999"))

	DescribeTable("cleans only request files on failure", func(outcome string) {
		sentinel := filepath.Join(tmpDir, "unrelated.png")
		Expect(os.WriteFile(sentinel, []byte("keep"), 0600)).To(Succeed())
		var srcPath, dstPath string
		original := backend.ImageUpscaleFunc
		DeferCleanup(func() { backend.ImageUpscaleFunc = original })
		backend.ImageUpscaleFunc = func(ctx context.Context, src, dst string, scale int, _ *model.ModelLoader, cfg config.ModelConfig, app *config.ApplicationConfig) (func() error, error) {
			srcPath, dstPath = src, dst
			Expect(os.WriteFile(dst, []byte("partial"), 0600)).To(Succeed())
			if outcome == "load" {
				return nil, errors.New("load failed")
			}
			fake := &upscaleTestBackend{outcome: outcome}
			loader := model.NewModelLoader(&system.SystemState{})
			loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
				return model.NewModelWithClient(id, "test://upscale", fake), nil
			})
			return backend.ImageUpscale(ctx, src, dst, scale, loader, cfg, app)
		}
		req, _ := makeMultipartRequest(map[string]string{"model": "upscaler", "scale": strconv.Itoa(16)}, map[string][]byte{"image": []byte("IMAGE")})
		rec := httptest.NewRecorder()
		e := echo.New()
		ctx := e.NewContext(req, rec)
		cfg := &config.ModelConfig{Name: "upscaler", Backend: "stub"}
		cfg.SetDefaults()
		ctx.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
		err := UpscaleEndpoint(nil, nil, appConfig)(ctx)
		Expect(err).To(HaveOccurred())
		if outcome == "unsuccessful" {
			Expect(err.Error()).To(ContainSubstring("native scale mismatch"))
		}
		e.HTTPErrorHandler(err, ctx)
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(srcPath).NotTo(BeEmpty())
		Expect(dstPath).NotTo(BeEmpty())
		_, err = os.Stat(srcPath)
		Expect(os.IsNotExist(err)).To(BeTrue())
		_, err = os.Stat(dstPath)
		Expect(os.IsNotExist(err)).To(BeTrue())
		contents, err := os.ReadFile(sentinel)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(contents)).To(Equal("keep"))
	}, Entry("model loading", "load"), Entry("transport", "transport"), Entry("nil result", "nil"), Entry("unsuccessful result", "unsuccessful"))
})

type upscaleTestBackend struct {
	grpcPkg.Backend
	outcome string
}

func (*upscaleTestBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*upscaleTestBackend) IsBusy() bool                              { return false }
func (b *upscaleTestBackend) UpscaleImage(_ context.Context, in *pb.UpscaleImageRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	Expect(in.Scale).To(Equal(int32(16)))
	switch b.outcome {
	case "transport":
		return nil, errors.New("transport failed")
	case "nil":
		return nil, nil
	default:
		return &pb.Result{Success: false, Message: "native scale mismatch"}, nil
	}
}
