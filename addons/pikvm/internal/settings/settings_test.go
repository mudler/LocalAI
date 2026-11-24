// SPDX-License-Identifier: MIT
package settings_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/pikvm/internal/settings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the device settings", func() {
	It("are off and small until an administrator says otherwise", func() {
		d, err := settings.Open(GinkgoT().TempDir()).For("https://a.example")
		Expect(err).NotTo(HaveOccurred())
		Expect(d).To(Equal(settings.Device{MaxTypeChars: 200}))
	})

	It("are kept per device and survive a restart", func() {
		dir := GinkgoT().TempDir()
		s := settings.Open(dir)
		_, err := s.Set("https://a.example", settings.Device{AllowTyping: true, MaxTypeChars: 5000})
		Expect(err).NotTo(HaveOccurred())
		again := settings.Open(dir)
		a, _ := again.For("https://a.example")
		b, _ := again.For("https://b.example")
		Expect(a.AllowTyping).To(BeTrue())
		Expect(a.MaxTypeChars).To(Equal(1000), "the ceiling holds")
		Expect(b.AllowTyping).To(BeFalse())
		info, _ := os.Stat(filepath.Join(dir, "settings.json"))
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("give the defaults and an error when the file is damaged", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{nope"), 0o600)).To(Succeed())
		d, err := settings.Open(dir).For("https://a.example")
		Expect(err).To(HaveOccurred())
		Expect(d.AllowTyping).To(BeFalse())
		Expect(d.AllowHardPower).To(BeFalse())
	})
})
