package http_test

import (
    "bytes"
    "context"
    "encoding/json"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "time"

    "github.com/mudler/LocalAI/core/application"
    "github.com/mudler/LocalAI/core/config"
    "github.com/mudler/LocalAI/pkg/system"
    "github.com/mudler/LocalAI/pkg/grpc"
    pb "github.com/mudler/LocalAI/pkg/grpc/proto"
    "fmt"
    . "github.com/mudler/LocalAI/core/http"
    "github.com/labstack/echo/v4"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"
)

const testAPIKey = "joshua"

type fakeAI struct{}

func (f *fakeAI) Busy() bool                                { return false }
func (f *fakeAI) Lock()                                     {}
func (f *fakeAI) Unlock()                                   {}
func (f *fakeAI) Locking() bool                             { return false }
func (f *fakeAI) Predict(*pb.PredictOptions) (string, error) { return "", nil }
func (f *fakeAI) PredictStream(*pb.PredictOptions, chan string) error {
    return nil
}
func (f *fakeAI) Load(*pb.ModelOptions) error                        { return nil }
func (f *fakeAI) Embeddings(*pb.PredictOptions) ([]float32, error)    { return nil, nil }
func (f *fakeAI) GenerateImage(*pb.GenerateImageRequest) error       { return nil }
func (f *fakeAI) GenerateVideo(*pb.GenerateVideoRequest) error       { return nil }
func (f *fakeAI) Detect(*pb.DetectOptions) (pb.DetectResponse, error) { return pb.DetectResponse{}, nil }
func (f *fakeAI) AudioTranscription(*pb.TranscriptRequest) (pb.TranscriptResult, error) {
    return pb.TranscriptResult{}, nil
}
func (f *fakeAI) TTS(*pb.TTSRequest) error                          { return nil }
func (f *fakeAI) SoundGeneration(*pb.SoundGenerationRequest) error  { return nil }
func (f *fakeAI) TokenizeString(*pb.PredictOptions) (pb.TokenizationResponse, error) {
    return pb.TokenizationResponse{}, nil
}
func (f *fakeAI) Status() (pb.StatusResponse, error) { return pb.StatusResponse{}, nil }
func (f *fakeAI) StoresSet(*pb.StoresSetOptions) error        { return nil }
func (f *fakeAI) StoresDelete(*pb.StoresDeleteOptions) error  { return nil }
func (f *fakeAI) StoresGet(*pb.StoresGetOptions) (pb.StoresGetResult, error) {
    return pb.StoresGetResult{}, nil
}
func (f *fakeAI) StoresFind(*pb.StoresFindOptions) (pb.StoresFindResult, error) {
    return pb.StoresFindResult{}, nil
}
func (f *fakeAI) VAD(*pb.VADRequest) (pb.VADResponse, error) { return pb.VADResponse{}, nil }

var _ = Describe("OpenAI /v1/videos (embedded backend)", func() {
    var tmpdir string
    var appServer *application.Application
    var app *echo.Echo
    var ctx context.Context
    var cancel context.CancelFunc

    BeforeEach(func() {
        var err error
        tmpdir, err = os.MkdirTemp("", "")
        Expect(err).ToNot(HaveOccurred())

        modelDir := filepath.Join(tmpdir, "models")
        err = os.Mkdir(modelDir, 0750)
        Expect(err).ToNot(HaveOccurred())

        ctx, cancel = context.WithCancel(context.Background())

        systemState, err := system.GetSystemState(
            system.WithModelPath(modelDir),
        )
        Expect(err).ToNot(HaveOccurred())

   		grpc.Provide("embedded://fake", &fakeAI{})

        appServer, err = application.New(
            config.WithContext(ctx),
            config.WithSystemState(systemState),
            config.WithApiKeys([]string{testAPIKey}),
            config.WithGeneratedContentDir(tmpdir),
            config.WithExternalBackend("fake", "embedded://fake"),
        )
        Expect(err).ToNot(HaveOccurred())
    })

    AfterEach(func() {
        cancel()
        if app != nil {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            _ = app.Shutdown(ctx)
        }
        _ = os.RemoveAll(tmpdir)
    })

    It("accepts OpenAI-style video create and delegates to backend", func() {
		var err error
		app, err = API(appServer)
		Expect(err).ToNot(HaveOccurred())
		go func() {
			if err := app.Start("127.0.0.1:9091"); err != nil && err != http.ErrServerClosed {
				// Log error if needed
			}
		}()

        // wait for server
        client := &http.Client{Timeout: 5 * time.Second}
        Eventually(func() error {
            req, _ := http.NewRequest("GET", "http://127.0.0.1:9091/v1/models", nil)
            req.Header.Set("Authorization", "Bearer "+testAPIKey)
            resp, err := client.Do(req)
            if err != nil {
                return err
            }
            defer resp.Body.Close()
            if resp.StatusCode >= 400 {
                return fmt.Errorf("bad status: %d", resp.StatusCode)
            }
            return nil
        }, "30s", "500ms").Should(Succeed())

        body := map[string]interface{}{
            "model": "fake-model",
            "backend": "fake",
            "prompt": "a test video",
            "size": "256x256",
            "seconds": "1",
        }
        payload, err := json.Marshal(body)
        Expect(err).ToNot(HaveOccurred())

        req, err := http.NewRequest("POST", "http://127.0.0.1:9091/v1/videos", bytes.NewBuffer(payload))
        Expect(err).ToNot(HaveOccurred())
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", "Bearer "+testAPIKey)

        resp, err := client.Do(req)
        Expect(err).ToNot(HaveOccurred())
        defer resp.Body.Close()
        Expect(resp.StatusCode).To(Equal(200))

        dat, err := io.ReadAll(resp.Body)
        Expect(err).ToNot(HaveOccurred())

        var out map[string]interface{}
        err = json.Unmarshal(dat, &out)
        Expect(err).ToNot(HaveOccurred())
        data, ok := out["data"].([]interface{})
        Expect(ok).To(BeTrue())
        Expect(len(data)).To(BeNumerically(">", 0))
        first := data[0].(map[string]interface{})
        url, ok := first["url"].(string)
        Expect(ok).To(BeTrue())
        Expect(url).To(ContainSubstring("/generated-videos/"))
        Expect(url).To(ContainSubstring(".mp4"))
    })
})
