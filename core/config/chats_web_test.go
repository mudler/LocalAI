package config_test

import (
	"github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the chats' web tool settings", func() {
	It("reads a port list, with 80 and 443 for an empty one", func() {
		Expect(config.ParsePorts("")).To(Equal([]int{80, 443}))
		Expect(config.ParsePorts(" 443, 80 ,443,8080")).To(Equal([]int{443, 80, 8080}))
		for _, bad := range []string{"0", "65536", "80,", "http", "-1", "80;443"} {
			_, err := config.ParsePorts(bad)
			Expect(err).To(HaveOccurred(), bad)
		}
	})

	It("accepts only an http(s) SearXNG address without credentials", func() {
		Expect(config.ValidateSearXNGURL("")).To(Succeed())
		Expect(config.ValidateSearXNGURL("http://searx.lan:8888")).To(Succeed())
		for _, bad := range []string{"searx.example.org", "ftp://x/", "https://", "https://u:p@x/"} {
			Expect(config.ValidateSearXNGURL(bad)).NotTo(Succeed(), bad)
		}
	})

	It("keeps both tools on until an administrator switches one off", func() {
		o := &config.ApplicationConfig{}
		Expect(o.ToRuntimeSettings().ChatsWebSearch).To(HaveValue(BeTrue()))
		Expect(o.ToRuntimeSettings().ChatsWebFetch).To(HaveValue(BeTrue()))
		off := false
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsWebFetch: &off})
		Expect(o.DisableChatsWebFetch).To(BeTrue())
		Expect(o.DisableChatsWebSearch).To(BeFalse())
		bad := "x"
		o.ChatsWebPorts = "80"
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsWebPorts: &bad})
		Expect(o.ChatsWebPorts).To(Equal("80"), "a bad list in the file is not taken")
	})
})
