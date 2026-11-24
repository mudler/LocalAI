//go:build !windows

package model

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestResolveLauncherOther(t *testing.T) {
	g := NewWithT(t)
	dir := t.TempDir()

	processName, args, err := resolveLauncher("grpc-server", dir, []string{"--addr", "localhost:50051"})
	g.Expect(err).NotTo(HaveOccurred())

	expectedName := filepath.Base("grpc-server")
	g.Expect(processName).To(Equal(expectedName))

	expectedArgs := []string{"--addr", "localhost:50051"}
	g.Expect(args).To(Equal(expectedArgs))
}
