package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Media types of the layers of an Ollama model manifest.
const (
	OllamaMediaTypeModel     = "application/vnd.ollama.image.model"
	OllamaMediaTypeProjector = "application/vnd.ollama.image.projector"
	OllamaMediaTypeParams    = "application/vnd.ollama.image.params"
	// OllamaMediaTypeTensor marks one tensor of a model stored as safetensors.
	OllamaMediaTypeTensor = "application/vnd.ollama.image.tensor"
)

// OllamaLayerProjector is the layer selector that makes OllamaFetchModel
// download the vision projector of a model instead of its weights. It is
// written after the image name: "gemma3:4b#projector".
const OllamaLayerProjector = "projector"

const (
	defaultOllamaRegistry = "registry.ollama.ai"

	// A manifest lists a handful of layers, so a megabyte is far above any real
	// one. The params layer is a few hundred bytes of JSON.
	maxOllamaManifestSize = 1 << 20
	maxOllamaParamsSize   = 64 << 10
)

var (
	ollamaMu       sync.RWMutex
	ollamaRegistry = defaultOllamaRegistry
)

// UseOllamaRegistry points Ollama pulls at another registry host, reached over
// plain HTTP when plainHTTP is set. It returns a function that restores the
// previous registry. It exists for tests and for local mirrors.
func UseOllamaRegistry(host string, plainHTTP bool) (restore func()) {
	ollamaMu.Lock()
	previous := ollamaRegistry
	ollamaRegistry = host
	ollamaMu.Unlock()

	plainHTTPMu.Lock()
	wasPlain := plainHTTPRegistries[host]
	plainHTTPRegistries[host] = plainHTTP
	plainHTTPMu.Unlock()

	return func() {
		ollamaMu.Lock()
		ollamaRegistry = previous
		ollamaMu.Unlock()
		plainHTTPMu.Lock()
		plainHTTPRegistries[host] = wasPlain
		plainHTTPMu.Unlock()
	}
}

func currentOllamaRegistry() string {
	ollamaMu.RLock()
	defer ollamaMu.RUnlock()
	return ollamaRegistry
}

func ollamaScheme(host string) string {
	if isPlainHTTPRegistry(host) {
		return "http"
	}
	return "https"
}

// Define the main struct for the JSON data
type Manifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	MediaType     string        `json:"mediaType"`
	Config        Config        `json:"config"`
	Layers        []LayerDetail `json:"layers"`
}

// Define the struct for the "config" section
type Config struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
}

// Define the struct for each item in the "layers" array
type LayerDetail struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Size      int    `json:"size"`
}

// splitOllamaLayer separates the optional layer selector ("#projector") from
// an Ollama image name.
func splitOllamaLayer(image string) (name, layer string) {
	name, layer, _ = strings.Cut(image, "#")
	return name, layer
}

// OllamaDisplayName is the model name as users write it: "name:tag", with a
// "user/" prefix for a namespaced model and none for the "library" namespace.
func OllamaDisplayName(image string) string {
	name, _ := splitOllamaLayer(image)
	tag, repository, img := ParseImageParts(name)
	if repository == "library" {
		return img + ":" + tag
	}
	return repository + "/" + img + ":" + tag
}

func OllamaModelManifest(image string) (*Manifest, error) {
	return ollamaModelManifest(context.Background(), image)
}

func ollamaModelManifest(ctx context.Context, image string) (*Manifest, error) {
	display := OllamaDisplayName(image)
	name, _ := splitOllamaLayer(image)
	// parse the repository and tag from `image`. `image` should be for e.g. gemma:2b, or foobar/gemma:2b

	// if there is a : in the image, then split it
	// if there is no : in the image, then assume it is the latest tag
	tag, repository, imageName := ParseImageParts(name)

	registry := currentOllamaRegistry()
	// get e.g. https://registry.ollama.ai/v2/library/llama3/manifests/latest
	url := ollamaScheme(registry) + "://" + registry + "/v2/" + repository + "/" + imageName + "/manifests/" + tag
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json")
	req.Header.Set("User-Agent", UserAgent())
	client := httpclient.New(httpclient.WithFollowRedirects())
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama model %q: could not reach the registry: %w", display, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("ollama model %q was not found in the registry (HTTP 404)", display)
		}
		return nil, fmt.Errorf("ollama model %q: the registry answered HTTP %d", display, resp.StatusCode)
	}

	// parse the JSON response
	var manifest Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOllamaManifestSize)).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("ollama model %q: the registry did not return a valid manifest: %w", display, err)
	}
	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("ollama model %q: the registry did not return a valid manifest (no layers)", display)
	}

	return &manifest, nil
}

// layerDigest returns the digest of the first layer with the media type, or "".
func (m *Manifest) layerDigest(mediaType string) string {
	for _, layer := range m.Layers {
		if layer.MediaType == mediaType {
			return layer.Digest
		}
	}
	return ""
}

func OllamaModelBlob(image string) (string, error) {
	manifest, err := OllamaModelManifest(image)
	if err != nil {
		return "", err
	}
	return ollamaLayerForSelector(manifest, image)
}

// ollamaLayerForSelector picks the layer an image name asks for: the model
// weights, or the projector with the "#projector" selector. A missing layer is
// an error, never an empty digest.
func ollamaLayerForSelector(manifest *Manifest, image string) (string, error) {
	_, layer := splitOllamaLayer(image)
	mediaType, what := OllamaMediaTypeModel, "model layer"
	switch layer {
	case "":
	case OllamaLayerProjector:
		mediaType, what = OllamaMediaTypeProjector, "projector layer"
	default:
		return "", fmt.Errorf("ollama model %q: unknown layer %q", OllamaDisplayName(image), layer)
	}
	digest := manifest.layerDigest(mediaType)
	if digest == "" && mediaType == OllamaMediaTypeModel && manifest.layerDigest(OllamaMediaTypeTensor) != "" {
		// MLX, nvfp4 and mxfp8 tags are stored as safetensors tensors, one
		// layer per tensor, with no single weights file.
		return "", fmt.Errorf("ollama model %q is stored as safetensors/MLX tensors, is not a GGUF file and cannot run with llama.cpp: choose a GGUF tag of the same model", OllamaDisplayName(image))
	}
	if digest == "" {
		return "", fmt.Errorf("ollama model %q has no %s", OllamaDisplayName(image), what)
	}
	return digest, nil
}

func OllamaFetchModel(ctx context.Context, image string, output string, statusWriter func(ocispec.Descriptor) io.Writer) error {
	name, _ := splitOllamaLayer(image)
	_, repository, imageNoTag := ParseImageParts(name)

	manifest, err := ollamaModelManifest(ctx, image)
	if err != nil {
		return err
	}
	blobID, err := ollamaLayerForSelector(manifest, image)
	if err != nil {
		return err
	}

	return FetchImageBlob(ctx, fmt.Sprintf("%s/%s/%s", currentOllamaRegistry(), repository, imageNoTag), blobID, output, statusWriter)
}

// OllamaModelInfo is what an Ollama manifest tells about a model before any
// weights are downloaded.
type OllamaModelInfo struct {
	// Params is the JSON object of the params layer (sampling defaults and
	// stop sequences). It is nil when the model has none.
	Params map[string]any
	// HasProjector is true when the model has a separate vision projector.
	HasProjector bool
}

// OllamaDescribeModel reads the manifest of an Ollama model and its params
// layer. It fails when the model is missing, the manifest is not valid, the
// model has no weights layer, or the weights are not a GGUF file. The params
// layer is read with a size cap; a params layer that is not a JSON object is
// ignored with the model still usable.
func OllamaDescribeModel(ctx context.Context, image string) (*OllamaModelInfo, error) {
	name, _ := splitOllamaLayer(image)
	_, repository, imageNoTag := ParseImageParts(name)
	repo := fmt.Sprintf("%s/%s/%s", currentOllamaRegistry(), repository, imageNoTag)

	manifest, err := ollamaModelManifest(ctx, name)
	if err != nil {
		return nil, err
	}
	modelDigest, err := ollamaLayerForSelector(manifest, name)
	if err != nil {
		return nil, err
	}

	// The weights are fetched later, so look at the first bytes now: a model
	// that is not a GGUF file fails the import here, before a large download.
	head, err := PeekImageBlob(ctx, repo, modelDigest, 4)
	if err != nil {
		return nil, fmt.Errorf("ollama model %q: could not read the model layer: %w", OllamaDisplayName(name), err)
	}
	if string(head) != "GGUF" {
		return nil, fmt.Errorf("ollama model %q: %w", OllamaDisplayName(name), ErrNotGGUF)
	}

	info := &OllamaModelInfo{HasProjector: manifest.layerDigest(OllamaMediaTypeProjector) != ""}
	if digest := manifest.layerDigest(OllamaMediaTypeParams); digest != "" {
		raw, err := FetchImageBlobBytes(ctx, repo, digest, maxOllamaParamsSize)
		if err != nil {
			return nil, fmt.Errorf("ollama model %q: could not read the params layer: %w", OllamaDisplayName(name), err)
		}
		var params map[string]any
		if json.Unmarshal(raw, &params) == nil {
			info.Params = params
		}
	}
	return info, nil
}
