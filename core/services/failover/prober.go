package failover

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// LoadFunc returns the backend for a local target, loading it if needed.
type LoadFunc func(ctx context.Context, cfg config.ModelConfig) (grpc.Backend, error)

// DefaultProber probes remote targets over the upstream's OpenAI-compatible
// API and local targets through their gRPC backend.
type DefaultProber struct {
	HTTP      *http.Client
	Load      LoadFunc
	ModelPath string
}

func NewProber(load LoadFunc, modelPath string) *DefaultProber {
	return &DefaultProber{HTTP: &http.Client{}, Load: load, ModelPath: modelPath}
}

func (p *DefaultProber) Liveness(ctx context.Context, cfg config.ModelConfig, kind Kind, warm bool) error {
	switch {
	case kind == KindRemote:
		return p.remoteLiveness(ctx, cfg)
	case warm:
		return p.localHealth(ctx, cfg)
	}
	return p.coldLiveness(cfg)
}

func (p *DefaultProber) Inference(ctx context.Context, cfg config.ModelConfig, kind Kind, warm bool) error {
	if kind == KindRemote {
		return p.remoteInference(ctx, cfg)
	}
	return p.localInference(ctx, cfg)
}

// UpstreamBase strips the endpoint path from a cloud-proxy upstream_url:
// everything from "/v1" on, so a path prefix before it survives.
func UpstreamBase(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid upstream_url %q", raw)
	}
	path := u.Path
	if i := strings.Index(path, "/v1"); i >= 0 {
		path = path[:i]
	}
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(path, "/"), nil
}

// UpstreamModel is the model name the upstream knows the target by.
func UpstreamModel(cfg config.ModelConfig) string {
	if cfg.Proxy.UpstreamModel != "" {
		return cfg.Proxy.UpstreamModel
	}
	return cfg.Name
}

func (p *DefaultProber) authorize(req *http.Request, cfg config.ModelConfig) error {
	key, err := cfg.Proxy.ResolveAPIKey()
	if err != nil || key == "" {
		return err
	}
	if cfg.Proxy.Provider == config.ProxyProviderAnthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+key)
	return nil
}

func (p *DefaultProber) do(req *http.Request, cfg config.ModelConfig) (*http.Response, error) {
	if err := p.authorize(req, cfg); err != nil {
		return nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %s: HTTP %d", req.URL.Path, resp.StatusCode)
	}
	return resp, nil
}

func (p *DefaultProber) remoteLiveness(ctx context.Context, cfg config.ModelConfig) error {
	base, err := UpstreamBase(cfg.Proxy.UpstreamURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return err
	}
	resp, err := p.do(req, cfg)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return fmt.Errorf("upstream /v1/models: %w", err)
	}
	want := UpstreamModel(cfg)
	for _, d := range list.Data {
		if d.ID == want {
			return nil
		}
	}
	return fmt.Errorf("upstream does not list model %q", want)
}

func (p *DefaultProber) remoteInference(ctx context.Context, cfg config.ModelConfig) error {
	base, err := UpstreamBase(cfg.Proxy.UpstreamURL)
	if err != nil {
		return err
	}
	model := UpstreamModel(cfg)
	ping := []map[string]string{{"role": "user", "content": "ping"}}
	switch {
	case cfg.HasUsecases(config.FLAG_CHAT) || cfg.HasUsecases(config.FLAG_COMPLETION):
		if cfg.Proxy.Provider == config.ProxyProviderAnthropic {
			return p.postJSON(ctx, cfg, base+"/v1/messages", map[string]any{"model": model, "max_tokens": 1, "messages": ping})
		}
		return p.postJSON(ctx, cfg, base+"/v1/chat/completions", map[string]any{"model": model, "max_tokens": 1, "messages": ping})
	case cfg.HasUsecases(config.FLAG_EMBEDDINGS):
		return p.postJSON(ctx, cfg, base+"/v1/embeddings", map[string]any{"model": model, "input": "ping"})
	case cfg.HasUsecases(config.FLAG_TRANSCRIPT):
		return p.postTranscription(ctx, cfg, base, model)
	case cfg.HasUsecases(config.FLAG_TTS):
		return p.postJSON(ctx, cfg, base+"/v1/audio/speech", map[string]any{"model": model, "input": "ok"})
	}
	// Image, video and other costly usecases: liveness is the confirmation.
	return p.remoteLiveness(ctx, cfg)
}

func (p *DefaultProber) postJSON(ctx context.Context, cfg config.ModelConfig, endpoint string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.do(req, cfg)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.Body.Close()
}

func (p *DefaultProber) postTranscription(ctx context.Context, cfg config.ModelConfig, base, model string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", model)
	fw, err := mw.CreateFormFile("file", "probe.wav")
	if err != nil {
		return err
	}
	_, _ = fw.Write(silenceWAV())
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := p.do(req, cfg)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.Body.Close()
}

// silenceWAV is 200 ms of 16 kHz mono 16-bit silence.
func silenceWAV() []byte {
	const rate, samples = 16000, 3200
	data := samples * 2
	b := make([]byte, 44+data)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+data))
	copy(b[8:], "WAVE")
	copy(b[12:], "fmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(data))
	return b
}

func (p *DefaultProber) localHealth(ctx context.Context, cfg config.ModelConfig) error {
	if p.Load == nil {
		return errors.New("failover: no backend loader configured")
	}
	// Load returns the running backend, or starts it again after a crash.
	b, err := p.Load(ctx, cfg)
	if err != nil {
		return err
	}
	ok, err := b.HealthCheck(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("backend health check failed")
	}
	return nil
}

func (p *DefaultProber) localInference(ctx context.Context, cfg config.ModelConfig) error {
	if p.Load == nil {
		return errors.New("failover: no backend loader configured")
	}
	b, err := p.Load(ctx, cfg)
	if err != nil {
		return err
	}
	switch {
	case cfg.HasUsecases(config.FLAG_CHAT) || cfg.HasUsecases(config.FLAG_COMPLETION):
		_, err = b.Predict(ctx, &pb.PredictOptions{Prompt: "ping", Tokens: 1})
		return err
	case cfg.HasUsecases(config.FLAG_EMBEDDINGS):
		_, err = b.Embeddings(ctx, &pb.PredictOptions{Embeddings: "ping"})
		return err
	}
	// A backend process that answers HealthCheck rarely fails only for TTS or
	// transcription, so a real request adds little here.
	return p.localHealth(ctx, cfg)
}

// coldLiveness checks the model file without loading the model.
func (p *DefaultProber) coldLiveness(cfg config.ModelConfig) error {
	f := cfg.Model
	if f == "" || p.ModelPath == "" || strings.Contains(f, "://") {
		return nil
	}
	path := f
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.ModelPath, f)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) && filepath.Ext(f) == "" {
			return nil // a repository id, downloaded on demand
		}
		return fmt.Errorf("model file %s: %w", f, err)
	}
	return nil
}
