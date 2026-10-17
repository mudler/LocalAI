package localai

import (
	"bytes"
	"context"
	"encoding/base64"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
)

type pixalEndpointBackend struct {
	grpcPkg.Backend
	seen *pb.Generate3DRequest
}

func (*pixalEndpointBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*pixalEndpointBackend) IsBusy() bool                              { return false }
func (b *pixalEndpointBackend) Generate3D(_ context.Context, r *pb.Generate3DRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.seen = r
	for _, path := range r.Images {
		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
	}
	return &pb.Result{Success: true}, os.WriteFile(r.Dst, []byte("glTF fixture"), 0600)
}

var _ = Describe("Pixal3D HTTP generation", func() {
	It("forwards four staged images and scale, ignores TRELLIS config defaults, and cleans inputs", func() {
		state := &system.SystemState{}
		loader := model.NewModelLoader(state)
		fixture := &pixalEndpointBackend{}
		loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://pixal", fixture), nil
		})
		cfg := &config.ModelConfig{Backend: "pixal3dcpp", Name: "pixal"}
		cfg.SetDefaults()
		cfg.Model = "pixal3d"
		cfg.Step = 12
		cfg.CFGScale = 7.5
		app := config.NewApplicationConfig(config.WithSystemState(state))
		app.GeneratedContentDir = GinkgoT().TempDir()
		var buf bytes.Buffer
		Expect(png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 2, 2)))).To(Succeed())
		data := base64.StdEncoding.EncodeToString(buf.Bytes())
		req := &schema.Model3DRequest{BasicModelRequest: schema.BasicModelRequest{Model: "pixal"}, Images: []string{data, data, data, data}, MeshScale: 0.8, ResponseFormat: "b64_json"}
		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/3d/generations", nil), rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, req)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
		Expect(Model3DEndpoint(nil, loader, app)(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(fixture.seen.Images).To(HaveLen(4))
		Expect(fixture.seen.Src).To(BeEmpty())
		Expect(fixture.seen.MeshScale).To(Equal(0.8))
		Expect(fixture.seen.Step).To(BeZero())
		Expect(fixture.seen.CfgScale).To(BeZero())
		for _, path := range fixture.seen.Images {
			_, err := os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())
		}
		entries, err := os.ReadDir(app.GeneratedContentDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
	It("rejects bad views and cleans partially staged input before loading", func() {
		var buf bytes.Buffer
		Expect(png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 2, 2)))).To(Succeed())
		data := base64.StdEncoding.EncodeToString(buf.Bytes())
		app := &config.ApplicationConfig{GeneratedContentDir: GinkgoT().TempDir()}
		req := &schema.Model3DRequest{BasicModelRequest: schema.BasicModelRequest{Model: "pixal"}, Images: []string{data, "bad%%%", data, data}, MeshScale: 1}
		e := echo.New()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/3d/generations", nil), httptest.NewRecorder())
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, req)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, &config.ModelConfig{Backend: "pixal3dcpp"})
		err := Model3DEndpoint(nil, nil, app)(c)
		Expect(err).To(BeAssignableToTypeOf(&echo.HTTPError{}))
		Expect(err.(*echo.HTTPError).Code).To(Equal(http.StatusBadRequest))
		entries, err := os.ReadDir(app.GeneratedContentDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})
})
