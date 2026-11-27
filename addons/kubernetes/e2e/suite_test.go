// SPDX-License-Identifier: MIT

// Package e2e runs the tools of the Kubernetes add-on against a real API
// server: etcd and kube-apiserver started by the suite as plain processes,
// with no kubelet, scheduler or controller manager. So logs, exec and top
// cannot run here; their tests use the fake cluster. Set KUBE_ASSETS to a
// folder with the etcd and kube-apiserver binaries, for example from
// setup-envtest:
//
//	go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.22 use 1.37.0 --bin-dir <dir> -p path
//
// and run with the build tag kubernetes_e2e. This file has no tag, so that a
// run without it, such as the recursive coverage run, finds a suite with no
// specs instead of a package whose files are all excluded.
package e2e_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubernetes add-on against a real API server")
}
