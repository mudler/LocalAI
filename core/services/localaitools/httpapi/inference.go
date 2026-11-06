package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
)

// inference returns the shared client-tool code, wired to this remote server:
// requests go to BaseURL with the API key, and files are the Files store (a
// folder on this machine, and data URLs).
func (c *Client) inference() *localaitools.Inference {
	return &localaitools.Inference{
		Do:     c.doInference,
		Models: c,
		Files:  c.Files,
	}
}

// doInference sends a request built by the shared code to the remote server.
// The inference routes are slow (a video can take minutes), so it uses a
// client without the short timeout of the management calls.
func (c *Client) doInference(req *http.Request) (*http.Response, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	req.URL.Scheme, req.URL.Host = base.Scheme, base.Host
	req.URL.Path = strings.TrimRight(base.Path, "/") + req.URL.Path
	if req.URL.RawPath != "" {
		req.URL.RawPath = strings.TrimRight(base.EscapedPath(), "/") + req.URL.RawPath
	}
	req.Host = base.Host
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	cl := c.HTTPClient
	if c.InferenceHTTPClient != nil {
		cl = c.InferenceHTTPClient
	}
	return cl.Do(req)
}

// FirstModel implements localaitools.ModelChooser with the capability list of
// the remote server: the first model by name that is not disabled and has the
// use case.
func (c *Client) FirstModel(ctx context.Context, use config.ModelConfigUsecase) (string, error) {
	label := ""
	for name, flag := range config.GetAllModelConfigUsecases() {
		if flag == use {
			label = name
		}
	}
	if label == "" {
		return "", nil
	}
	var resp struct {
		Data []struct {
			ID           string   `json:"id"`
			Capabilities []string `json:"capabilities"`
			Disabled     bool     `json:"disabled"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, routeModelsCapabilities, nil, &resp); err != nil {
		return "", err
	}
	var names []string
	for _, m := range resp.Data {
		if m.Disabled {
			continue
		}
		for _, cap := range m.Capabilities {
			if cap == label {
				names = append(names, m.ID)
				break
			}
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return names[0], nil
}

// ---- Client (inference) ----

func (c *Client) GenerateImage(ctx context.Context, req localaitools.GenerateImageRequest) (*localaitools.ImageResult, error) {
	return c.inference().GenerateImage(ctx, req)
}

func (c *Client) EditImage(ctx context.Context, req localaitools.EditImageRequest) (*localaitools.ImageResult, error) {
	return c.inference().EditImage(ctx, req)
}

func (c *Client) Speak(ctx context.Context, req localaitools.SpeakRequest) (*localaitools.AudioResult, error) {
	return c.inference().Speak(ctx, req)
}

func (c *Client) TranscribeAudio(ctx context.Context, req localaitools.TranscribeAudioRequest) (*localaitools.TranscriptResult, error) {
	return c.inference().TranscribeAudio(ctx, req)
}

func (c *Client) DescribeImage(ctx context.Context, req localaitools.DescribeImageRequest) (*localaitools.DescribeImageResult, error) {
	return c.inference().DescribeImage(ctx, req)
}

func (c *Client) Embed(ctx context.Context, req localaitools.EmbedRequest) (*localaitools.EmbedResult, error) {
	return c.inference().Embed(ctx, req)
}

func (c *Client) Detect(ctx context.Context, req localaitools.DetectRequest) (*localaitools.DetectResult, error) {
	return c.inference().Detect(ctx, req)
}

func (c *Client) Rerank(ctx context.Context, req localaitools.RerankRequest) (*localaitools.RerankResult, error) {
	return c.inference().Rerank(ctx, req)
}

func (c *Client) GenerateVideo(ctx context.Context, req localaitools.GenerateVideoRequest) (*localaitools.VideoResult, error) {
	return c.inference().GenerateVideo(ctx, req)
}

func (c *Client) GenerateSound(ctx context.Context, req localaitools.GenerateSoundRequest) (*localaitools.AudioResult, error) {
	return c.inference().GenerateSound(ctx, req)
}

func (c *Client) VerifyVoice(ctx context.Context, req localaitools.VoiceVerifyRequest) (*localaitools.VerifyResult, error) {
	return c.inference().VerifyVoice(ctx, req)
}

func (c *Client) IdentifyVoice(ctx context.Context, req localaitools.IdentifyVoiceRequest) (*localaitools.IdentifyResult, error) {
	return c.inference().IdentifyVoice(ctx, req)
}

func (c *Client) VerifyFace(ctx context.Context, req localaitools.VerifyFaceRequest) (*localaitools.VerifyResult, error) {
	return c.inference().VerifyFace(ctx, req)
}

func (c *Client) IdentifyFace(ctx context.Context, req localaitools.IdentifyFaceRequest) (*localaitools.IdentifyResult, error) {
	return c.inference().IdentifyFace(ctx, req)
}

func (c *Client) ListVoiceProfiles(ctx context.Context) ([]localaitools.VoiceProfile, error) {
	return c.inference().ListVoiceProfiles(ctx)
}

func (c *Client) CreateVoiceProfile(ctx context.Context, req localaitools.CreateVoiceProfileRequest) (*localaitools.VoiceProfile, error) {
	return c.inference().CreateVoiceProfile(ctx, req)
}

func (c *Client) DeleteVoiceProfile(ctx context.Context, id string) error {
	return c.inference().DeleteVoiceProfile(ctx, id)
}
