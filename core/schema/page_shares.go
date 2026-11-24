package schema

// PageShareGrant is one grant on a page you own. UserID is the account ID, or
// @everyone; DisplayName is empty for @everyone. Role is view, use or edit.
// Consent says whether that account allowed the current version of the page:
// none, allowed or changed (they allowed an earlier one); it is empty for
// @everyone, whose accounts cannot be listed.
type PageShareGrant struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	Consent     string `json:"consent,omitempty"`
}

// PageShareList is who a page of your home is shared with. Enabled is false
// when an administrator turned off the pages that people share: you can still
// read the list and take a share back, and you cannot add one. AllowedCount is
// how many of the named accounts allowed the current version, and Hash is the
// hash of that version.
type PageShareList struct {
	Name         string           `json:"name"`
	Enabled      bool             `json:"enabled"`
	Hash         string           `json:"hash,omitempty"`
	AllowedCount int              `json:"allowedCount"`
	Grants       []PageShareGrant `json:"grants"`
}

// PageShareRequest gives principal a role on a page. Principal is the account
// ID that the user picker returned, or @everyone. Role is view, use or edit;
// edit is for named accounts only.
type PageShareRequest struct {
	Principal string `json:"principal"`
	Role      string `json:"role"`
}
