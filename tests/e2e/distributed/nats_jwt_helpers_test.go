package distributed_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
)

// JWTTestInfra holds a NATS server configured with JWT auth and minted worker credentials.
type JWTTestInfra struct {
	*TestInfra
	AccountSeed string
	NodeID      string
	WorkerJWT   string
	WorkerSeed  string
}

// SetupJWTInfra starts NATS with an in-memory JWT resolver and returns worker credentials
// minted the same way as node registration (pkg/natsauth).
func SetupJWTInfra() *JWTTestInfra {
	GinkgoHelper()

	infra := &JWTTestInfra{TestInfra: &TestInfra{Ctx: context.Background()}}

	operatorJWT, accountJWT, accountSeed, err := jwtResolverMaterial()
	Expect(err).ToNot(HaveOccurred())
	infra.AccountSeed = accountSeed

	conf := fmt.Sprintf(`listen: 0.0.0.0:4222

operator: %s

resolver: MEMORY
resolver_preload: {
	%s: %s
}
`, operatorJWT, accountPublicKeyFromSeed(accountSeed), accountJWT)

	var natsContainer *tcnats.NATSContainer
	// Override default testcontainers -js: JetStream fails without a system account in JWT mode.
	natsContainer, err = tcnats.Run(infra.Ctx, "nats:2-alpine",
		tcnats.WithConfigFile(bytes.NewBufferString(conf)),
		testcontainers.WithCmd("-c", "/etc/nats.conf"),
	)
	Expect(err).ToNot(HaveOccurred())
	infra.NATSContainer = natsContainer

	infra.NatsURL, err = infra.NATSContainer.ConnectionString(infra.Ctx)
	Expect(err).ToNot(HaveOccurred())

	infra.NodeID = "550e8400-e29b-41d4-a716-446655440000"
	cfg := natsauth.Config{AccountSeed: infra.AccountSeed, WorkerJWTTTL: time.Hour}
	infra.WorkerJWT, infra.WorkerSeed, err = cfg.MintWorkerJWT(infra.NodeID, "backend")
	Expect(err).ToNot(HaveOccurred())

	infra.NC, err = messaging.New(infra.NatsURL, messaging.WithUserJWT(infra.WorkerJWT, infra.WorkerSeed))
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		if infra.NC != nil {
			infra.NC.Close()
		}
		if infra.NATSContainer != nil {
			_ = infra.NATSContainer.Terminate(context.Background())
		}
	})

	return infra
}

// jwtResolverMaterial builds operator + account JWTs for a MEMORY resolver.
// Follows the NATS JWT tutorial: self-signed account, then operator re-sign, with the
// account identity key listed as a signing key so MintWorkerJWT can use the account seed.
func jwtResolverMaterial() (operatorJWT, accountJWT, accountSeed string, err error) {
	okp, err := nkeys.CreateOperator()
	if err != nil {
		return "", "", "", err
	}
	opk, err := okp.PublicKey()
	if err != nil {
		return "", "", "", err
	}
	oc := jwt.NewOperatorClaims(opk)
	oc.Name = "localai-test-operator"
	oskp, err := nkeys.CreateOperator()
	if err != nil {
		return "", "", "", err
	}
	ospk, err := oskp.PublicKey()
	if err != nil {
		return "", "", "", err
	}
	oc.SigningKeys.Add(ospk)
	operatorJWT, err = oc.Encode(okp)
	if err != nil {
		return "", "", "", err
	}

	akp, err := nkeys.CreateAccount()
	if err != nil {
		return "", "", "", err
	}
	seed, err := akp.Seed()
	if err != nil {
		return "", "", "", err
	}
	accountSeed = string(seed)

	apk, err := akp.PublicKey()
	if err != nil {
		return "", "", "", err
	}
	ac := jwt.NewAccountClaims(apk)
	ac.Name = "localai-test-account"
	ac.SigningKeys.Add(apk)
	accountJWT, err = ac.Encode(akp)
	if err != nil {
		return "", "", "", err
	}
	ac, err = jwt.DecodeAccountClaims(accountJWT)
	if err != nil {
		return "", "", "", err
	}
	accountJWT, err = ac.Encode(oskp)
	if err != nil {
		return "", "", "", err
	}
	return operatorJWT, accountJWT, accountSeed, nil
}

func accountPublicKeyFromSeed(accountSeed string) string {
	akp, err := nkeys.FromSeed([]byte(accountSeed))
	Expect(err).ToNot(HaveOccurred())
	pk, err := akp.PublicKey()
	Expect(err).ToNot(HaveOccurred())
	return pk
}

// nodeSubjectPrefix returns the sanitized nodes.* prefix for a node ID.
func nodeSubjectPrefix(nodeID string) string {
	tok := strings.NewReplacer(".", "-", "*", "-", ">", "-", " ", "-", "\t", "-", "\n", "-").Replace(nodeID)
	return "nodes." + tok
}