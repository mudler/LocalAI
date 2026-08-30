package oci

import (
	"encoding/json"
	"fmt"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// CNCF ModelPack (https://github.com/modelpack/model-spec) discriminators. A
// manifest declaring either carries model files directly rather than a
// runnable container filesystem, so it cannot be extracted as an image tar.
const (
	ModelPackArtifactType    = "application/vnd.cncf.model.manifest.v1+json"
	ModelPackConfigMediaType = "application/vnd.cncf.model.config.v1+json"
)

// manifestEnvelope is the subset of an OCI manifest ModelPack detection needs.
// go-containerregistry's v1.Manifest has no ArtifactType field, so the raw
// manifest is parsed instead.
type manifestEnvelope struct {
	ArtifactType string `json:"artifactType"`
	Config       struct {
		MediaType string `json:"mediaType"`
	} `json:"config"`
}

// IsModelPackImage reports whether img is a CNCF ModelPack artifact.
//
// artifactType is the spec's discriminator. The config mediaType is checked as
// well, for registries and clients that predate artifactType or strip it.
func IsModelPackImage(img v1.Image) (bool, error) {
	raw, err := img.RawManifest()
	if err != nil {
		return false, fmt.Errorf("reading manifest: %w", err)
	}
	env := &manifestEnvelope{}
	if err := json.Unmarshal(raw, env); err != nil {
		return false, fmt.Errorf("parsing manifest: %w", err)
	}
	return env.ArtifactType == ModelPackArtifactType ||
		env.Config.MediaType == ModelPackConfigMediaType, nil
}
