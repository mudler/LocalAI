package files

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// OthersWrite returns the folders of the home of principal on which another
// person, or everyone, holds write access (see workspace.OthersWrite). Every
// loader that reads the owner's skills, notes, profiles or pages with the
// owner's authority asks it and leaves out what it covers: a person who may
// write a shared folder must not put text into the owner's chats.
func (s *Service) OthersWrite(ctx context.Context, principal string) (workspace.Writable, error) {
	return workspace.OthersWrite(ctx, s.ws, s.admin, principal, session.HomePath(principal))
}

// ResolveSkills is skills.Resolve as principal, over roots, without the
// skills of a folder that another person may write (OthersWrite).
func (s *Service) ResolveSkills(ctx context.Context, principal string, roots []skills.Root, o skills.Options) ([]skills.Skill, []string, error) {
	others, err := s.OthersWrite(ctx, principal)
	if err != nil {
		return nil, nil, err
	}
	o.Ignore = others.Covers
	return skills.Resolve(ctx, s.ws, s.Actor(principal), roots, o)
}
