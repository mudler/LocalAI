package cli

import (
	"errors"
	"fmt"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chat command wiring", func() {
	Describe("chatAPIBaseURL", func() {
		It("adds /v1 to a root endpoint", func() {
			Expect(chatAPIBaseURL("http://127.0.0.1:8080")).To(Equal("http://127.0.0.1:8080/v1"))
		})

		It("keeps endpoints that already include /v1", func() {
			Expect(chatAPIBaseURL("http://127.0.0.1:8080/v1")).To(Equal("http://127.0.0.1:8080/v1"))
			Expect(chatAPIBaseURL("http://127.0.0.1:8080/v1/")).To(Equal("http://127.0.0.1:8080/v1"))
		})

		It("adds a default http scheme", func() {
			Expect(chatAPIBaseURL("127.0.0.1:8080")).To(Equal("http://127.0.0.1:8080/v1"))
		})

		It("preserves non-root paths before /v1", func() {
			Expect(chatAPIBaseURL("http://127.0.0.1:8080/localai")).To(Equal("http://127.0.0.1:8080/localai/v1"))
		})
	})

	Describe("argument parsing", func() {
		parse := func(args ...string) *ChatCMD {
			var cli struct {
				Chat ChatCMD `cmd:""`
			}
			parser, err := kong.New(&cli)
			Expect(err).ToNot(HaveOccurred())
			_, err = parser.Parse(append([]string{"chat"}, args...))
			Expect(err).ToNot(HaveOccurred())
			return &cli.Chat
		}

		It("leaves Args empty for a bare invocation", func() {
			Expect(parse().Args).To(BeEmpty())
		})

		It("binds flags that precede the forwarded arguments", func() {
			c := parse("--endpoint", "http://host:9090", "--model", "m", "plugin", "list")
			Expect(c.Endpoint).To(Equal("http://host:9090"))
			Expect(c.Model).To(Equal("m"))
			Expect(c.Args).To(Equal([]string{"plugin", "list"}))
		})

		It("forwards flags that follow the first positional to the agent", func() {
			c := parse("plugin", "install", "https://example.invalid/p", "--yes")
			Expect(c.Args).To(Equal([]string{"plugin", "install", "https://example.invalid/p", "--yes"}))
		})

		It("parses its own mode flags", func() {
			c := parse("--cli")
			Expect(c.CLI).To(BeTrue())
			Expect(c.Args).To(BeEmpty())
		})
	})

	// The agent prints its own diagnosis and hands back a status. main exits
	// with that status and prints nothing more, so the user reads one message
	// rather than an "exit status 1" stacked under it.
	Describe("ExitCodeError", func() {
		It("carries the status out", func() {
			Expect(ExitCodeError{Code: 2}.Code).To(Equal(2))
		})

		It("is recognisable after wrapping", func() {
			var got ExitCodeError
			Expect(errors.As(fmt.Errorf("chat: %w", ExitCodeError{Code: 2}), &got)).To(BeTrue())
			Expect(got.Code).To(Equal(2))
		})
	})

	Describe("agentArgs", func() {
		It("translates mode flags into the agent's own flags", func() {
			c := &ChatCMD{CLI: true}
			Expect(c.agentArgs()).To(Equal([]string{"--cli"}))
		})

		It("puts forwarded arguments after the translated flags", func() {
			c := &ChatCMD{Height: "40%", Args: []string{"plugin", "list"}}
			Expect(c.agentArgs()).To(Equal([]string{"--height", "40%", "plugin", "list"}))
		})

		It("returns nothing for a bare invocation", func() {
			Expect((&ChatCMD{}).agentArgs()).To(BeEmpty())
		})
	})
})
