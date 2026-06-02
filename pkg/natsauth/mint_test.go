package natsauth_test

import (
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNatsAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NatsAuth")
}

var _ = Describe("MintWorkerJWT", func() {
	var accountSeed string

	BeforeEach(func() {
		akp, err := nkeys.CreateAccount()
		Expect(err).NotTo(HaveOccurred())
		seed, err := akp.Seed()
		Expect(err).NotTo(HaveOccurred())
		accountSeed = string(seed)
	})

	It("mints a JWT with backend worker permissions", func() {
		cfg := natsauth.Config{AccountSeed: accountSeed, WorkerJWTTTL: time.Hour}
		token, seed, err := cfg.MintWorkerJWT("550e8400-e29b-41d4-a716-446655440000", "backend")
		Expect(err).NotTo(HaveOccurred())
		Expect(token).NotTo(BeEmpty())
		Expect(seed).NotTo(BeEmpty())

		uc, err := jwt.DecodeUserClaims(token)
		Expect(err).NotTo(HaveOccurred())
		Expect(uc.Permissions.Sub.Allow).To(ContainElement("nodes.550e8400-e29b-41d4-a716-446655440000.>"))
		Expect(uc.Permissions.Pub.Allow).To(ContainElement("nodes.550e8400-e29b-41d4-a716-446655440000.backend.install.*.progress"))
	})

	It("mints agent permissions without backend install subscribe", func() {
		cfg := natsauth.Config{AccountSeed: accountSeed}
		token, _, err := cfg.MintWorkerJWT("node-1", "agent")
		Expect(err).NotTo(HaveOccurred())

		uc, err := jwt.DecodeUserClaims(token)
		Expect(err).NotTo(HaveOccurred())
		Expect(uc.Permissions.Sub.Allow).To(ContainElement("agent.execute"))
		for _, subj := range uc.Permissions.Sub.Allow {
			Expect(subj).NotTo(ContainSubstring("backend.install"))
		}
	})

	It("rejects mint without account seed", func() {
		_, _, err := (natsauth.Config{}).MintWorkerJWT("id", "backend")
		Expect(err).To(HaveOccurred())
	})
})