// SPDX-License-Identifier: MIT
package launcher

import (
	"fmt"
	"github.com/alecthomas/kong"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
)

var _ = Describe("Secure launcher launch", func() {
	var c *Config
	BeforeEach(func() {
		c = &Config{Mode: "worker", ModelsPath: "/models", BackendsPath: "/backends", Worker: WorkerConfig{Connection: WorkerConnectionConfig{FrontendURL: "https://frontend.example/prefix/", RegistrationToken: "registration-secret", AdvertisedAddress: "[2001:db8::1]:50051"}, NATS: WorkerNATSConfig{URL: "tls://nats.example:4222"}}}
	})
	It("parses the actual generic worker Config and global context CLI tags", func() {
		c.LogLevel = "info"
		d, e := buildLaunch(c, "/data", nil, false)
		Expect(e).NotTo(HaveOccurred())
		var cli struct {
			cliContext.Context `embed:""`
			Worker             worker.Config `cmd:""`
		}
		parser, e := kong.New(&cli, kong.Vars{"basepath": "/data", "backends": "[]", "galleries": "[]"})
		Expect(e).NotTo(HaveOccurred())
		GinkgoT().Setenv("LOCALAI_REGISTRATION_TOKEN", c.Worker.Connection.RegistrationToken)
		_, e = parser.Parse(d.Args())
		Expect(e).NotTo(HaveOccurred())
		Expect(cli.Worker.NatsAuthRequired()).To(BeTrue())
		Expect(cli.Worker.RegistrationAuthRequired()).To(BeTrue())
		Expect(cli.Worker.RegistrationToken).To(Equal(c.Worker.Connection.RegistrationToken))
		Expect(cli.Worker.RegisterTo).To(Equal("https://frontend.example/prefix"))
		Expect(cli.Worker.Addr).To(Equal(c.Worker.Connection.AdvertisedAddress))
		Expect(cli.Worker.GRPCMaxPort).To(BeZero())
	})
	It("accepts a matching static user JWT and seed only in the child environment", func() {
		account, e := nkeys.CreateAccount()
		Expect(e).NotTo(HaveOccurred())
		defer account.Wipe()
		user, e := nkeys.CreateUser()
		Expect(e).NotTo(HaveOccurred())
		defer user.Wipe()
		pub, e := user.PublicKey()
		Expect(e).NotTo(HaveOccurred())
		claims := jwt.NewUserClaims(pub)
		token, e := claims.Encode(account)
		Expect(e).NotTo(HaveOccurred())
		seed, e := user.Seed()
		Expect(e).NotTo(HaveOccurred())
		c.Worker.NATS.JWT = token
		c.Worker.NATS.UserSeed = string(seed)
		d, e := buildLaunch(c, "/data", nil, false)
		Expect(e).NotTo(HaveOccurred())
		Expect(d.CredentialMode()).To(ContainSubstring("no automatic renewal"))
		Expect(d.Env()).To(ContainElement("LOCALAI_NATS_JWT=" + token))
		Expect(strings.Join(d.Args(), " ") + fmt.Sprintf("%+v %#v", d, d)).NotTo(ContainSubstring(token))
	})
	It("creates unique owner-only logs and refuses unsafe directories", func() {
		dir := GinkgoT().TempDir()
		f, e := secureLogFile(dir)
		Expect(e).NotTo(HaveOccurred())
		defer f.Close()
		st, e := f.Stat()
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0600)))
		other := GinkgoT().TempDir()
		Expect(os.Symlink(dir, filepath.Join(other, "logs"))).To(Succeed())
		_, e = secureLogFile(other)
		Expect(e).To(HaveOccurred())
	})

	It("builds an isolated authenticated generic worker description", func() {
		d, err := buildLaunch(c, "/data", []string{"LOCALAI_NATS_JWT=hostile", "MODELS_PATH=/evil", "CUDA_VISIBLE_DEVICES=0"}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Args()).To(ContainElements("worker", "--distributed-require-auth", "https://frontend.example/prefix", "--addr", "[2001:db8::1]:50051"))
		Expect(strings.Join(d.Args(), " ")).NotTo(ContainSubstring("registration-secret"))
		Expect(d.Env()).To(ContainElements("LOCALAI_REGISTRATION_TOKEN=registration-secret", "CUDA_VISIBLE_DEVICES=0"))
		Expect(strings.Join(d.Env(), " ")).NotTo(ContainSubstring("hostile"))
		Expect(d.CredentialMode()).To(Equal("registration-issued"))
		args := d.Args()
		args[0] = "run"
		c.Worker.Connection.RegistrationToken = "changed"
		Expect(d.Args()[0]).To(Equal("worker"))
		Expect(d.Env()).To(ContainElement("LOCALAI_REGISTRATION_TOKEN=registration-secret"))
	})
	It("preserves standalone arguments and environment precedence", func() {
		c.Mode = "standalone"
		c.Address = ":8080"
		c.EnvironmentVars = map[string]string{"CUDA_VISIBLE_DEVICES": "2"}
		l := &Launcher{config: c}
		d, e := buildLaunch(c, l.GetDataPath(), []string{"CUDA_VISIBLE_DEVICES=0"}, false)
		Expect(e).NotTo(HaveOccurred())
		Expect(d.Args()).To(Equal(l.BuildRunArgs()))
		Expect(d.Env()).To(Equal([]string{"CUDA_VISIBLE_DEVICES=2"}))
		Expect(d.Dir()).To(BeEmpty())
	})
	It("fails closed at the existing start entry point", func() {
		l := &Launcher{config: c}
		Expect(l.StartLocalAI()).To(MatchError(ContainSubstring("not available")))
	})
	It("requires explicit plaintext acknowledgement", func() {
		c.Worker.Connection.FrontendURL = "http://frontend.example"
		_, e := buildLaunch(c, "/data", nil, false)
		Expect(e).To(HaveOccurred())
		_, e = buildLaunch(c, "/data", nil, true)
		Expect(e).NotTo(HaveOccurred())
	})
	It("rejects ambiguous URLs without echoing input", func() {
		for _, v := range []string{"https://user:secret@host", "https://host?q=secret", "https://host/#secret", "https://host:", "https://host:65536", "https://host/a b", "https://host/%0a"} {
			c.Worker.Connection.FrontendURL = v
			_, e := buildLaunch(c, "/data", nil, false)
			Expect(e).To(HaveOccurred(), v)
			Expect(e.Error()).NotTo(ContainSubstring(v))
		}
	})
	It("rejects unsupported NATS forms", func() {
		for _, v := range []string{"wss://host", "tls://host/path", "tls://a,tls://b", "tls://u:p@host", "tls://host?token=x"} {
			c.Worker.NATS.URL = v
			_, e := buildLaunch(c, "/data", nil, false)
			Expect(e).To(HaveOccurred(), v)
		}
	})
	It("validates address and maximum port boundaries", func() {
		for _, v := range []string{"host:1", "host:65536", ":50051", "0.0.0.0:5", "[::]:5", "http://host:5", "host:+5"} {
			c.Worker.Connection.AdvertisedAddress = v
			_, e := buildLaunch(c, "/data", nil, false)
			Expect(e).To(HaveOccurred(), v)
		}
		c.Worker.Connection.AdvertisedAddress = "host:65535"
		max := 65535
		c.Worker.Connection.GRPCMaxPort = &max
		_, e := buildLaunch(c, "/data", nil, false)
		Expect(e).NotTo(HaveOccurred())
		max = 65534
		_, e = buildLaunch(c, "/data", nil, false)
		Expect(e).To(HaveOccurred())
	})
	It("rejects empty credentials, controls and incomplete static pairs", func() {
		for _, v := range []string{"", "  ", "secret\n"} {
			c.Worker.Connection.RegistrationToken = v
			_, e := buildLaunch(c, "/data", nil, false)
			Expect(e).To(HaveOccurred())
		}
		c.Worker.Connection.RegistrationToken = "safe"
		c.Worker.NATS.JWT = "jwt"
		_, e := buildLaunch(c, "/data", nil, false)
		Expect(e).To(HaveOccurred())
		c.Worker.NATS.UserSeed = "seed"
		_, e = buildLaunch(c, "/data", nil, false)
		Expect(e).To(HaveOccurred())
	})
	It("rejects every reserved advanced override and strips inherited aliases", func() {
		for _, key := range strings.Fields(reservedLaunchKeys) {
			c.Worker.EnvironmentVars = map[string]string{key: "false"}
			_, e := buildLaunch(c, "/data", nil, false)
			Expect(e).To(HaveOccurred(), key)
			c.Worker.EnvironmentVars = nil
			d, e := buildLaunch(c, "/data", []string{key + "=evil"}, false)
			Expect(e).NotTo(HaveOccurred())
			Expect(d.Env()).NotTo(ContainElement(key + "=evil"))
		}
	})
	It("checks TLS files and pairing independently", func() {
		c.Worker.NATS.TLSCert = "missing"
		_, e := buildLaunch(c, "/data", nil, false)
		Expect(e).To(HaveOccurred())
		c.Worker.NATS.TLSCert = ""
		c.Worker.NATS.TLSCA = GinkgoT().TempDir()
		_, e = buildLaunch(c, "/data", nil, false)
		Expect(e).To(HaveOccurred())
		p := filepath.Join(GinkgoT().TempDir(), "ca")
		Expect(os.WriteFile(p, []byte("test"), 0600)).To(Succeed())
		c.Worker.NATS.TLSCA = p
		_, e = buildLaunch(c, "/data", nil, false)
		Expect(e).NotTo(HaveOccurred())
	})
	It("redacts configured and issued credentials before emitting bounded complete lines", func() {
		var lines []string
		r := newLogFilter([]string{"my-secret"})
		r.consume(strings.NewReader("my-secret\nAuthorization: Bearer new-secret\ntls://u:p@host\neyJhbGciOi.test.signature\nSU"+strings.Repeat("A", 56)+"\n"+strings.Repeat("x", 20000)+"\nokay\npartial-secret"), func(s string) { lines = append(lines, s) })
		out := strings.Join(lines, "\n")
		for _, v := range []string{"my-secret", "new-secret", "u:p", "eyJhbGciOi", "SUAAAA", "partial-secret"} {
			Expect(out).NotTo(ContainSubstring(v))
		}
		Expect(out).To(ContainSubstring("okay"))
		Expect(len(out)).To(BeNumerically("<", 2000))
	})
})
