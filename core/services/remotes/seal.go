package remotes

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// The sealing follows core/services/studio/connectors (seal.go): AES-256-GCM,
// the key in the same store (a file of mode 0600, or a table row). The
// connectors package is not imported because it pulls in the whole studio
// tree. Keep the two in step.

const sealKeySize = 32

func newSealKey() ([]byte, error) {
	k := make([]byte, sealKeySize)
	_, err := rand.Read(k)
	return k, err
}

// sealer seals the upstream keys at rest. The additional data ties a sealed
// value to its remote, so a copy in another row does not open.
type sealer struct{ aead cipher.AEAD }

func newSealer(key []byte) (*sealer, error) {
	if len(key) != sealKeySize {
		return nil, errors.New("remotes: the sealing key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

func sealAAD(id string) string { return "remote|" + id }

func (s *sealer) seal(aad string, plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(aad)), nil
}

func (s *sealer) open(aad string, sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("remotes: the sealed value is too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
}
