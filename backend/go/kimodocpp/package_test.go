// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("packaged backend", func() {
	It("starts with bundled libraries and answers health without weights", func() {
		directory := os.Getenv("KIMODO_TEST_PACKAGE")
		if directory == "" {
			Skip("set KIMODO_TEST_PACKAGE to test the built package")
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		address := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())
		command := exec.Command("bash", filepath.Join(directory, "run.sh"), "--addr", address)
		command.Stdout, command.Stderr = GinkgoWriter, GinkgoWriter
		Expect(command.Start()).To(Succeed())
		DeferCleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
		client := grpc.NewClient(address, false, nil, false)
		Eventually(func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			healthy, err := client.HealthCheck(ctx)
			return err == nil && healthy
		}, 20*time.Second, 100*time.Millisecond).Should(BeTrue())
	})
})
