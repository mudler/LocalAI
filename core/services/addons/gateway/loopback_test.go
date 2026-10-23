// SPDX-License-Identifier: MIT
package gateway

import (
	"net"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the loopback carrier config", func() {
	It("keeps its address stable across starts", func() {
		root := filepath.Join(GinkgoT().TempDir(), "addons")
		_, first, err := NewLoopbackCarrierConfig("local", root)
		Expect(err).ToNot(HaveOccurred())
		cfg, second, err := NewLoopbackCarrierConfig("local", root)
		Expect(err).ToNot(HaveOccurred())
		Expect(second).To(Equal(first), "address changed between starts")
		Expect(cfg.OwnerAddress).To(Equal("https://"+second), "owner address does not match listen address")
		info, err := os.Stat(filepath.Join(root, loopbackPortFile))
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)), "port file mode")
	})

	It("does not share state between different roots", func() {
		_, a, err := NewLoopbackCarrierConfig("local", filepath.Join(GinkgoT().TempDir(), "addons"))
		Expect(err).ToNot(HaveOccurred())
		_, _, err = NewLoopbackCarrierConfig("local", filepath.Join(GinkgoT().TempDir(), "addons"))
		Expect(err).ToNot(HaveOccurred())
		Expect(a).ToNot(BeEmpty(), "empty address")
	})

	It("fails clearly when the port is in use", func() {
		root := filepath.Join(GinkgoT().TempDir(), "addons")
		_, address, err := NewLoopbackCarrierConfig("local", root)
		Expect(err).ToNot(HaveOccurred())
		held, err := net.Listen("tcp", address)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = held.Close() }()
		_, _, err = NewLoopbackCarrierConfig("local", root)
		Expect(err).To(HaveOccurred(), "expected an error while the port is in use")
		_, port, _ := net.SplitHostPort(address)
		Expect(err.Error()).To(ContainSubstring(port), "error does not name the port")
		Expect(err.Error()).To(ContainSubstring("--addons-enabled=false"), "error does not name the option")
	})

	It("rejects an invalid port file", func() {
		root := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(root, loopbackPortFile), []byte("nope"), 0o600)).To(Succeed())
		_, _, err := NewLoopbackCarrierConfig("local", root)
		Expect(err).To(HaveOccurred(), "expected an error for an invalid port file")
	})

	It("rejects a port file with a loose mode", func() {
		root := GinkgoT().TempDir()
		path := filepath.Join(root, loopbackPortFile)
		Expect(os.WriteFile(path, []byte("40123\n"), 0o600)).To(Succeed())
		Expect(os.Chmod(path, 0o644)).To(Succeed())
		_, _, err := NewLoopbackCarrierConfig("local", root)
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "0600")).To(BeTrue(), "expected a mode error, got %v", err)
	})

	It("rejects a port file that links outside the root", func() {
		root := GinkgoT().TempDir()
		outside := filepath.Join(GinkgoT().TempDir(), "port")
		Expect(os.WriteFile(outside, []byte("40123\n"), 0o600)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(root, loopbackPortFile))).To(Succeed())
		_, _, err := NewLoopbackCarrierConfig("local", root)
		Expect(err).To(HaveOccurred(), "expected an error for a port file that links outside the root")
	})
})
