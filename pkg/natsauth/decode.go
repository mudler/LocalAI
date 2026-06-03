package natsauth

import (
	"fmt"

	"github.com/nats-io/jwt/v2"
)

// DecodeUserClaims decodes a minted worker JWT for tests and diagnostics.
func DecodeUserClaims(token string) (*jwt.UserClaims, error) {
	uc, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return nil, fmt.Errorf("natsauth: decode user JWT: %w", err)
	}
	return uc, nil
}
