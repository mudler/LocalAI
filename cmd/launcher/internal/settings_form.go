// SPDX-License-Identifier: MIT
package launcher

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// secretEdit is the state of one masked field. A field left blank keeps the
// saved value: saving something else never clears a secret.
type secretEdit struct {
	// Saved is true when the profile holds a value. The value itself is never
	// copied into the form.
	Saved       bool
	Replacement string
	Clear       bool
}

// workerForm is the editable text of the worker profile. It holds no secret
// values, only edits to them.
type workerForm struct {
	FrontendURL, AdvertisedAddress, NodeName, GRPCMaxPort string
	NATSURL, TLSCA, TLSCert, TLSKey                       string
	// EnvLines is one KEY=VALUE per line.
	EnvLines         string
	Token, JWT, Seed secretEdit
}

func newWorkerForm(c *Config) workerForm {
	w := c.Worker
	f := workerForm{
		FrontendURL:       w.Connection.FrontendURL,
		AdvertisedAddress: w.Connection.AdvertisedAddress,
		NodeName:          w.Connection.NodeName,
		NATSURL:           w.NATS.URL,
		TLSCA:             w.NATS.TLSCA,
		TLSCert:           w.NATS.TLSCert,
		TLSKey:            w.NATS.TLSKey,
		Token:             secretEdit{Saved: w.Connection.RegistrationToken != ""},
		JWT:               secretEdit{Saved: w.NATS.JWT != ""},
		Seed:              secretEdit{Saved: w.NATS.UserSeed != ""},
	}
	if w.Connection.GRPCMaxPort != nil {
		f.GRPCMaxPort = strconv.Itoa(*w.Connection.GRPCMaxPort)
	}
	keys := make([]string, 0, len(w.EnvironmentVars))
	for k := range w.EnvironmentVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+w.EnvironmentVars[k])
	}
	f.EnvLines = strings.Join(lines, "\n")
	return f
}

func (e secretEdit) resolve(saved string) string {
	switch {
	case e.Clear:
		return ""
	case e.Replacement != "":
		return e.Replacement
	default:
		return saved
	}
}

// Apply returns a copy of base with the form's worker profile. Saving needs no
// semantic validation: a blank or invalid profile is valid to retain, and the
// launch builder refuses to start it. Only text that cannot be stored is
// rejected, and errors never echo values.
func (f workerForm) Apply(base *Config) (*Config, error) {
	next := base.clone()
	c := &next.Worker.Connection
	n := &next.Worker.NATS
	c.FrontendURL, c.AdvertisedAddress, c.NodeName = f.FrontendURL, f.AdvertisedAddress, f.NodeName
	n.URL, n.TLSCA, n.TLSCert, n.TLSKey = f.NATSURL, f.TLSCA, f.TLSCert, f.TLSKey
	c.RegistrationToken = f.Token.resolve(c.RegistrationToken)
	n.JWT = f.JWT.resolve(n.JWT)
	n.UserSeed = f.Seed.resolve(n.UserSeed)

	c.GRPCMaxPort = nil
	if s := strings.TrimSpace(f.GRPCMaxPort); s != "" {
		port, err := decimalPort(s)
		if err != nil {
			return nil, fmt.Errorf("maximum gRPC port must be a number")
		}
		c.GRPCMaxPort = &port
	}

	env := map[string]string{}
	for i, line := range strings.Split(f.EnvLines, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("environment line %d must be KEY=VALUE", i+1)
		}
		k = strings.TrimSpace(k)
		if reservedLaunchKey(k) {
			return nil, fmt.Errorf("environment line %d sets a reserved worker setting; use the connection settings", i+1)
		}
		env[k] = v
	}
	next.Worker.EnvironmentVars = env
	return next, nil
}

// reachabilityPreview explains which ports the frontend must reach, derived
// from the advertised address the worker will use.
func reachabilityPreview(address, maxPort string) (string, error) {
	_, p, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("enter the address as host:port")
	}
	base, err := decimalPort(p)
	if err != nil || base < 2 {
		return "", fmt.Errorf("the base port must be 2-65535")
	}
	top := 65535
	if s := strings.TrimSpace(maxPort); s != "" {
		top, err = decimalPort(s)
		if err != nil || top < base {
			return "", fmt.Errorf("the maximum gRPC port must be %d-65535", base)
		}
	}
	return fmt.Sprintf("The frontend must reach reverse HTTP on port %d and gRPC on ports %d-%d. Each concurrent backend takes one gRPC port; choose a narrow range that fits expected concurrency. The worker needs outbound access to the frontend and the message bus.", base-1, base, top), nil
}
