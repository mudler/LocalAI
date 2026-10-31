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

	It("reads the order of the search providers, with a default that keeps SearXNG alone", func() {
		Expect(config.ParseSearchProviders("", false)).To(Equal([]string{"duckduckgo", "brave_html", "bing_html"}))
		Expect(config.ParseSearchProviders(" ", true)).To(Equal([]string{"searxng"}))
		Expect(config.ParseSearchProviders("Bing_HTML, searxng,", false)).To(Equal([]string{"bing_html", "searxng"}))
		for _, bad := range []string{"google", "duckduckgo,duckduckgo", ",", "brave"} {
			_, err := config.ParseSearchProviders(bad, false)
			Expect(err).To(HaveOccurred(), bad)
		}
		o := &config.ApplicationConfig{ChatsWebSearchProviders: "duckduckgo"}
		bad, good := "nope", " brave_html,duckduckgo "
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsWebSearchProviders: &bad})
		Expect(o.ChatsWebSearchProviders).To(Equal("duckduckgo"), "a bad list in the file is not taken")
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsWebSearchProviders: &good})
		Expect(o.ChatsWebSearchProviders).To(Equal("brave_html,duckduckgo"))
		Expect(o.ToRuntimeSettings().ChatsWebSearchProviders).To(HaveValue(Equal("brave_html,duckduckgo")))
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

	It("keeps the saved agents of the chats on until an administrator switches them off", func() {
		o := &config.ApplicationConfig{}
		Expect(o.ToRuntimeSettings().ChatsSavedAgents).To(HaveValue(BeTrue()))
		off := false
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsSavedAgents: &off})
		Expect(o.DisableChatsSavedAgents).To(BeTrue())
		Expect(o.ToRuntimeSettings().ChatsSavedAgents).To(HaveValue(BeFalse()))
	})
})
