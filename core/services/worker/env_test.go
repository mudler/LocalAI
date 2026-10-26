// SPDX-License-Identifier: MIT
package worker

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

// The real worker starts this test executable as a tiny gRPC backend. Only the
// child's explicit environment enables helper mode; the test runner is untouched.
func init() {
	if os.Getenv("LOCALAI_ENV_TEST_CHILD") != "1" {
		return
	}
	var addr string
	for i, arg := range os.Args {
		if arg == "--addr" && i+1 < len(os.Args) {
			addr = os.Args[i+1]
		}
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		os.Exit(2)
	}
	value, present := os.LookupEnv("LOCALAI_ENV_TEST_VALUE")
	if err := os.WriteFile(os.Getenv("LOCALAI_ENV_TEST_OUTPUT"), []byte(fmt.Sprintf("%t:%s", present, value)), 0600); err != nil {
		os.Exit(3)
	}
	server := grpc.NewServer()
	pb.RegisterBackendServer(server, envTestBackend{})
	if server.Serve(listener) != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

type envTestBackend struct{ pb.UnimplementedBackendServer }

func (envTestBackend) Health(context.Context, *pb.HealthMessage) (*pb.Reply, error) {
	return &pb.Reply{Message: []byte("OK")}, nil
}
func (envTestBackend) Free(context.Context, *pb.HealthMessage) (*pb.Result, error) {
	return &pb.Result{Success: true}, nil
}

var _ = Describe("Worker model environment", func() {
	It("passes isolated snapshots to real children and restarts on replacement and removal", func() {
		root := GinkgoT().TempDir()
		exe, err := os.Executable()
		Expect(err).NotTo(HaveOccurred())
		backend := filepath.Join(root, "backend")
		Expect(os.WriteFile(backend, []byte("#!/bin/sh\nexec "+strconv.Quote(exe)+" \"$@\"\n"), 0700)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(root), system.WithBackendSystemPath(root), system.WithModelPath(root))
		Expect(err).NotTo(HaveOccurred())
		sup := &backendSupervisor{cfg: &Config{}, ml: model.NewModelLoader(state), processes: map[string]*backendProcess{}, minPort: 32000, maxPort: 42000, nextPort: 32000}
		DeferCleanup(func() {
			for key := range sup.processes {
				Expect(sup.stopBackendExact(key, true)).To(Succeed())
			}
		})
		snapshot := func(slot, value string) map[string]string {
			return map[string]string{"LOCALAI_ENV_TEST_CHILD": "1", "LOCALAI_ENV_TEST_OUTPUT": filepath.Join(root, slot), "LOCALAI_ENV_TEST_VALUE": value}
		}
		first := snapshot("a", "first")
		_, err = sup.startBackend("a#0", "test", backend, first)
		Expect(err).NotTo(HaveOccurred())
		original := sup.processes["a#0"]
		first["LOCALAI_ENV_TEST_VALUE"] = "caller mutation"
		Expect(original.envVars["LOCALAI_ENV_TEST_VALUE"]).To(Equal("first"))
		_, err = sup.startBackend("a#0", "test", backend, snapshot("a", "first"))
		Expect(err).NotTo(HaveOccurred())
		Expect(sup.processes["a#0"]).To(BeIdenticalTo(original))
		_, err = sup.startBackend("b#0", "test", backend, snapshot("b", "second"))
		Expect(err).NotTo(HaveOccurred())
		body, err := os.ReadFile(filepath.Join(root, "b"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("true:second"))
		_, err = sup.startBackend("a#0", "test", backend, snapshot("a", "changed"))
		Expect(err).NotTo(HaveOccurred())
		Expect(sup.processes["a#0"]).NotTo(BeIdenticalTo(original))
		removed := snapshot("a", "")
		delete(removed, "LOCALAI_ENV_TEST_VALUE")
		_, err = sup.startBackend("a#0", "test", backend, removed)
		Expect(err).NotTo(HaveOccurred())
		body, err = os.ReadFile(filepath.Join(root, "a"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("false:"))
		body, err = os.ReadFile(filepath.Join(root, "b"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("true:second"))
		Expect(strings.Join(os.Environ(), "\n")).NotTo(ContainSubstring("LOCALAI_ENV_TEST_CHILD=1"))
		Expect(sup.processMatchesEnv("b#0", map[string]string{})).To(BeFalse())
		Expect(sup.processMatchesEnv("b#0", nil)).To(BeTrue())
	})
})
