// SPDX-License-Identifier: MIT
package hostops

import (
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// RequestSummary describes requests, not authority. Names are bounded by the
// verified manifest (32 total, 64 bytes each); no policy paths or other addon
// grants are included. Granted counts only requested, supported operations.
type RequestSummary struct {
	Required        []string `json:"required,omitempty"`
	Optional        []string `json:"optional,omitempty"`
	Granted         []string `json:"granted,omitempty"`
	MissingRequired []string `json:"missingRequired,omitempty"`
	MissingOptional []string `json:"missingOptional,omitempty"`
	// DeniedRequired and DeniedOptional are the missing operations that a deny
	// rule forbids. They are also listed in the Missing lists. An operation
	// that is denied can never be approved, so a caller reads this to tell
	// "ask the administrator to approve" from "blocked by policy".
	DeniedRequired []string `json:"deniedRequired,omitempty"`
	DeniedOptional []string `json:"deniedOptional,omitempty"`
	RequiredCount  int      `json:"requiredCount"`
	OptionalCount  int      `json:"optionalCount"`
	GrantedCount   int      `json:"grantedCount"`
}

// Requests is side-effect free. Registry membership is exact; coverage uses the
// broker's literal nonempty prefix rule, not a regexp or a request-as-grant.
func Requests(req *contract.HostOperations, registered, grants []string) RequestSummary {
	return RequestsFor(req, registered, NewStaticAuthority(map[string][]string{"": grants}), "")
}

// RequestsFor is Requests against an Authority: an operation counts as granted
// when the authority allows it to the add-on now.
func RequestsFor(req *contract.HostOperations, registered []string, auth Authority, addonID string) RequestSummary {
	return requestsWith(req, registered, func(op string) (bool, Reason) {
		if auth == nil {
			return false, ReasonNotGranted
		}
		return auth.Allows(addonID, op)
	})
}

func requestsWith(req *contract.HostOperations, registered []string, allows func(op string) (bool, Reason)) RequestSummary {
	s := RequestSummary{}
	if req == nil {
		return s
	}
	s.Required = slices.Clone(req.Required)
	s.Optional = slices.Clone(req.Optional)
	slices.Sort(s.Required)
	slices.Sort(s.Optional)
	for i, names := range [][]string{s.Required, s.Optional} {
		for _, name := range names {
			ok, why := false, ReasonNotGranted
			if slices.Contains(registered, name) {
				ok, why = allows(name)
			}
			switch {
			case ok:
				s.Granted = append(s.Granted, name)
			case i == 0:
				s.MissingRequired = append(s.MissingRequired, name)
				if why.Denied() {
					s.DeniedRequired = append(s.DeniedRequired, name)
				}
			default:
				s.MissingOptional = append(s.MissingOptional, name)
				if why.Denied() {
					s.DeniedOptional = append(s.DeniedOptional, name)
				}
			}
		}
	}
	slices.Sort(s.Granted)
	s.RequiredCount, s.OptionalCount, s.GrantedCount = len(s.Required), len(s.Optional), len(s.Granted)
	return s
}

// SupportedOperations is the host's startup registry vocabulary. Handlers are
// wired after manager recovery as their services become available. Admission
// must not mistake that startup ordering for unsupported operations. The broker
// still requires a registered handler and revalidates every request's authority.
func SupportedOperations() []string {
	return strings.Fields(`finetune.start finetune.get finetune.list finetune.checkpoints finetune.dataset finetune.export finetune.progress finetune.stop quantize.start quantize.get quantize.list quantize.import quantize.progress quantize.stop workspace.materialize workspace.commit workspace.export workspace.import workspace.changes workspace.admin egress.check egress.audit connector.inbound connector.file.put connector.file.get`)
}
