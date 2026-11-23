package p2ppair

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

const (
	// inviteRetention is how long the JSON store keeps an invite that is no
	// longer pending (used, canceled or expired), so that the file does not
	// grow with every invite ever made.
	inviteRetention = 30 * 24 * time.Hour
	// touchInterval is the shortest time between two writes of the last-seen
	// time of one device; every write rewrites the whole file.
	touchInterval = time.Minute
)

// JSONFileName is the file that the JSON store keeps in its directory.
const JSONFileName = "p2p-pairing.json"

type inviteRecord struct {
	Invite
	CodeHash string `json:"code_hash"`
}

type jsonFile struct {
	Version int                     `json:"version"`
	Invites map[string]inviteRecord `json:"invites"`
	Devices map[string]Device       `json:"devices"`
}

// jsonStore keeps everything in one file (mode 0600, rewritten atomically).
// It reads the file on every call and keeps nothing in memory between calls.
// One process owns the directory; a cluster uses the GORM store.
type jsonStore struct {
	mu     sync.Mutex
	path   string
	secret string
	mem    *jsonFile
}

// NewJSONStore returns a Store that keeps its data in dir, for a deployment
// with no database. With an empty dir it keeps everything in memory.
// hmacSecret keys the hash of the codes.
func NewJSONStore(dir, hmacSecret string) (Store, error) {
	s := &jsonStore{secret: hmacSecret}
	if dir == "" {
		s.mem = newJSONFile()
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("p2ppair: create store directory: %w", err)
	}
	s.path = filepath.Join(dir, JSONFileName)
	return s, nil
}

func newJSONFile() *jsonFile {
	return &jsonFile{Version: 1, Invites: map[string]inviteRecord{}, Devices: map[string]Device{}}
}

// load reads the file. A file that does not parse is moved aside so that the
// service keeps working with an empty list; the move is logged at error level.
func (s *jsonStore) load() (*jsonFile, error) {
	if s.path == "" {
		return cloneFile(s.mem), nil
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return newJSONFile(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("p2ppair: read store: %w", err)
	}
	var f jsonFile
	if err := json.Unmarshal(raw, &f); err != nil {
		aside := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
		if rerr := os.Rename(s.path, aside); rerr != nil {
			return nil, fmt.Errorf("p2ppair: the store file is damaged and could not be moved aside: %w", rerr)
		}
		xlog.Error("The pairing file is damaged; it was moved aside and the lists start empty", "moved_to", aside)
		return newJSONFile(), nil
	}
	if f.Invites == nil {
		f.Invites = map[string]inviteRecord{}
	}
	if f.Devices == nil {
		f.Devices = map[string]Device{}
	}
	return &f, nil
}

func cloneFile(f *jsonFile) *jsonFile {
	c := newJSONFile()
	maps.Copy(c.Invites, f.Invites)
	maps.Copy(c.Devices, f.Devices)
	return c
}

func (s *jsonStore) save(f *jsonFile) error {
	if s.path == "" {
		s.mem = f
		return nil
	}
	return atomicfile.WriteJSON(s.path, f, 0o600)
}

func (s *jsonStore) CreateInvite(_ context.Context, inv Invite, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	hash := hashCode(code, s.secret)
	for id, r := range f.Invites {
		if id == inv.ID || codeMatches(r.CodeHash, hash) {
			return errors.New("p2ppair: create invite: duplicate")
		}
	}
	pruneInvites(f, inv.CreatedAt)
	f.Invites[inv.ID] = inviteRecord{Invite: inv, CodeHash: hash}
	return s.save(f)
}

// pruneInvites drops the invites that ended more than inviteRetention before now.
func pruneInvites(f *jsonFile, now time.Time) {
	for id, r := range f.Invites {
		end := r.ExpiresAt
		switch {
		case r.UsedAt != nil:
			end = *r.UsedAt
		case r.CanceledAt != nil:
			end = *r.CanceledAt
		}
		if now.Sub(end) > inviteRetention {
			delete(f.Invites, id)
		}
	}
}

func (s *jsonStore) GetInvite(_ context.Context, id string) (Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Invite{}, err
	}
	r, ok := f.Invites[id]
	if !ok {
		return Invite{}, ErrNotFound
	}
	return r.Invite, nil
}

func (s *jsonStore) ListInvites(_ context.Context) ([]Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Invite, 0, len(f.Invites))
	for _, r := range f.Invites {
		out = append(out, r.Invite)
	}
	slices.SortFunc(out, func(a, b Invite) int { return newestFirst(a.CreatedAt, b.CreatedAt, a.ID, b.ID) })
	return out, nil
}

func (s *jsonStore) CancelInvite(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	r, ok := f.Invites[id]
	if !ok {
		return ErrNotFound
	}
	if r.Status(now) != InvitePending {
		return ErrNotPending
	}
	t := now.UTC()
	r.CanceledAt = &t
	f.Invites[id] = r
	return s.save(f)
}

func (s *jsonStore) Redeem(ctx context.Context, p RedeemParams) (Invite, Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Invite{}, Device{}, err
	}
	hash := hashCode(p.Code, s.secret)
	// Compare against every invite, so the time does not tell where a match
	// sits.
	var found *inviteRecord
	for id := range f.Invites {
		r := f.Invites[id]
		if codeMatches(r.CodeHash, hash) {
			found = &r
		}
	}
	if found == nil {
		return Invite{}, Device{}, ErrInviteInvalid
	}
	now := p.Now.UTC()
	if found.Status(now) != InvitePending {
		return Invite{}, Device{}, ErrInviteInvalid
	}
	for _, d := range f.Devices {
		if d.PeerID == p.Peer.String() && d.RevokedAt == nil {
			return Invite{}, Device{}, ErrPeerPaired
		}
	}
	found.UsedAt, found.UsedByPeer, found.DeviceID = &now, p.Peer.String(), p.DeviceID
	label := p.Label
	if label == "" {
		label = found.Label
	}
	dev := Device{
		ID: p.DeviceID, OwnerID: found.OwnerID, Kind: found.Kind, Label: label,
		PeerID: p.Peer.String(), InviteID: found.ID, Scope: found.Scope, CreatedAt: now,
		NodeID: found.Scope.Labels[NodeLabel],
	}
	if p.Issue != nil {
		keyID, err := p.Issue(ctx, Tx{}, found.Invite, dev)
		if err != nil {
			// Nothing was written: f is a copy.
			return Invite{}, Device{}, err
		}
		dev.ClientKeyID = keyID
	}
	f.Invites[found.ID] = *found
	f.Devices[dev.ID] = dev
	if err := s.save(f); err != nil {
		return Invite{}, Device{}, err
	}
	return found.Invite, dev, nil
}

func (s *jsonStore) GetDevice(_ context.Context, id string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Device{}, err
	}
	d, ok := f.Devices[id]
	if !ok {
		return Device{}, ErrNotFound
	}
	return d, nil
}

func (s *jsonStore) ListDevices(_ context.Context) ([]Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(f.Devices))
	for _, d := range f.Devices {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Device) int { return newestFirst(a.CreatedAt, b.CreatedAt, a.ID, b.ID) })
	return out, nil
}

func (s *jsonStore) RenameDevice(_ context.Context, id, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	d, ok := f.Devices[id]
	if !ok {
		return ErrNotFound
	}
	d.Label = label
	f.Devices[id] = d
	return s.save(f)
}

func (s *jsonStore) SetDeviceAllowSlowPath(_ context.Context, id string, allow bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	d, ok := f.Devices[id]
	if !ok {
		return ErrNotFound
	}
	d.AllowSlowPath = allow
	f.Devices[id] = d
	return s.save(f)
}

func (s *jsonStore) SetDeviceNode(_ context.Context, id, node, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	d, ok := f.Devices[id]
	if !ok {
		return ErrNotFound
	}
	d.NodeID, d.NodeNote = node, note
	f.Devices[id] = d
	return s.save(f)
}

func (s *jsonStore) RevokeDevice(_ context.Context, id, by string, now time.Time) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Device{}, err
	}
	d, ok := f.Devices[id]
	if !ok {
		return Device{}, ErrNotFound
	}
	if d.RevokedAt != nil {
		return d, nil
	}
	t := now.UTC()
	d.RevokedAt, d.RevokedBy = &t, by
	f.Devices[id] = d
	return d, s.save(f)
}

func (s *jsonStore) TouchDevice(_ context.Context, p peer.ID, at time.Time, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	for id, d := range f.Devices {
		if d.PeerID == p.String() && d.RevokedAt == nil {
			t := at.UTC()
			if d.LastSeenAt != nil && d.LastPath == path && t.Sub(*d.LastSeenAt) < touchInterval && !t.Before(*d.LastSeenAt) {
				// The last-seen time is not worth a rewrite of the file.
				return nil
			}
			d.LastSeenAt, d.LastPath = &t, path
			f.Devices[id] = d
			return s.save(f)
		}
	}
	return nil
}

// newestFirst orders records by creation time, newest first, and then by ID.
func newestFirst(aCreated, bCreated time.Time, aID, bID string) int {
	return cmp.Or(bCreated.Compare(aCreated), cmp.Compare(aID, bID))
}
