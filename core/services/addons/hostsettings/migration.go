// SPDX-License-Identifier: MIT
package hostsettings

import "strings"

type Mode string

const (
	Standalone  Mode = "standalone"
	Distributed Mode = "distributed"
)

// Migration is trusted host input, not a request payload. The caller must
// identify the sole effective owner configuration, not frontend replicas.
// OwnerSelected records an explicit operator decision in distributed mode.
type Migration struct {
	Mode          Mode
	Source        string
	Version       string
	OwnerSelected bool
	Candidates    []Policy
}

func provenanceValid(p Provenance) bool {
	return (p.Mode == string(Standalone) || p.Mode == string(Distributed)) &&
		strings.TrimSpace(p.Source) != "" && len(p.Source) <= 256 &&
		strings.TrimSpace(p.Version) != "" && len(p.Version) <= 128
}

func migrate(m *Migration) (document, error) {
	if m == nil || len(m.Candidates) != 1 || (m.Mode == Distributed && !m.OwnerSelected) {
		return document{}, ErrMigrationRequired
	}
	p := Provenance{Mode: string(m.Mode), Source: m.Source, Version: m.Version}
	if !provenanceValid(p) {
		return document{}, ErrInvalid
	}
	if err := validate(m.Candidates[0]); err != nil {
		return document{}, err
	}
	return document{Snapshot: clone(Snapshot{SchemaVersion: SchemaVersion, Revision: 1, Policy: m.Candidates[0], Migration: p}), Receipts: map[string]receipt{}}, nil
}
