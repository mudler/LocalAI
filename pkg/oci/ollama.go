package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

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

func OllamaModelManifest(image string) (*Manifest, error) {
	return ollamaModelManifest("https", "registry.ollama.ai", image)
}

func ollamaModelManifest(scheme, registry, image string) (*Manifest, error) {
	// parse the repository and tag from `image`. `image` should be for e.g. gemma:2b, or foobar/gemma:2b

	// if there is a : in the image, then split it
	// if there is no : in the image, then assume it is the latest tag
	tag, repository, image := ParseImageParts(image)

	// get e.g. https://registry.ollama.ai/v2/library/llama3/manifests/latest
	req, err := http.NewRequest("GET", scheme+"://"+registry+"/v2/"+repository+"/"+image+"/manifests/"+tag, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	req.Header.Set("User-Agent", UserAgent())
	client := httpclient.New(httpclient.WithFollowRedirects())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// parse the JSON response
	var manifest Manifest
	err = json.NewDecoder(resp.Body).Decode(&manifest)
	if err != nil {
		return nil, err
	}

	return &manifest, nil
}

func OllamaModelBlob(image string) (string, error) {
	return ollamaModelBlob("https", "registry.ollama.ai", image)
}

func ollamaModelBlob(scheme, registry, image string) (string, error) {
	manifest, err := ollamaModelManifest(scheme, registry, image)
	if err != nil {
		return "", err
	}
	// find a application/vnd.ollama.image.model in the mediaType

	for _, layer := range manifest.Layers {
		if layer.MediaType == "application/vnd.ollama.image.model" {
			return layer.Digest, nil
		}
	}

	return "", nil
}

func OllamaFetchModel(ctx context.Context, image string, output string, statusWriter func(ocispec.Descriptor) io.Writer) error {
	return ollamaFetchModel(ctx, "https", "registry.ollama.ai", image, output, statusWriter)
}

func ollamaFetchModel(ctx context.Context, scheme, registry, image string, output string, statusWriter func(ocispec.Descriptor) io.Writer) error {
	_, repository, imageNoTag := ParseImageParts(image)

	blobID, err := ollamaModelBlob(scheme, registry, image)
	if err != nil {
		return err
	}

	return fetchImageBlob(ctx, fmt.Sprintf("%s/%s/%s", registry, repository, imageNoTag), blobID, output, statusWriter, scheme == "http")
}
