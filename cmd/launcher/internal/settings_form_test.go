// SPDX-License-Identifier: MIT
package launcher

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The worker form edits a copy of the saved profile. Masked secrets are never
// shown, never lost by saving something else, and only changed on request.
var _ = Describe("Worker settings form", func() {
	var base *Config
	BeforeEach(func() {
		base = &Config{Mode: "worker"}
		base.Worker.Connection = WorkerConnectionConfig{FrontendURL: "https://frontend.example", RegistrationToken: "token-secret", AdvertisedAddress: "worker.example:50051", NodeName: "node"}
		base.Worker.NATS = WorkerNATSConfig{URL: "tls://nats.example:4222", JWT: "jwt-secret", UserSeed: "seed-secret"}
		base.Worker.EnvironmentVars = map[string]string{"CUDA_VISIBLE_DEVICES": "1"}
	})

	It("loads the visible fields and never the secrets", func() {
		f := newWorkerForm(base)
		Expect(f.FrontendURL).To(Equal("https://frontend.example"))
		Expect(f.NATSURL).To(Equal("tls://nats.example:4222"))
		Expect(f.EnvLines).To(Equal("CUDA_VISIBLE_DEVICES=1"))
		Expect(f.Token.Replacement + f.JWT.Replacement + f.Seed.Replacement).To(BeEmpty())
		Expect(f.Token.Saved).To(BeTrue())
		Expect(f.JWT.Saved).To(BeTrue())
	})

	It("keeps every saved secret when an unrelated field changes", func() {
		f := newWorkerForm(base)
		f.NodeName = "renamed"
		got, err := f.Apply(base)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.Connection.NodeName).To(Equal("renamed"))
		Expect(got.Worker.Connection.RegistrationToken).To(Equal("token-secret"))
		Expect(got.Worker.NATS.JWT).To(Equal("jwt-secret"))
		Expect(got.Worker.NATS.UserSeed).To(Equal("seed-secret"))
	})

	It("replaces or clears a secret only when asked", func() {
		f := newWorkerForm(base)
		f.Token.Replacement = "new-token"
		f.JWT.Clear, f.Seed.Clear = true, true
		got, err := f.Apply(base)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.Connection.RegistrationToken).To(Equal("new-token"))
		Expect(got.Worker.NATS.JWT).To(BeEmpty())
		Expect(got.Worker.NATS.UserSeed).To(BeEmpty())
	})

	It("does not modify the configuration it was given", func() {
		f := newWorkerForm(base)
		f.NodeName = "renamed"
		f.Token.Clear = true
		_, err := f.Apply(base)
		Expect(err).ToNot(HaveOccurred())
		Expect(base.Worker.Connection.NodeName).To(Equal("node"))
		Expect(base.Worker.Connection.RegistrationToken).To(Equal("token-secret"))
	})

	It("keeps the standalone profile untouched", func() {
		base.EnvironmentVars = map[string]string{"STANDALONE": "x"}
		base.Address = ":8123"
		f := newWorkerForm(base)
		f.FrontendURL = "https://elsewhere.example"
		got, err := f.Apply(base)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.EnvironmentVars).To(Equal(map[string]string{"STANDALONE": "x"}))
		Expect(got.Address).To(Equal(":8123"))
	})

	It("parses the maximum gRPC port and rejects a non-number", func() {
		f := newWorkerForm(base)
		f.GRPCMaxPort = "50100"
		got, err := f.Apply(base)
		Expect(err).ToNot(HaveOccurred())
		Expect(*got.Worker.Connection.GRPCMaxPort).To(Equal(50100))
		f.GRPCMaxPort = ""
		got, err = f.Apply(base)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.Connection.GRPCMaxPort).To(BeNil(), "empty means the worker default")
		f.GRPCMaxPort = "5x"
		_, err = f.Apply(base)
		Expect(err).To(HaveOccurred())
	})

	It("parses environment lines and refuses a reserved key without echoing the value", func() {
		f := newWorkerForm(base)
		f.EnvLines = "CUDA_VISIBLE_DEVICES=2\n\nHSA_OVERRIDE=11.0\n"
		got, err := f.Apply(base)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.EnvironmentVars).To(Equal(map[string]string{"CUDA_VISIBLE_DEVICES": "2", "HSA_OVERRIDE": "11.0"}))
		for _, line := range []string{"LOCALAI_NATS_REQUIRE_AUTH=false", "LOCALAI_REGISTER_TO=secret-value", "NOEQUALS"} {
			f.EnvLines = line
			_, err = f.Apply(base)
			Expect(err).To(HaveOccurred(), line)
			Expect(err.Error()).ToNot(ContainSubstring("secret-value"))
		}
	})

	It("does not validate the profile to save it: a blank or invalid inactive draft is retained", func() {
		f := newWorkerForm(&Config{})
		f.FrontendURL = "not a url"
		got, err := f.Apply(&Config{})
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Worker.Connection.FrontendURL).To(Equal("not a url"))
	})

	Describe("reachability preview", func() {
		It("shows the reverse HTTP port and the whole gRPC range", func() {
			text, err := reachabilityPreview("worker.example:50051", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(text).To(ContainSubstring("50050"))
			Expect(text).To(ContainSubstring("50051"))
			Expect(text).To(ContainSubstring("65535"))
			text, err = reachabilityPreview("[2001:db8::1]:50051", "50060")
			Expect(err).ToNot(HaveOccurred())
			Expect(text).To(ContainSubstring("50051-50060"))
		})
		It("reports an address or range it cannot derive", func() {
			for _, c := range [][2]string{{"worker.example", ""}, {"worker.example:1", ""}, {"worker.example:50051", "50000"}, {"worker.example:50051", "70000"}} {
				_, err := reachabilityPreview(c[0], c[1])
				Expect(err).To(HaveOccurred(), c[0]+" "+c[1])
			}
		})
	})
})
