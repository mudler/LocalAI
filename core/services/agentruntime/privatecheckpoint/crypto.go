// SPDX-License-Identifier: MIT
package privatecheckpoint

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"io"
	"reflect"
	"strings"
	"time"
)

type AES256GCM struct{}

func (AES256GCM) Seal(k, p, a []byte) ([]byte, []byte, error) {
	if len(k) != 32 {
		return nil, nil, ErrDenied
	}
	b, e := aes.NewCipher(k)
	if e != nil {
		return nil, nil, ErrDenied
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, nil, ErrDenied
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, nil, ErrDenied
	}
	return n, g.Seal(nil, n, p, a), nil
}
func (AES256GCM) Open(k, n, c, a []byte) ([]byte, error) {
	if len(k) != 32 || len(n) != 12 || len(c) < 16 {
		return nil, ErrDenied
	}
	b, e := aes.NewCipher(k)
	if e != nil {
		return nil, ErrDenied
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, ErrDenied
	}
	p, e := g.Open(nil, n, c, a)
	if e != nil {
		return nil, ErrDenied
	}
	return p, nil
}
func EnvelopeHash(cp store.Checkpoint) [32]byte { return sha256.Sum256(cp.Context) }

type header struct {
	Version   int
	Algorithm string
	Key       KeyVersion
	Codec     string
	Binding   Binding
}
type envelope struct {
	Version           int
	Header            header
	Nonce, Ciphertext []byte
}

// Internal encoding intentionally bypasses public redaction. It is never an
// exported serializer and is used only for encrypted storage and authenticated AAD.
func wire(v any) []byte { b, _ := json.Marshal(wireValue(reflect.ValueOf(v))); return b }
func wireValue(v reflect.Value) any {
	if v.Type() == reflect.TypeOf(time.Time{}) {
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Struct:
		m := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			m[v.Type().Field(i).Name] = wireValue(v.Field(i))
		}
		return m
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return v.Interface()
		}
		a := make([]any, v.Len())
		for i := range a {
			a[i] = wireValue(v.Index(i))
		}
		return a
	default:
		return v.Interface()
	}
}

// strict checks duplicate keys at every depth (including case aliases), bounds
// recursion, rejects unknown fields, and consumes exactly one JSON value.
func strict(b []byte, v any) error {
	if len(b) > MaxEnvelope {
		return ErrDenied
	}
	d := json.NewDecoder(bytes.NewReader(b))
	var scan func(int) error
	scan = func(depth int) error {
		if depth > 64 {
			return ErrDenied
		}
		t, e := d.Token()
		if e != nil {
			return ErrDenied
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return ErrDenied
					}
					s, ok := k.(string)
					if !ok {
						return ErrDenied
					}
					s = strings.ToLower(s)
					if seen[s] {
						return ErrDenied
					}
					seen[s] = true
					if e = scan(depth + 1); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e = scan(depth + 1); e != nil {
						return e
					}
				}
			default:
				return ErrDenied
			}
			_, e = d.Token()
			if e != nil {
				return ErrDenied
			}
		}
		return nil
	}
	if scan(0) != nil {
		return ErrDenied
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrDenied
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrDenied
	}
	return nil
}
