// SPDX-License-Identifier: MIT
package connectors

import (
	"context"
	"maps"
	"time"
)

// mutate runs change on the data and saves it, or restores the data when the
// save fails. The caller holds s.mu.
func (s *jsonStore) mutate(change func()) error {
	grants, clients, states := maps.Clone(s.d.Grants), maps.Clone(s.d.OAuthClients), maps.Clone(s.d.LoginStates)
	change()
	if err := s.save(); err != nil {
		s.d.Grants, s.d.OAuthClients, s.d.LoginStates = grants, clients, states
		return err
	}
	return nil
}

func (s *jsonStore) GetGrant(_ context.Context, binding string) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.d.Grants[binding]
	if !ok {
		return Grant{}, ErrNotFound
	}
	return g.clone(), nil
}

func (s *jsonStore) PutGrant(_ context.Context, g Grant) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Bindings[g.Binding]; !ok {
		return 0, ErrNotFound
	}
	g = g.clone()
	g.Version = s.d.Grants[g.Binding].Version + 1
	g.LeaseOwner, g.LeaseUntil = "", time.Time{}
	err := s.mutate(func() { s.d.Grants[g.Binding] = g })
	if err != nil {
		return 0, err
	}
	return g.Version, nil
}

func (s *jsonStore) UpdateGrant(_ context.Context, g Grant, version int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.d.Grants[g.Binding]
	if !ok || cur.Version != version {
		return 0, nil
	}
	next := g.clone()
	next.Owner, next.AddonID = cur.Owner, cur.AddonID
	next.Version, next.LeaseOwner, next.LeaseUntil = version+1, "", time.Time{}
	if err := s.mutate(func() { s.d.Grants[g.Binding] = next }); err != nil {
		return 0, err
	}
	return next.Version, nil
}

func (s *jsonStore) LeaseGrant(_ context.Context, binding string, version int64, owner string, now time.Time, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.d.Grants[binding]
	if !ok || cur.Version != version || !cur.LeaseUntil.Before(now) {
		return false, nil
	}
	cur.LeaseOwner, cur.LeaseUntil = owner, now.Add(ttl).UTC()
	if err := s.mutate(func() { s.d.Grants[binding] = cur }); err != nil {
		return false, err
	}
	return true, nil
}

func (s *jsonStore) ReleaseGrant(_ context.Context, binding string, version int64, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.d.Grants[binding]
	if !ok || cur.Version != version || cur.LeaseOwner != owner {
		return nil
	}
	cur.LeaseOwner, cur.LeaseUntil = "", time.Time{}
	return s.mutate(func() { s.d.Grants[binding] = cur })
}

func (s *jsonStore) DeleteGrant(_ context.Context, binding string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Grants[binding]; !ok {
		return nil
	}
	return s.mutate(func() { delete(s.d.Grants, binding) })
}

func (s *jsonStore) GetOAuthClient(_ context.Context, addonID string) (OAuthClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.d.OAuthClients[addonID]
	if !ok {
		return OAuthClient{}, ErrNotFound
	}
	return c.clone(), nil
}

func (s *jsonStore) PutOAuthClient(_ context.Context, c OAuthClient) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func() { s.d.OAuthClients[c.AddonID] = c.clone() })
}

func (s *jsonStore) DeleteOAuthClient(_ context.Context, addonID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.OAuthClients[addonID]; !ok {
		return nil
	}
	return s.mutate(func() { delete(s.d.OAuthClients, addonID) })
}

func (s *jsonStore) PutLoginState(_ context.Context, st LoginState, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The ended logins are dropped in memory first; the next write saves
	// that. A refused login does not rewrite the file.
	binding, owner := 0, 0
	for h, old := range s.d.LoginStates {
		if old.Expires.Before(now) {
			delete(s.d.LoginStates, h)
			continue
		}
		if old.Binding == st.Binding {
			binding++
		}
		if old.Owner == st.Owner {
			owner++
		}
	}
	if binding >= MaxLoginsPerBinding || owner >= MaxLoginsPerOwner {
		return ErrTooManyLogins
	}
	return s.mutate(func() { s.d.LoginStates[st.Hash] = st.clone() })
}

func (s *jsonStore) ConsumeLoginState(_ context.Context, hash string, now time.Time) (LoginState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.d.LoginStates[hash]
	if !ok {
		return LoginState{}, false, nil
	}
	if err := s.mutate(func() { delete(s.d.LoginStates, hash) }); err != nil {
		return LoginState{}, false, err
	}
	if !now.Before(st.Expires) {
		return LoginState{}, false, nil
	}
	return st.clone(), true, nil
}
