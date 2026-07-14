package auth

// emailForRoleDecision returns the email value to use for role assignment
// (admin promotion, admin-email invite bypass) when handling an OAuth/OIDC
// callback. An unverified email must NOT be honoured for these checks —
// otherwise an attacker who can register on the configured IdP with an
// unverified copy of LOCALAI_ADMIN_EMAIL would inherit admin role on first
// login (via AssignRole) or on every subsequent login (via MaybePromote).
//
// Profile/display uses of the email are unaffected: those happen elsewhere
// in the callback and treat the email as user-supplied advisory data.
func emailForRoleDecision(email string, verified bool) string {
	if !verified {
		return ""
	}
	return email
}
