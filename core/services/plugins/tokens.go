package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/plugins/store"
)

// TokenTTL is how long a preview token stays valid.
const TokenTTL = 15 * time.Minute

// ErrTokenInvalid means the token is unknown, expired, used, or not the
// caller's.
var ErrTokenInvalid = errors.New("plugins: the preview token is not valid; inspect the plugin again")

func tokenID(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// newToken stores t under a new secret and returns the secret. The store
// keeps only its hash: the rows are shared, the secret is the caller's.
// Mode: stateless per request; the token is a row, never a map in memory.
func (s *Service) newToken(ctx context.Context, t store.Token) (string, time.Time, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", time.Time{}, err
	}
	secret := hex.EncodeToString(b[:])
	now := s.now()
	t.ID, t.CreatedAt, t.ExpiresAt = tokenID(secret), now, now.Add(TokenTTL)
	if err := s.st.PutToken(ctx, t); err != nil {
		return "", time.Time{}, err
	}
	return secret, t.ExpiresAt, nil
}

// takeToken consumes a token of kind for the actor. A token of another
// principal is not consumed.
func (s *Service) takeToken(ctx context.Context, a Actor, secret, kind string) (store.Token, error) {
	if secret == "" {
		return store.Token{}, ErrTokenInvalid
	}
	id := tokenID(secret)
	t, err := s.st.Token(ctx, id, s.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Token{}, ErrTokenInvalid
		}
		return store.Token{}, err
	}
	if t.Principal != a.ID || t.Kind != kind {
		return store.Token{}, ErrTokenInvalid
	}
	if t, err = s.st.TakeToken(ctx, id, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Token{}, ErrTokenInvalid
		}
		return store.Token{}, fmt.Errorf("plugins: take token: %w", err)
	}
	return t, nil
}

// PreviewToken returns the token of a preview for the file route of the
// review (D1), without consuming it.
func (s *Service) PreviewToken(ctx context.Context, a Actor, secret string) (store.Token, error) {
	t, err := s.st.Token(ctx, tokenID(secret), s.now())
	if err != nil || t.Principal != a.ID {
		return store.Token{}, ErrTokenInvalid
	}
	return t, nil
}
