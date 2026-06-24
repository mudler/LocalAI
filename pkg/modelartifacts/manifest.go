package modelartifacts

import (
	"encoding/json"
	"fmt"
	"os"
)

const ManifestVersion = 1

type Manifest struct {
	Version  int            `json:"version"`
	Artifact Spec           `json:"artifact"`
	Files    []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	BlobOID string `json:"blob_oid,omitempty"`
	LFSOID  string `json:"lfs_oid,omitempty"`
	XetHash string `json:"xet_hash,omitempty"`
}

func ReadManifest(fileName string) (Manifest, error) {
	data, err := os.ReadFile(fileName)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Version != ManifestVersion || manifest.Artifact.Resolved == nil {
		return Manifest{}, fmt.Errorf("unsupported or incomplete artifact manifest")
	}
	for _, file := range manifest.Files {
		if err := ValidateRelativeHubPath(file.Path); err != nil {
			return Manifest{}, err
		}
		if file.Size < 0 || len(file.SHA256) != 64 {
			return Manifest{}, fmt.Errorf("invalid manifest entry %q", file.Path)
		}
	}
	return manifest, nil
}
