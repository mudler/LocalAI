package cli

import (
	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/worker"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The routes of an agent worker", func() {
	DescribeTable("follow the flags of the operator",
		func(cmd AgentWorkerCMD, want []worker.Route) {
			got, err := cmd.routes()
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("a NATS URL and a token: both, NATS first", AgentWorkerCMD{NatsURL: "nats://n:4222", RegistrationToken: "t"}, []worker.Route{worker.RouteNATS, worker.RouteTunnel}),
		Entry("a token only: the tunnel", AgentWorkerCMD{RegistrationToken: "t"}, []worker.Route{worker.RouteTunnel}),
		Entry("a NATS URL only: NATS, because the tunnel needs the token", AgentWorkerCMD{NatsURL: "nats://n:4222"}, []worker.Route{worker.RouteNATS}),
		Entry("neither: NATS from the address the frontend hands over", AgentWorkerCMD{}, []worker.Route{worker.RouteNATS}),
		Entry("--routes nats", AgentWorkerCMD{RegistrationToken: "t", Routes: []string{"nats"}}, []worker.Route{worker.RouteNATS}),
	)

	It("refuses the tunnel without a registration token, and a name that is not a route", func() {
		_, err := (&AgentWorkerCMD{Routes: []string{"tunnel"}}).routes()
		Expect(err).To(MatchError(ContainSubstring("LOCALAI_JOIN_TOKEN")))
		_, err = (&AgentWorkerCMD{Routes: []string{"pigeon"}}).routes()
		Expect(err).To(HaveOccurred())
	})

	It("is a flag of the agent worker and of the worker", func() {
		k, err := parseCLI("agent-worker", "--register-to", "http://f:8080", "--routes", "nats,tunnel")
		Expect(err).ToNot(HaveOccurred())
		Expect(k.Command()).To(Equal("agent-worker"))
		k, err = parseCLI("worker", "--register-to", "http://f:8080", "--routes", "nats,tunnel")
		Expect(err).ToNot(HaveOccurred())
		Expect(k.Command()).To(Equal("worker run"))
	})
})

var _ = Describe("The join token of a worker", func() {
	const joinToken = "ljt_0123456789abcdef.secret"

	It("is the flag --join-token of the worker, the agent worker and the vLLM follower", func() {
		app := CLI
		k, err := kong.New(&app, kong.Vars{
			"backendssystempath":   "/var/lib/local-ai/backends",
			"basepath":             kong.ExpandPath("."),
			"generatedcontentpath": DefaultGeneratedContentPath(),
			"uploadpath":           DefaultUploadPath(),
			"galleries":            config.DefaultGalleriesJSON,
			"backends":             config.DefaultBackendGalleriesJSON,
			"version":              "test",
		})
		Expect(err).ToNot(HaveOccurred())

		_, err = k.Parse([]string{"worker", "--register-to", "http://f:8080", "--join-token", joinToken})
		Expect(err).ToNot(HaveOccurred())
		Expect(app.Worker.Run.JoinToken).To(Equal(joinToken))
		Expect(app.Worker.Run.RegistrationToken).To(BeEmpty(), "a worker with a join token needs no registration token")

		app.AgentWorker.JoinToken = ""
		_, err = k.Parse([]string{"agent-worker", "--register-to", "http://f:8080", "--join-token", joinToken})
		Expect(err).ToNot(HaveOccurred())
		Expect(app.AgentWorker.JoinToken).To(Equal(joinToken))
	})

	It("is read from LOCALAI_JOIN_TOKEN", func() {
		GinkgoT().Setenv("LOCALAI_JOIN_TOKEN", joinToken)
		app := CLI
		k, err := kong.New(&app, kong.Vars{
			"backendssystempath":   "/var/lib/local-ai/backends",
			"basepath":             kong.ExpandPath("."),
			"generatedcontentpath": DefaultGeneratedContentPath(),
			"uploadpath":           DefaultUploadPath(),
			"galleries":            config.DefaultGalleriesJSON,
			"backends":             config.DefaultBackendGalleriesJSON,
			"version":              "test",
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = k.Parse([]string{"worker", "--register-to", "http://f:8080"})
		Expect(err).ToNot(HaveOccurred())
		Expect(app.Worker.Run.JoinToken).To(Equal(joinToken))
	})

	It("gives an agent worker the tunnel route, as a registration token does", func() {
		got, err := (&AgentWorkerCMD{JoinToken: joinToken}).routes()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal([]worker.Route{worker.RouteTunnel}))
		got, err = (&AgentWorkerCMD{JoinToken: joinToken, Routes: []string{"nats", "websocket"}}).routes()
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal([]worker.Route{worker.RouteNATS, worker.RouteTunnel}))
	})
})
