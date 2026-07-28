package oci

import (
	"fmt"

	"github.com/mudler/LocalAI/internal"
)

// UserAgent returns the User-Agent string LocalAI sends on outbound registry
// requests (OCI registries and Ollama). It identifies the client as LocalAI
// and, when the binary was built with a version stamp, appends it so registries
// can attribute client-side usage to LocalAI rather than to the generic
// User-Agent of the underlying transport library.
func UserAgent() string {
	if internal.Version == "" {
		return "LocalAI"
	}
	return fmt.Sprintf("LocalAI/%s", internal.Version)
}
