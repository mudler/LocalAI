// SPDX-License-Identifier: MIT
package kubeapi

import (
	"context"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MaxRules is the most RBAC rules a check reports.
const MaxRules = 100

// Rule is one RBAC rule of the credential.
type Rule struct {
	Verbs         []string `json:"verbs"`
	APIGroups     []string `json:"apiGroups,omitempty"`
	Resources     []string `json:"resources,omitempty"`
	ResourceNames []string `json:"resourceNames,omitempty"`
}

// CheckResult is what the check of a connection reports.
type CheckResult struct {
	Version    string `json:"version"`
	Namespace  string `json:"namespace"`
	Rules      []Rule `json:"rules"`
	RulesCut   bool   `json:"rulesCut,omitempty"`
	Incomplete bool   `json:"incomplete,omitempty"`
}

// Check reads the version of the server and asks what the credential may do
// in one namespace (SelfSubjectRulesReview), so a person can see the RBAC of
// the connection.
func Check(ctx context.Context, c *Client, ns string) (CheckResult, error) {
	if ns == "" {
		ns = "default"
	}
	v, err := c.Discovery.ServerVersion()
	if err != nil {
		return CheckResult{}, err
	}
	rev, err := c.Typed.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx,
		&authv1.SelfSubjectRulesReview{Spec: authv1.SelfSubjectRulesReviewSpec{Namespace: ns}}, metav1.CreateOptions{})
	if err != nil {
		return CheckResult{}, err
	}
	out := CheckResult{Version: v.GitVersion, Namespace: ns, Rules: []Rule{}, Incomplete: rev.Status.Incomplete}
	for _, r := range rev.Status.ResourceRules {
		if len(out.Rules) >= MaxRules {
			out.RulesCut = true
			break
		}
		out.Rules = append(out.Rules, Rule{Verbs: r.Verbs, APIGroups: r.APIGroups, Resources: r.Resources, ResourceNames: r.ResourceNames})
	}
	return out, nil
}
