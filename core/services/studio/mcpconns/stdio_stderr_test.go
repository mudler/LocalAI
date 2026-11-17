package mcpconns_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

const helperKey = "sealed-key-0123456789"

var _ = Describe("the output of a local command", func() {
	// localConnector saves an enabled stdio connector that runs the test
	// binary as the helper in the given mode, with a secret in its environment.
	localConnector := func(e *env, mode string) mcpconns.Connector {
		self, err := os.Executable()
		Expect(err).NotTo(HaveOccurred())
		e.admin.Store(true)
		env := map[string]string{"MCPCONNS_HELPER": mode, "HELPER_KEY": helperKey}
		c, err := e.svc.Create(specCtx(), "u1", mcpconns.Input{Name: "Local", Transport: mcpconns.TransportStdio, Command: self, Env: &env, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		return c
	}

	It("tells a connection test why the command failed, apart from the one-line error and without secrets", func() {
		e := newEnv()
		c := localConnector(e, "fail-start")
		p, err := e.svc.Test(specCtx(), "u1", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Error).NotTo(BeEmpty())
		Expect(p.Error).NotTo(ContainSubstring("\n"), "the error stays one sentence")
		Expect(p.Error).To(ContainSubstring("EOF"))
		// A test binary built with coverage adds its own line to stderr when
		// it exits through os.Exit without GOCOVERDIR, so match the start.
		Expect(p.Output).To(HavePrefix("fatal: cannot open the database with key ***"))
		Expect(p.Error + p.Output).NotTo(ContainSubstring(helperKey))
		Expect(p.Tools).To(BeEmpty())
	})

	It("puts the redacted output in the log when a chat cannot connect", func() {
		var buf bytes.Buffer
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}), xlog.LogLevel("info")))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })
		e := newEnv()
		c := localConnector(e, "fail-start")
		tools, missing := e.svc.Resolve(specCtx(), "u1", []string{c.ID}, false)
		Expect(tools).To(BeEmpty())
		Expect(missing).To(HaveLen(1))
		Expect(buf.String()).To(ContainSubstring("fatal: cannot open the database with key ***"))
		Expect(buf.String()).NotTo(ContainSubstring(helperKey))
	})

	It("adds the output to the error of a tool call that ends the command", func() {
		e := newEnv()
		c := localConnector(e, "die-on-call")
		tools, missing := e.svc.Resolve(specCtx(), "u1", []string{c.ID}, false)
		Expect(missing).To(BeEmpty())
		var crash *mcpconns.Tool
		for i := range tools {
			if tools[i].Name == "crash" {
				crash = &tools[i]
			}
		}
		Expect(crash).NotTo(BeNil())
		_, err := crash.Run(context.Background(), json.RawMessage(`{"text":"x"}`))
		Expect(err).To(HaveOccurred())
		first, output, found := strings.Cut(err.Error(), "\ncommand output:\n")
		Expect(found).To(BeTrue(), err.Error())
		Expect(first).To(HavePrefix("the tool crash of Local failed"))
		Expect(output).To(ContainSubstring("panic: out of memory, key ***"))
		Expect(err.Error()).NotTo(ContainSubstring(helperKey))
	})

	It("leaves a healthy command's test with no output", func() {
		e := newEnv()
		c := localConnector(e, "1")
		p, err := e.svc.Test(specCtx(), "u1", c.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Error).To(BeEmpty())
		Expect(p.Output).To(BeEmpty())
		Expect(p.Tools).To(Equal([]string{"local_echo"}))
	})
})
