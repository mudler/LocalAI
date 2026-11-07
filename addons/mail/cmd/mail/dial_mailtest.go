// SPDX-License-Identifier: MIT

//go:build mailtest

package main

import (
	"crypto/x509"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/mail/internal/dial"
)

// dialOptions reads mailtest.json from the directory of the executable: the
// test authority, the ports of the test servers and the loopback address to
// treat as public. This file exists only in a build made with -tags mailtest,
// for tests/addons/mail; a release is never built with that tag.
func dialOptions() dial.Options {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "mailtest.json"))
	if err != nil {
		panic(err)
	}
	var c struct {
		CA    string `json:"ca"`
		Ports []int  `json:"ports"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		panic(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(c.CA))
	return dial.Options{Ports: c.Ports, RootCAs: pool, TreatAsPublic: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
}
