// SPDX-License-Identifier: MIT
package cli

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Add-on carrier keys command", func() {
	parse := func(args ...string) (*AddonsCarrierKeysCMD, error) {
		command := struct {
			Keys AddonsCarrierKeysCMD `cmd:"" name:"addons-carrier-keys"`
		}{}
		p, err := kong.New(&command)
		if err != nil {
			return nil, err
		}
		_, err = p.Parse(append([]string{"addons-carrier-keys"}, args...))
		return &command.Keys, err
	}

	It("writes the carrier keys and says which files go where", func() {
		out := filepath.Join(GinkgoT().TempDir(), "keys")
		cmd, err := parse("--out", out, "--owner-host", "owner.example", "--owner-host", "192.0.2.5")
		Expect(err).NotTo(HaveOccurred())
		var buf bytes.Buffer
		cmd.output = &buf
		Expect(cmd.Run(nil)).To(Succeed())
		for _, name := range []string{"ca.pem", "owner.pem", "owner.key", "frontend.pem", "frontend.key", "frontend-sign.key", "frontend-sign.pub"} {
			Expect(filepath.Join(out, name)).To(BeARegularFile())
		}
		Expect(buf.String()).To(ContainSubstring("--addons-role=owner"))
		Expect(buf.String()).To(ContainSubstring("--addons-role=frontend"))
		Expect(buf.String()).NotTo(ContainSubstring("BEGIN"))
	})

	It("fails when the directory already holds a key set", func() {
		out := filepath.Join(GinkgoT().TempDir(), "keys")
		first, err := parse("--out", out, "--owner-host", "owner.example")
		Expect(err).NotTo(HaveOccurred())
		first.output = &bytes.Buffer{}
		Expect(first.Run(nil)).To(Succeed())
		before, err := os.ReadFile(filepath.Join(out, "ca.pem"))
		Expect(err).NotTo(HaveOccurred())
		second, err := parse("--out", out, "--owner-host", "owner.example")
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Run(nil)).To(MatchError(ContainSubstring("already exists")))
		after, err := os.ReadFile(filepath.Join(out, "ca.pem"))
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
	})

	It("needs --owner-host and --out", func() {
		_, err := parse("--out", filepath.Join(GinkgoT().TempDir(), "keys"))
		Expect(err).To(MatchError(ContainSubstring("owner-host")))
		_, err = parse("--owner-host", "owner.example")
		Expect(err).To(MatchError(ContainSubstring("out")))
	})
})
