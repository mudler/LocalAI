// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("carrier key set", func() {
	var keyActor = contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
	allow := func(context.Context, contract.Actor) error { return nil }

	generate := func() string {
		dir := filepath.Join(GinkgoT().TempDir(), "keys")
		Expect(GenerateCarrierKeys(dir, []string{"127.0.0.1", "owner.example"})).To(Succeed())
		return dir
	}
	// startOwner serves an owner carrier loaded from dir on a loopback port and
	// returns its address.
	startOwner := func(dir string) string {
		server := httptest.NewUnstartedServer(nil)
		address := "https://" + server.Listener.Addr().String()
		cfg, e := LoadCarrierConfig(dir, "owner", address)
		Expect(e).NotTo(HaveOccurred())
		owner, e := NewCarrier(*cfg)
		Expect(e).NotTo(HaveOccurred())
		handler, e := owner.OwnerHandler(&Gateway{Manager: ownerManager(keyActor, true), Revalidate: allow})
		Expect(e).NotTo(HaveOccurred())
		server.Config.Handler = http.Handler(handler)
		server.TLS = owner.ServerTLS()
		server.StartTLS()
		DeferCleanup(server.Close)
		return address
	}
	frontend := func(dir, address string) *Gateway {
		cfg, e := LoadCarrierConfig(dir, "frontend", address)
		Expect(e).NotTo(HaveOccurred())
		carrier, e := NewCarrier(*cfg)
		Expect(e).NotTo(HaveOccurred())
		return &Gateway{Forward: carrier, Revalidate: allow}
	}

	It("writes the set with private modes and no CA key", func() {
		dir := generate()
		info, e := os.Stat(dir)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		entries, e := os.ReadDir(dir)
		Expect(e).NotTo(HaveOccurred())
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
			fi, e := entry.Info()
			Expect(e).NotTo(HaveOccurred())
			Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o600)), entry.Name())
		}
		Expect(names).To(ConsistOf("ca.pem", "owner.pem", "owner.key", "frontend.pem", "frontend.key", "frontend-sign.key", "frontend-sign.pub"))
	})

	It("loads both roles into carriers and serves a frontend call from the owner", func() {
		dir := generate()
		address := startOwner(dir)
		list, e := frontend(dir, address).Apps(context.Background(), keyActor)
		Expect(e).NotTo(HaveOccurred())
		Expect(list.Apps).NotTo(BeEmpty())
	})

	It("gives the owner no signing key and the frontend its own", func() {
		dir := generate()
		owner, e := LoadCarrierConfig(dir, "owner", "https://owner.example:8443")
		Expect(e).NotTo(HaveOccurred())
		Expect(owner.SigningKey).To(BeEmpty())
		Expect(owner.VerificationKeys).To(HaveKey(FrontendIdentity))
		front, e := LoadCarrierConfig(dir, "frontend", "https://owner.example:8443")
		Expect(e).NotTo(HaveOccurred())
		Expect(front.SigningKey).To(HaveLen(ed25519.PrivateKeySize))
	})

	It("accepts two frontends made from the same set", func() {
		dir := generate()
		address := startOwner(dir)
		for range 2 {
			_, e := frontend(dir, address).Apps(context.Background(), keyActor)
			Expect(e).NotTo(HaveOccurred())
		}
	})

	It("refuses a frontend from another generation", func() {
		address := startOwner(generate())
		_, e := frontend(generate(), address).Apps(context.Background(), keyActor)
		Expect(e).To(HaveOccurred())
		// The carrier reports a refused TLS handshake as an unavailable owner,
		// like every other untrusted peer (see "certificate pins").
		Expect(errors.Is(e, ErrUnavailable)).To(BeTrue(), "got %v", e)
	})

	It("reads only the files of the role", func() {
		dir := generate()
		Expect(os.Remove(filepath.Join(dir, "frontend-sign.key"))).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "frontend.key"))).To(Succeed())
		_, e := LoadCarrierConfig(dir, "owner", "https://owner.example:8443")
		Expect(e).NotTo(HaveOccurred())
		Expect(os.Remove(filepath.Join(dir, "owner.key"))).To(Succeed())
		Expect(os.Remove(filepath.Join(dir, "frontend-sign.pub"))).To(Succeed())
		_, e = LoadCarrierConfig(dir, "frontend", "https://owner.example:8443")
		Expect(e).To(HaveOccurred())
	})

	It("refuses a key file with group or other access", func() {
		dir := generate()
		Expect(os.Chmod(filepath.Join(dir, "owner.key"), 0o644)).To(Succeed())
		_, e := LoadCarrierConfig(dir, "owner", "https://owner.example:8443")
		Expect(e).To(MatchError(ContainSubstring("owner.key")))
	})

	It("refuses an unknown role", func() {
		_, e := LoadCarrierConfig(generate(), "worker", "https://owner.example:8443")
		Expect(e).To(MatchError(ContainSubstring("worker")))
	})

	It("refuses to overwrite a file and leaves it unchanged", func() {
		dir := generate()
		before, e := os.ReadFile(filepath.Join(dir, "ca.pem"))
		Expect(e).NotTo(HaveOccurred())
		Expect(GenerateCarrierKeys(dir, []string{"owner.example"})).To(MatchError(ContainSubstring("already exists")))
		after, e := os.ReadFile(filepath.Join(dir, "ca.pem"))
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))

		partial := filepath.Join(GinkgoT().TempDir(), "partial")
		Expect(os.Mkdir(partial, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(partial, "owner.key"), []byte("x"), 0o600)).To(Succeed())
		Expect(GenerateCarrierKeys(partial, []string{"owner.example"})).To(HaveOccurred())
		entries, e := os.ReadDir(partial)
		Expect(e).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})

	It("needs an owner host", func() {
		Expect(GenerateCarrierKeys(filepath.Join(GinkgoT().TempDir(), "keys"), nil)).To(HaveOccurred())
	})
})
