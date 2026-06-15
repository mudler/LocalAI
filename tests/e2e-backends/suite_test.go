// Package e2ebackends exercises a built backend container image end-to-end over
// its gRPC surface.
//
// The suite is intentionally backend-agnostic: it extracts a Docker image,
// launches the bundled run.sh entrypoint, then drives a configurable set of
// gRPC calls against the result. Specs are gated by capability flags so that a
// non-LLM backend (e.g. image generation, TTS, embeddings-only) can opt in to
// only the RPCs it implements.
//
// Configuration is entirely through environment variables — see backend_test.go
// for the full list.
package e2ebackends_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBackendE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Backend gRPC End-to-End Suite")
}
