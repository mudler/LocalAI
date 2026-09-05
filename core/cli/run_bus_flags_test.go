package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The frontend's broker flags are ACCEPTED and IGNORED, and the two halves are
// separate Its on purpose.
//
// Accepted, because kong fails on an unknown flag: deleting --nats-url would
// turn every existing unit file, compose file and Helm chart into a startup
// parse error on the day of the upgrade, in exchange for nothing, since the
// flag has nothing left to do. Ignored, because a flag that parses and is then
// quietly honoured is the failure this spec exists to exclude, and a single
// "it starts" assertion cannot tell the two apart.
// runFlagVars supplies the kong variables cmd/local-ai/main.go supplies, so a
// RunCMD can be parsed here at all: its path defaults interpolate ${basepath}.
func runFlagVars() kong.Vars {
	return kong.Vars{
		"basepath":             GinkgoT().TempDir(),
		"generatedcontentpath": DefaultGeneratedContentPath(),
		"uploadpath":           DefaultUploadPath(),
		"galleries":            config.DefaultGalleriesJSON,
		"backends":             config.DefaultBackendGalleriesJSON,
		"version":              "test",
	}
}

var _ = Describe("The frontend's broker flags", func() {
	busFlags := []string{
		"--nats-url", "nats://bus:4222",
		"--nats-account-seed", "SUACCOUNT",
		"--nats-service-jwt", "eyJ0",
		"--nats-service-seed", "SUSERVICE",
		"--nats-worker-jwtttl", "24h",
		"--nats-require-auth",
	}

	parse := func(args ...string) (*RunCMD, error) {
		// kong resolves env: tags from the process environment, so a
		// LOCALAI_NATS_URL inherited from a developer's shell would let the
		// first spec pass for the wrong reason.
		for _, name := range []string{"LOCALAI_NATS_URL", "LOCALAI_NATS_ACCOUNT_SEED", "LOCALAI_NATS_REQUIRE_AUTH"} {
			if prior, had := os.LookupEnv(name); had {
				Expect(os.Unsetenv(name)).To(Succeed())
				DeferCleanup(func() { _ = os.Setenv(name, prior) })
			}
		}
		var cli struct {
			Run RunCMD `cmd:""`
		}
		parser, err := kong.New(&cli, runFlagVars())
		Expect(err).ToNot(HaveOccurred())
		_, err = parser.Parse(append([]string{"run"}, args...))
		return &cli.Run, err
	}

	It("starts with no bus named at all", func() {
		_, err := parse("--distributed")
		Expect(err).To(Succeed(),
			"a distributed frontend dials no message bus and must not demand the URL of one")
	})

	It("still accepts a command line that names one", func() {
		_, err := parse(append([]string{"--distributed"}, busFlags...)...)
		Expect(err).To(Succeed())
	})

	It("does not stat the TLS material it no longer presents", func() {
		// The paths were validated as existing files while they were dialled
		// with. Keeping that validation on an ignored flag would fail a
		// deployment at startup over a certificate for a broker that is gone,
		// which is the exact upgrade the acceptance is meant to survive.
		missing := filepath.Join(GinkgoT().TempDir(), "a-broker-ca-that-was-deleted.pem")
		_, err := parse("--distributed",
			"--nats-tlsca", missing,
			"--nats-tls-cert", missing,
			"--nats-tls-key", missing)
		Expect(err).To(Succeed())
	})

	It("keeps every accepted bus flag hidden from --help", func() {
		// Accepted for the upgrade, not offered to a new operator: a flag that
		// does nothing must not appear in the list of things to configure.
		var cli struct {
			Run RunCMD `cmd:""`
		}
		parser, err := kong.New(&cli, runFlagVars())
		Expect(err).ToNot(HaveOccurred())
		var visible []string
		for _, node := range parser.Model.Children {
			for _, flag := range node.Flags {
				if len(flag.Name) >= 5 && flag.Name[:5] == "nats-" && !flag.Hidden {
					visible = append(visible, flag.Name)
				}
			}
		}
		Expect(visible).To(BeEmpty(),
			"%v are still offered in --help while doing nothing", visible)
	})
})

// The serve-backend worker's half of the same promise, which nothing pinned.
//
// `local-ai worker` kept ONE broker flag and dropped the rest a release earlier,
// when it stopped connecting to a broker at all. That asymmetry is documented
// in docs/content/reference/cli-reference.md, and a documented promise with no
// spec is how the wrong half gets deleted: --nats-url is the one an operator's
// worker unit file actually carries, and it is the one whose removal would turn
// an upgrade into a parse error on every worker in the fleet at once.
//
// The negative It is here for the same reason the frontend's two halves are
// separate. Without it, "accepted" could be satisfied by quietly re-adding the
// credential flags, and the docs would be describing a surface nobody checked.
var _ = Describe("The serve-backend worker's broker flags", func() {
	parse := func(args ...string) error {
		for _, name := range []string{"LOCALAI_NATS_URL", "LOCALAI_ADDRESS"} {
			if prior, had := os.LookupEnv(name); had {
				Expect(os.Unsetenv(name)).To(Succeed())
				DeferCleanup(func() { _ = os.Setenv(name, prior) })
			}
		}
		var cli struct {
			Worker WorkerCMD `cmd:""`
		}
		parser, err := kong.New(&cli, runFlagVars())
		Expect(err).ToNot(HaveOccurred())
		_, err = parser.Parse(append([]string{"worker"}, args...))
		return err
	}

	It("still accepts the bus URL an existing worker unit file carries", func() {
		Expect(parse("--register-to", "http://frontend:8080", "--nats-url", "nats://bus:4222")).To(Succeed(),
			"a serve-backend worker dials no message bus, and an operator whose unit file still names one must still get a worker that starts")
	})

	It("keeps it hidden from --help", func() {
		var cli struct {
			Worker WorkerCMD `cmd:""`
		}
		parser, err := kong.New(&cli, runFlagVars())
		Expect(err).ToNot(HaveOccurred())
		var visible []string
		for _, node := range parser.Model.Children {
			for _, flag := range node.Flags {
				if strings.HasPrefix(flag.Name, "nats-") && !flag.Hidden {
					visible = append(visible, flag.Name)
				}
			}
		}
		Expect(visible).To(BeEmpty(),
			"%v are still offered in --help while doing nothing", visible)
	})

	It("took no broker CREDENTIAL flag back", func() {
		// The credential and TLS flags left this command in phase 3 and must
		// stay gone: re-adding one would put a broker credential back on the
		// surface of a process that opens no broker connection, and the docs
		// say per-command which flags survive.
		Expect(parse("--register-to", "http://frontend:8080", "--nats-jwt", "eyJ0")).To(HaveOccurred())
		Expect(parse("--register-to", "http://frontend:8080", "--nats-service-jwt", "eyJ0")).To(HaveOccurred())
		Expect(parse("--register-to", "http://frontend:8080", "--nats-tlsca", "/dev/null")).To(HaveOccurred())
	})
})
