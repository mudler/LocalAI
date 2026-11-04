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
type HostRequirementsRefusal struct {
	Summary hostops.RequestSummary
	// SourceChanged is set when the package comes from another library or
	// signer than the installed one. The grants of the installed package do not
	// carry over to it without a new approval.
	SourceChanged bool
}

func (e *HostRequirementsRefusal) Error() string {
	if e.SourceChanged && !e.Blocked() {
		return "this package comes from another library or signer than the installed one; review its host access in the browser and approve it"
	}
	if e.Blocked() {
		return fmt.Sprintf("this add-on needs %s, which the administrator policy blocks; it cannot be installed or started", strings.Join(e.Summary.DeniedRequired, ", "))
	}
	return fmt.Sprintf("required host operations unavailable: %s; approve host access in the browser, or set the host policy and restart", strings.Join(e.Summary.MissingRequired, ", "))
}

// Blocked reports whether a deny rule forbids a required operation. Such an
// add-on cannot be approved, only unblocked by the administrator.
func (e *HostRequirementsRefusal) Blocked() bool { return len(e.Summary.DeniedRequired) != 0 }

// Code is the failure code of an operation that this refusal ended.
func (e *HostRequirementsRefusal) Code() string {
	if e.Blocked() {
		return "blocked_by_policy"
	}
	return "approval_required"
}
func (m *Manager) requests(r packages.PackageReceipt) hostops.RequestSummary {
	return hostops.RequestsFor(r.Manifest.Capabilities.HostOperations, hostops.SupportedOperations(), m.pendingFor(r.Manifest.ID), r.Manifest.ID)
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

// held lists what an add-on holds now as operation prefixes and names, the
// form the apps list reads to decide which host controls to draw.
func held(a hostops.Authority, id string) []string {
	set := a.Snapshot(id)
	return append(slices.Clone(set.Prefixes), set.Operations...)
}
