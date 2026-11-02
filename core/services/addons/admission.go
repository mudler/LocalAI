// SPDX-License-Identifier: MIT
package addons

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
)

// HostRequirementsRefusal is returned only by owner-admin lifecycle operations.
// It contains verified request names, never policy paths or grants of others.
type HostRequirementsRefusal struct{ Summary hostops.RequestSummary }

func (e *HostRequirementsRefusal) Error() string {
	return fmt.Sprintf("required host operations unavailable: %s; configure host policy and restart", strings.Join(e.Summary.MissingRequired, ", "))
}
func (m *Manager) requests(r packages.PackageReceipt) hostops.RequestSummary {
	return hostops.Requests(r.Manifest.Capabilities.HostOperations, hostops.SupportedOperations(), m.hostOps[r.Manifest.ID])
}
func (m *Manager) admit(r packages.PackageReceipt) error {
	s := m.requests(r)
	if len(s.MissingRequired) != 0 {
		return &HostRequirementsRefusal{Summary: s}
	}
	return nil
}
func (m *Manager) admission(id string) error {
	if r, ok := currentReceipt(m.journal.Addons[id]); ok {
		return m.admit(r)
	}
	return nil
}

func summaryPointer(s hostops.RequestSummary) *hostops.RequestSummary {
	if s.RequiredCount+s.OptionalCount == 0 {
		return nil
	}
	return &s
}

func copyRequestSummary(s *hostops.RequestSummary) *hostops.RequestSummary {
	if s == nil {
		return nil
	}
	out := *s
	out.Required = slices.Clone(s.Required)
	out.Optional = slices.Clone(s.Optional)
	out.Granted = slices.Clone(s.Granted)
	out.MissingRequired = slices.Clone(s.MissingRequired)
	out.MissingOptional = slices.Clone(s.MissingOptional)
	return &out
}
