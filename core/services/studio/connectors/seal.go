package connectors

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// KeySize is the size of the sealing key.
const KeySize = 32

// NewKey makes a random sealing key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeySize)
	_, err := rand.Read(k)
	return k, err
}

// Sealer seals the bot tokens at rest with AES-256-GCM. The additional data
// ties a sealed value to its binding, so it cannot be moved to another one.
type Sealer struct{ aead cipher.AEAD }

// NewSealer returns a Sealer for a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, errors.New("connectors: the sealing key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// AAD is the additional data of the token of a binding.
func AAD(id, owner string) string { return "binding|" + id + "|" + owner }

// Seal returns nonce and ciphertext.
func (s *Sealer) Seal(aad string, plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(aad)), nil
}

// Open reverses Seal. It fails when the data or the additional data changed.
func (s *Sealer) Open(aad string, sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("connectors: the sealed value is too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
}
