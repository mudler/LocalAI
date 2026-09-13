package credentials

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/mudler/xlog"
)

// Kind is how a credential authenticates.
type Kind int

const (
	KindBasic Kind = iota + 1
	KindBearer
	KindHeader
)

func (k Kind) String() string {
	switch k {
	case KindBasic:
		return "basic"
	case KindBearer:
		return "bearer"
	case KindHeader:
		return "header"
	}
	return "unknown"
}

// LookupEnvFunc has the signature of os.LookupEnv. It is injected because
// packages under pkg/ must not read the process environment directly; the CLI
// layer passes os.LookupEnv.
type LookupEnvFunc func(string) (string, bool)

type secretRef struct {
	// literal is a pointer because fmt prints a nested pointer as an address.
	// A Credential reached through an unexported field (inside Store, or any
	// caller struct) bypasses String, so a plain string would print in full.
	literal *string
	env     string
	file    string
}

func (r secretRef) set() bool {
	return r.literal != nil || r.env != "" || r.file != ""
}

// Credential is one rule from the credentials file. Secret sources are
// unexported and resolved only when a request needs them.
type Credential struct {
	// Match is the rule's match string as written. It never holds secret
	// material, so it is what errors and logs use to name a rule.
	Match string
	Kind  Kind

	username    string
	password    secretRef
	bearer      secretRef
	headerName  string
	headerValue secretRef

	allowInsecure bool
	scheme        string
	host          string
	path          string
	lookupEnv     LookupEnvFunc
}

// ErrUnresolvedSecret marks a failure to read a rule's secret. It is a
// configuration problem, so downloads must not retry it like a network error.
var ErrUnresolvedSecret = errors.New("credential secret unavailable")

type resolvedSecret struct {
	username    string
	password    string
	bearer      string
	headerName  string
	headerValue string
}

func (r secretRef) resolve(lookupEnv LookupEnvFunc) (string, error) {
	switch {
	case r.literal != nil:
		return *r.literal, nil
	case r.env != "":
		if lookupEnv != nil {
			if v, ok := lookupEnv(r.env); ok && v != "" {
				return v, nil
			}
		}
		return "", fmt.Errorf("environment variable %s is not set", r.env)
	case r.file != "":
		b, err := os.ReadFile(r.file)
		if err != nil {
			return "", fmt.Errorf("reading secret file %s: %w", r.file, err)
		}
		// Secret mounts conventionally end with a newline that is not part
		// of the value; sending it breaks the Authorization header.
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return "", nil
}

// resolve reads secrets at use time rather than at load, so a rotated K8s
// secret mount is picked up without restarting LocalAI.
func (c Credential) resolve() (resolvedSecret, error) {
	var out resolvedSecret
	var err error
	switch c.Kind {
	case KindBasic:
		out.username = c.username
		out.password, err = c.password.resolve(c.lookupEnv)
	case KindBearer:
		out.bearer, err = c.bearer.resolve(c.lookupEnv)
	case KindHeader:
		out.headerName = c.headerName
		out.headerValue, err = c.headerValue.resolve(c.lookupEnv)
	}
	if err != nil {
		return resolvedSecret{}, fmt.Errorf("credential %q: %w: %w", c.Match, ErrUnresolvedSecret, err)
	}
	return out, nil
}

// ApplyHeaders adds the credential to h.
func (c Credential) ApplyHeaders(h http.Header) error {
	r, err := c.resolve()
	if err != nil {
		return err
	}
	switch c.Kind {
	case KindBasic:
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(r.username+":"+r.password)))
	case KindBearer:
		h.Set("Authorization", "Bearer "+r.bearer)
	case KindHeader:
		h.Set(r.headerName, r.headerValue)
	}
	return nil
}

// warnIfUnresolved keeps a rule whose secret is not available yet, because a
// secret mount can appear after startup, but tells the operator now instead of
// at the first failing download.
func (c Credential) warnIfUnresolved() {
	if _, err := c.resolve(); err != nil {
		xlog.Warn("Download credential cannot be resolved yet; matching downloads will fail until it can", "error", err)
	}
}

// String, GoString and LogValue exist so that no fmt verb and no structured
// log call can print the unexported secret fields.
func (c Credential) String() string {
	return fmt.Sprintf("credential(%s, %s)", c.Match, c.Kind)
}

func (c Credential) GoString() string {
	return c.String()
}

func (c Credential) LogValue() slog.Value {
	return slog.GroupValue(slog.String("match", c.Match), slog.String("kind", c.Kind.String()))
}
