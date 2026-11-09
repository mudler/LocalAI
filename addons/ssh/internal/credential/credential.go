// SPDX-License-Identifier: MIT

// Package credential reads the secret of an SSH connection. The host keeps one
// sealed string per connection and hands it to the add-on with each call. It
// is either a private key (PEM or OpenSSH format) or "user:password".
//
// Parse is the only place that knows these two formats. When the host gets a
// credential kind with typed fields, only this function changes.
//
// A Credential also knows which strings must never reach the model: the
// password, or the key material in every form a program could print it. Scrub
// removes them from any text that leaves the add-on.
package credential

import (
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

// MaxLength is the longest secret the host stores.
const MaxLength = 4096

// Redacted replaces a secret in a scrubbed text.
const Redacted = "[redacted]"

// Kind tells how the credential logs in.
type Kind string

const (
	// KindKey is a private key. The user comes from the connection address.
	KindKey Kind = "key"
	// KindPassword is a user name and a password.
	KindPassword Kind = "password"
)

// Errors of Parse. None of them holds any part of the secret.
var (
	ErrEmpty      = errors.New("the credential is empty")
	ErrFormat     = errors.New("the credential must be a private key (PEM or OpenSSH format) or user:password")
	ErrBadKey     = errors.New("the private key could not be read; use an unencrypted key in PEM or OpenSSH format")
	ErrPassphrase = errors.New("the private key is protected by a passphrase, which is not supported; use a key without one")
	ErrControl    = errors.New("the user name or the password holds a control character")
)

// Credential is a parsed secret.
type Credential struct {
	Kind Kind
	// User is set for KindPassword only.
	User     string
	password string
	signer   ssh.Signer
	secrets  []string
}

// Parse reads the secret of a connection. Text that starts with "-----BEGIN"
// is a private key. Anything else is "user:password", split at the first
// colon, so the password may hold colons and the user may not.
func Parse(secret string) (*Credential, error) {
	text := strings.TrimSpace(secret)
	if text == "" {
		return nil, ErrEmpty
	}
	if len(secret) > MaxLength {
		return nil, ErrFormat
	}
	if strings.HasPrefix(text, "-----BEGIN") {
		return parseKey(text)
	}
	user, pass, ok := strings.Cut(secret, ":")
	if !ok || user == "" || pass == "" {
		return nil, ErrFormat
	}
	if hasControl(user) || hasControl(pass) {
		return nil, ErrControl
	}
	return &Credential{Kind: KindPassword, User: user, password: pass, secrets: []string{secret, pass}}, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func parseKey(text string) (*Credential, error) {
	signer, err := ssh.ParsePrivateKey([]byte(text))
	if err != nil {
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			return nil, ErrPassphrase
		}
		return nil, ErrBadKey
	}
	return &Credential{Kind: KindKey, signer: signer, secrets: keyFragments(text)}, nil
}

// keyFragments lists the strings that show a key: the whole text, each line of
// its body, and the body joined into one line.
func keyFragments(text string) []string {
	out := []string{text}
	var body strings.Builder
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") || strings.Contains(line, ": ") {
			continue
		}
		body.WriteString(line)
		if len(line) >= 8 {
			out = append(out, line)
		}
	}
	if body.Len() >= 8 {
		out = append(out, body.String())
	}
	return out
}

// Auth returns the login methods of the credential.
func (c *Credential) Auth() []ssh.AuthMethod {
	if c.Kind == KindKey {
		return []ssh.AuthMethod{ssh.PublicKeys(c.signer)}
	}
	return []ssh.AuthMethod{
		ssh.Password(c.password),
		// Many servers offer the password through keyboard-interactive. A
		// single prompt is answered; a server that asks more gets nothing.
		ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			if len(questions) != 1 {
				return nil, errors.New("the server asked more than a password")
			}
			return []string{c.password}, nil
		}),
	}
}

// Scrub removes every form of the secret from text. Use it on everything that
// leaves the add-on: output, errors and previews.
func (c *Credential) Scrub(text string) string {
	for _, s := range c.secrets {
		if s != "" && strings.Contains(text, s) {
			text = strings.ReplaceAll(text, s, Redacted)
		}
	}
	return text
}

// minAppears is the shortest secret that Appears looks for. A password of one
// or two letters is in almost every text; refusing all of them would make the
// tool useless. Scrub still removes it from output.
const minAppears = 6

// Appears reports whether text holds the secret in any form Scrub knows. The
// add-on refuses a command that holds the secret, so a model cannot ask the
// remote machine to echo it back or write it to a file.
func (c *Credential) Appears(text string) bool {
	for _, s := range c.secrets {
		if len(s) >= minAppears && strings.Contains(text, s) {
			return true
		}
	}
	return false
}
