package voiceprofile

import (
	"context"
	"os"
	"strings"
)

// Viewer is the user a call acts for. Every entry point that resolves a
// profile on behalf of a person goes through a Viewer, so one rule decides who
// sees, uses and deletes which profile:
//
//   - a profile with no owner belongs to the instance: everyone sees and uses
//     it, only an administrator deletes it;
//   - a profile with an owner is seen, used and deleted by that user and by
//     administrators;
//   - a profile the viewer may not see answers ErrNotFound, never a refusal,
//     so the existence of another user's profile does not leak;
//   - a viewer with None set (NoProfiles) sees no profile at all, not even an
//     instance-wide one: it is the caller of a paired client.
//
// Without accounts the one person is the administrator: use AdminViewer.
type Viewer struct {
	// UserID is the account of the caller. Empty when there are no accounts.
	UserID string
	// Admin is true for an administrator, and for every caller of a server
	// without accounts.
	Admin bool
	// None makes the viewer see and use no saved profile. It wins over Admin
	// and UserID.
	None bool
}

// NoProfiles is the viewer of a caller that must not touch any saved voice
// profile, whoever owns it: the paired client of an owner. The profiles are
// the owner's recordings, and the client has no account of its own.
func NoProfiles() Viewer { return Viewer{None: true} }

// UnsafeVoice reports whether a voice value that is not a profile reference
// looks like a path: it has a path separator or "..". The TTS backends that
// clone from a reference clip take a voice value as a file name, so a user who
// is not an administrator must not choose one.
func UnsafeVoice(voice string) bool {
	return strings.ContainsAny(voice, `/\`) || strings.Contains(voice, "..")
}

// AdminViewer is the viewer of a server without accounts.
func AdminViewer() Viewer { return Viewer{Admin: true} }

// CanSee reports whether the viewer may list, preview and synthesize with p.
func (v Viewer) CanSee(p Profile) bool {
	return !v.None && (v.Admin || p.OwnerID == "" || p.OwnerID == v.UserID)
}

// CanDelete reports whether the viewer may delete p.
func (v Viewer) CanDelete(p Profile) bool {
	return !v.None && (v.Admin || (p.OwnerID != "" && p.OwnerID == v.UserID))
}

// NewOwner is the owner stamped on a profile the viewer creates. An
// administrator creates instance-wide profiles, as before ownership existed,
// so the library the whole instance shares stays in the hands of the
// administrators; any other user owns what they create.
func (v Viewer) NewOwner() string {
	if v.Admin {
		return ""
	}
	return v.UserID
}

// ListFor returns the profiles the viewer may see, newest first.
func (s *Store) ListFor(ctx context.Context, v Viewer) ([]Profile, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, p := range all {
		if v.CanSee(p) {
			p.CanDelete = v.CanDelete(p)
			out = append(out, p)
		}
	}
	return out, nil
}

// GetFor returns one profile the viewer may see; any other answers ErrNotFound.
func (s *Store) GetFor(ctx context.Context, v Viewer, id string) (Profile, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	if !v.CanSee(p) {
		return Profile{}, ErrNotFound
	}
	p.CanDelete = v.CanDelete(p)
	return p, nil
}

// CreateFor stores a profile owned by the viewer (see Viewer.NewOwner). A user
// who is not an administrator may own at most MaxProfilesPerUser profiles and MaxBytesPerUser bytes of audio.
func (s *Store) CreateFor(ctx context.Context, v Viewer, input CreateInput, references []ReferenceInput) (Profile, error) {
	if v.None {
		return Profile{}, ErrForbidden
	}
	input.OwnerID = v.NewOwner()
	input.MaxOwned, input.MaxOwnedBytes = 0, 0
	if input.OwnerID != "" {
		input.MaxOwned = MaxProfilesPerUser
		input.MaxOwnedBytes = MaxBytesPerUser
	}
	p, err := s.CreateWithReferences(ctx, input, references)
	if err != nil {
		return Profile{}, err
	}
	p.CanDelete = v.CanDelete(p)
	return p, nil
}

// OpenAudioFor is OpenAudio for a profile the viewer may see.
func (s *Store) OpenAudioFor(ctx context.Context, v Viewer, id string) (*os.File, Profile, error) {
	if _, err := s.GetFor(ctx, v, id); err != nil {
		return nil, Profile{}, err
	}
	return s.OpenAudio(ctx, id)
}

// LeaseAudiosFor is LeaseAudios for a profile the viewer may see.
func (s *Store) LeaseAudiosFor(ctx context.Context, v Viewer, id string) (Profile, []string, func(), error) {
	if _, err := s.GetFor(ctx, v, id); err != nil {
		return Profile{}, nil, nil, err
	}
	return s.LeaseAudios(ctx, id)
}

// LeaseAudioFor is LeaseAudio for a profile the viewer may see.
func (s *Store) LeaseAudioFor(ctx context.Context, v Viewer, id string) (Profile, string, func(), error) {
	if _, err := s.GetFor(ctx, v, id); err != nil {
		return Profile{}, "", nil, err
	}
	return s.LeaseAudio(ctx, id)
}

// DeleteFor deletes a profile the viewer may delete. The profile of another
// user answers ErrNotFound. An instance-wide profile, which the viewer can see
// but not delete, answers ErrForbidden; both leave it in place.
func (s *Store) DeleteFor(ctx context.Context, v Viewer, id string) error {
	return s.deleteIf(ctx, id, func(p Profile) error {
		switch {
		case !v.CanSee(p):
			return ErrNotFound
		case !v.CanDelete(p):
			return ErrForbidden
		}
		return nil
	})
}
