package natsauth

import (
	"fmt"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// MintWorkerJWT creates a signed NATS user JWT and user seed scoped to nodeID and nodeType.
// The seed is returned once at registration so the worker can sign NATS connections.
func (c Config) MintWorkerJWT(nodeID, nodeType string) (userJWT, userSeed string, err error) {
	if c.AccountSeed == "" {
		return "", "", fmt.Errorf("natsauth: account seed not configured")
	}
	if nodeID == "" {
		return "", "", fmt.Errorf("natsauth: node ID is required")
	}

	accountKP, err := nkeys.FromSeed([]byte(c.AccountSeed))
	if err != nil {
		return "", "", fmt.Errorf("natsauth: invalid account seed: %w", err)
	}

	userKP, err := nkeys.CreateUser()
	if err != nil {
		return "", "", fmt.Errorf("natsauth: create user key: %w", err)
	}
	seedBytes, err := userKP.Seed()
	if err != nil {
		return "", "", fmt.Errorf("natsauth: user seed: %w", err)
	}

	accountPub, err := accountKP.PublicKey()
	if err != nil {
		return "", "", fmt.Errorf("natsauth: account public key: %w", err)
	}
	userPub, err := userKP.PublicKey()
	if err != nil {
		return "", "", fmt.Errorf("natsauth: user public key: %w", err)
	}

	pubAllow, subAllow := WorkerPermissions(nodeID, nodeType)

	uc := jwt.NewUserClaims(userPub)
	uc.Name = fmt.Sprintf("localai-%s-%s", nodeType, workerSubjectToken(nodeID))
	uc.IssuerAccount = accountPub
	uc.Expires = time.Now().Add(c.WorkerTTL()).Unix()

	uc.Permissions.Pub.Allow = pubAllow
	uc.Permissions.Sub.Allow = subAllow

	token, err := uc.Encode(accountKP)
	if err != nil {
		return "", "", fmt.Errorf("natsauth: encode user JWT: %w", err)
	}
	return token, string(seedBytes), nil
}
