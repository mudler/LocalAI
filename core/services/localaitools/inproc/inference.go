package inproc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The client tools run inference through LocalAI's own API, in memory and as
// the acting user, with the transport the chats use. The route's feature
// gate, the model allow-list, the quota and the usage log all apply as for
// any request; nothing here calls a backend. The shared request code is
// localaitools.Inference.

// inference returns the Inference of one call: its sender acts as the user
// of ctx.
func (c *Client) inference(ctx context.Context) *localaitools.Inference {
	return &localaitools.Inference{
		Do:     c.doAsUser(ctx),
		Models: c,
		Vision: c.VisionModel,
		Files:  c.clientFiles(),
	}
}

// doAsUser returns the function that sends a request through the in-process
// transport as the acting user. A refusal that the transport reports as an
// error becomes the status it stands for, so the shared code maps it.
func (c *Client) doAsUser(ctx context.Context) func(*http.Request) (*http.Response, error) {
	return func(req *http.Request) (*http.Response, error) {
		var tr *provider.Transport
		if c.Transport != nil {
			tr = c.Transport()
		}
		if tr == nil {
			return nil, errors.New("inference is not available on this server")
		}
		user, err := c.requireActingUser(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := tr.Client(user).Do(req)
		if err != nil {
			switch msg := err.Error(); {
			case strings.Contains(msg, session.MarkNotAllowed):
				return statusResponse(req, http.StatusForbidden), nil
			case strings.Contains(msg, session.MarkQuota):
				return statusResponse(req, http.StatusTooManyRequests), nil
			}
			return nil, err
		}
		return resp, nil
	}
}

func statusResponse(req *http.Request, code int) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}
}

// FirstModel implements localaitools.ModelChooser: the first installed model
// by name that is not disabled and has the use case. Set Models to replace it.
func (c *Client) FirstModel(ctx context.Context, use config.ModelConfigUsecase) (string, error) {
	if c.Models != nil {
		return c.Models.FirstModel(ctx, use)
	}
	if c.ConfigLoader == nil {
		return "", nil
	}
	var names []string
	for _, m := range c.ConfigLoader.GetAllModelsConfigs() {
		if !m.IsDisabled() && m.HasUsecases(use) {
			names = append(names, m.Name)
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return names[0], nil
}

func (c *Client) clientFiles() localaitools.FileStore {
	if c.Files != nil {
		return c.Files
	}
	return workspaceFiles{lookup: c.userFiles}
}

// workspaceFiles is the FileStore of the in-process client: the home folder
// of the acting user in the chats' workspace. A reference is a path in that
// home, "/workspace/home/..." as the chats show it or relative to the home,
// or a data URL. Results go to the "generated" folder of the home.
type workspaceFiles struct {
	// lookup returns the files service and the principal of the acting user.
	lookup func(ctx context.Context) (*files.Service, string, error)
}

const generatedDir = "generated"

// userFiles finds the files service of the chats and the principal of the
// acting user: the user's ID, or the anonymous principal when auth is off.
func (c *Client) userFiles(ctx context.Context) (*files.Service, string, error) {
	if c.Chats == nil {
		return nil, "", errors.New("the user's files are not available on this server")
	}
	svc := c.Chats()
	if svc == nil || !svc.Files().Available() {
		return nil, "", errors.New("the user's files are not available on this server")
	}
	u, err := c.requireActingUser(ctx)
	if err != nil {
		return nil, "", err
	}
	id := ""
	if u != nil {
		id = u.ID
	}
	return svc.Files(), svc.Principal(id), nil
}

// homeRel turns a reference into a path inside the home.
func homeRel(ref string) string {
	guestHome := session.GuestRoot + "/" + session.GuestHome
	switch {
	case ref == guestHome:
		return "/"
	case strings.HasPrefix(ref, guestHome+"/"):
		return strings.TrimPrefix(ref, guestHome)
	}
	return ref
}

func (w workspaceFiles) Read(ctx context.Context, ref string, max int64) (localaitools.File, error) {
	if strings.HasPrefix(ref, "data:") {
		return localaitools.ReadDataURL(ref, max)
	}
	fs, principal, err := w.lookup(ctx)
	if err != nil {
		return localaitools.File{}, err
	}
	abs, err := fs.Abs(principal, homeRel(ref))
	if err != nil {
		return localaitools.File{}, localaitools.ErrFileNotFound
	}
	r, n, err := fs.Store().Open(ctx, fs.Actor(principal), abs)
	if err != nil {
		// Not found and not readable look the same: the tool must not tell
		// a user which paths exist.
		return localaitools.File{}, localaitools.ErrFileNotFound
	}
	defer func() { _ = r.Close() }()
	if n.Size > max {
		return localaitools.File{}, localaitools.ErrFileTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return localaitools.File{}, err
	}
	return localaitools.File{Name: n.Name, Data: data}, nil
}

func (w workspaceFiles) Write(ctx context.Context, name string, data []byte, _ string) (string, error) {
	fs, principal, err := w.lookup(ctx)
	if err != nil {
		return "", err
	}
	if err := fs.EnsureHome(ctx, principal); err != nil {
		return "", err
	}
	unique := localaitools.UniqueName(name)
	rel := "/" + path.Join(generatedDir, unique)
	abs, err := fs.Abs(principal, rel)
	if err != nil {
		return "", err
	}
	n, err := fs.Store().Put(ctx, fs.Actor(principal), abs, bytes.NewReader(data), workspace.PutOptions{Title: "Generated " + unique})
	if err != nil {
		return "", err
	}
	fs.Written(ctx, n, principal)
	return session.GuestRoot + "/" + session.GuestHome + rel, nil
}

// ---- Client (inference) ----

func (c *Client) GenerateImage(ctx context.Context, req localaitools.GenerateImageRequest) (*localaitools.ImageResult, error) {
	return c.inference(ctx).GenerateImage(ctx, req)
}

func (c *Client) EditImage(ctx context.Context, req localaitools.EditImageRequest) (*localaitools.ImageResult, error) {
	return c.inference(ctx).EditImage(ctx, req)
}

func (c *Client) Speak(ctx context.Context, req localaitools.SpeakRequest) (*localaitools.AudioResult, error) {
	return c.inference(ctx).Speak(ctx, req)
}

func (c *Client) TranscribeAudio(ctx context.Context, req localaitools.TranscribeAudioRequest) (*localaitools.TranscriptResult, error) {
	return c.inference(ctx).TranscribeAudio(ctx, req)
}

func (c *Client) DescribeImage(ctx context.Context, req localaitools.DescribeImageRequest) (*localaitools.DescribeImageResult, error) {
	return c.inference(ctx).DescribeImage(ctx, req)
}

func (c *Client) Embed(ctx context.Context, req localaitools.EmbedRequest) (*localaitools.EmbedResult, error) {
	return c.inference(ctx).Embed(ctx, req)
}

func (c *Client) Detect(ctx context.Context, req localaitools.DetectRequest) (*localaitools.DetectResult, error) {
	return c.inference(ctx).Detect(ctx, req)
}

func (c *Client) Rerank(ctx context.Context, req localaitools.RerankRequest) (*localaitools.RerankResult, error) {
	return c.inference(ctx).Rerank(ctx, req)
}

func (c *Client) GenerateVideo(ctx context.Context, req localaitools.GenerateVideoRequest) (*localaitools.VideoResult, error) {
	return c.inference(ctx).GenerateVideo(ctx, req)
}

func (c *Client) GenerateSound(ctx context.Context, req localaitools.GenerateSoundRequest) (*localaitools.AudioResult, error) {
	return c.inference(ctx).GenerateSound(ctx, req)
}

func (c *Client) VerifyVoice(ctx context.Context, req localaitools.VoiceVerifyRequest) (*localaitools.VerifyResult, error) {
	return c.inference(ctx).VerifyVoice(ctx, req)
}

func (c *Client) IdentifyVoice(ctx context.Context, req localaitools.IdentifyVoiceRequest) (*localaitools.IdentifyResult, error) {
	return c.inference(ctx).IdentifyVoice(ctx, req)
}

func (c *Client) VerifyFace(ctx context.Context, req localaitools.VerifyFaceRequest) (*localaitools.VerifyResult, error) {
	return c.inference(ctx).VerifyFace(ctx, req)
}

func (c *Client) IdentifyFace(ctx context.Context, req localaitools.IdentifyFaceRequest) (*localaitools.IdentifyResult, error) {
	return c.inference(ctx).IdentifyFace(ctx, req)
}

func (c *Client) ListVoiceProfiles(ctx context.Context) ([]localaitools.VoiceProfile, error) {
	return c.inference(ctx).ListVoiceProfiles(ctx)
}

func (c *Client) CreateVoiceProfile(ctx context.Context, req localaitools.CreateVoiceProfileRequest) (*localaitools.VoiceProfile, error) {
	return c.inference(ctx).CreateVoiceProfile(ctx, req)
}

func (c *Client) DeleteVoiceProfile(ctx context.Context, id string) error {
	return c.inference(ctx).DeleteVoiceProfile(ctx, id)
}
