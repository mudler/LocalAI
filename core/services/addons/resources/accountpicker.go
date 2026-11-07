// SPDX-License-Identifier: MIT
package resources

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// AccountFinder is the directory search of every active named account: at
// most limit accounts whose display name starts with query, or the account
// without a display name whose whole address is query. It returns an
// immutable ID and a display name only (auth.AddonUsers.Accounts).
type AccountFinder func(ctx context.Context, query string, limit int) ([]gateway.UserRef, error)

// AccountPicker is the user picker of every Share dialog that searches all
// active named accounts: the kinds of /api/shares and the folders of a
// person's home. Both use this one, so they find people by the same rule: a
// display-name prefix of MinShareQueryRunes to MaxQueryRunes characters, at
// most MaxUserResults people, never the caller or the owner, and a bounded
// rate of searches per person.
type AccountPicker struct {
	find    func() AccountFinder
	limiter *searchLimiter
}

// NewAccountPicker returns a picker over find. find is asked on every search,
// so a directory that comes up later is used; a nil finder answers
// gateway.ErrUnavailable. burst and every bound the searches of one person
// (zero selects DefaultUserSearchBurst and DefaultUserSearchEvery); now is the
// clock, nil for the real one.
//
// The limits are kept in the memory of the process that answers the picker.
func NewAccountPicker(find func() AccountFinder, burst int, every time.Duration, now func() time.Time) *AccountPicker {
	if burst <= 0 {
		burst = DefaultUserSearchBurst
	}
	if every <= 0 {
		every = DefaultUserSearchEvery
	}
	return &AccountPicker{find: find, limiter: newSearchLimiter(burst, every, now)}
}

// Search runs one search of caller. owner, when set, is left out as the
// caller is; keep, when set, leaves out the people a grant would refuse. It
// answers gateway.ErrInvalidRequest for a query that is too short, too long
// or has control characters, and gateway.ErrSearchLimited when the caller has
// searched too often.
func (p *AccountPicker) Search(ctx context.Context, caller, owner, query string, limit int, keep func(id string) (bool, error)) (gateway.UserList, error) {
	var find AccountFinder
	if p.find != nil {
		find = p.find()
	}
	return pickUsers(ctx, caller, owner, query, MinShareQueryRunes, limit, find, keep, p.limiter.allow)
}

// pickUsers runs a user picker whose caller holds the owner role of the
// resource. find is the directory search, called with the number of rows to
// read; keep, when set, leaves out the people a grant would refuse. minRunes
// is the shortest query; allow, when set, takes one search from the caller's
// budget and refuses the search when it is spent.
func pickUsers(ctx context.Context, caller, owner, query string, minRunes, limit int, find AccountFinder, keep func(id string) (bool, error), allow func(user string) bool) (gateway.UserList, error) {
	query = strings.TrimSpace(query)
	if n := utf8.RuneCountInString(query); n < minRunes || n > MaxQueryRunes || strings.ContainsFunc(query, unicode.IsControl) {
		return gateway.UserList{}, gateway.ErrInvalidRequest
	}
	if allow != nil && !allow(caller) {
		return gateway.UserList{}, gateway.ErrSearchLimited
	}
	if limit <= 0 || limit > MaxUserResults {
		limit = MaxUserResults
	}
	if find == nil {
		return gateway.UserList{}, gateway.ErrUnavailable
	}
	// The owner and the caller are left out, and keep may drop more, so more
	// rows than limit are read.
	read := limit + 2
	if keep != nil {
		read = 4 * limit
	}
	found, err := find(ctx, query, read)
	if err != nil {
		return gateway.UserList{}, classify(err)
	}
	out := gateway.UserList{Users: []gateway.UserRef{}}
	for _, u := range found {
		if u.ID == caller || (owner != "" && u.ID == owner) || u.ID == "" || len(out.Users) >= limit {
			continue
		}
		if keep != nil {
			ok, err := keep(u.ID)
			if err != nil {
				return gateway.UserList{}, classify(err)
			}
			if !ok {
				continue
			}
		}
		out.Users = append(out.Users, gateway.UserRef{ID: u.ID, DisplayName: cleanName(u.DisplayName)})
	}
	return out, nil
}
