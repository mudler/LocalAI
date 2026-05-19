// Package chathistory implements server-side persistence of WebUI chat
// conversations (GitHub issue #9432). Conversations are stored as a single
// JSON file per user under {baseDir}/{userID}/conversations.json (when auth is
// active) or {baseDir}/anonymous/conversations.json (single-user / no-auth
// deployments). The store is in-memory authoritative with synchronous writes
// after every mutation so a crash never loses more than one in-flight save.
package chathistory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"
)

var (
	// ErrNotFound is returned when a conversation does not exist.
	ErrNotFound = errors.New("conversation not found")
	// ErrInvalidID is returned for malformed or unsafe conversation IDs.
	ErrInvalidID = errors.New("invalid conversation id")
	// ErrInvalidUserID is returned for malformed or unsafe user IDs.
	ErrInvalidUserID = errors.New("invalid user id")
)

// idRegex restricts conversation IDs to safe filesystem-printable runes.
// The React UI uses crypto-random IDs (see utils/format.generateId), which
// fit comfortably inside this character class.
var idRegex = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// anonymousDir is the directory used when auth is disabled (empty userID).
const anonymousDir = "anonymous"

// Store persists conversations to disk, partitioned by userID.
type Store struct {
	baseDir string

	mu    sync.Mutex
	cache map[string]map[string]schema.Conversation // userID -> id -> conv
}

// New creates a new Store rooted at baseDir. The directory is created on the
// first write — empty installs do not pollute the filesystem.
func New(baseDir string) *Store {
	return &Store{
		baseDir: baseDir,
		cache:   make(map[string]map[string]schema.Conversation),
	}
}

// validateID checks the conversation ID against idRegex.
func validateID(id string) error {
	if !idRegex.MatchString(id) {
		return ErrInvalidID
	}
	return nil
}

// validateUserID rejects path-traversal attempts in the user ID.
// Empty string is allowed (maps to anonymousDir).
func validateUserID(id string) error {
	if id == "" {
		return nil
	}
	if strings.ContainsAny(id, "/\\\x00") || strings.Contains(id, "..") {
		return ErrInvalidUserID
	}
	return nil
}

// userDir returns the directory where userID's conversation file lives.
func (s *Store) userDir(userID string) string {
	if userID == "" {
		return filepath.Join(s.baseDir, anonymousDir)
	}
	return filepath.Join(s.baseDir, userID)
}

// userFile returns the on-disk path for a user's conversations file.
func (s *Store) userFile(userID string) string {
	return filepath.Join(s.userDir(userID), "conversations.json")
}

// load reads userID's conversations from disk (cached after first read).
// Caller must hold s.mu.
func (s *Store) load(userID string) (map[string]schema.Conversation, error) {
	if cached, ok := s.cache[userID]; ok {
		return cached, nil
	}
	convs := map[string]schema.Conversation{}
	path := s.userFile(userID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.cache[userID] = convs
			return convs, nil
		}
		return nil, fmt.Errorf("read conversations file: %w", err)
	}
	var cf schema.ConversationsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse conversations file: %w", err)
	}
	for _, c := range cf.Conversations {
		convs[c.ID] = c
	}
	s.cache[userID] = convs
	return convs, nil
}

// save writes userID's conversations back to disk. Caller must hold s.mu.
func (s *Store) save(userID string, convs map[string]schema.Conversation) error {
	dir := s.userDir(userID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create user dir: %w", err)
	}

	list := make([]schema.Conversation, 0, len(convs))
	for _, c := range convs {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].UpdatedAt > list[j].UpdatedAt
	})

	cf := schema.ConversationsFile{
		Conversations: list,
		UpdatedAt:     time.Now(),
	}
	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal conversations: %w", err)
	}

	tmp := s.userFile(userID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write conversations file: %w", err)
	}
	if err := os.Rename(tmp, s.userFile(userID)); err != nil {
		return fmt.Errorf("rename conversations file: %w", err)
	}
	return nil
}

// List returns all conversations for userID, sorted newest-updated first.
func (s *Store) List(userID string) ([]schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	convs, err := s.load(userID)
	if err != nil {
		return nil, err
	}
	out := make([]schema.Conversation, 0, len(convs))
	for _, c := range convs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

// Get returns a single conversation, or ErrNotFound if absent.
func (s *Store) Get(userID, id string) (*schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	convs, err := s.load(userID)
	if err != nil {
		return nil, err
	}
	c, ok := convs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &c, nil
}

// Save upserts a conversation. CreatedAt is preserved across updates;
// UpdatedAt is refreshed on every save.
func (s *Store) Save(userID string, conv schema.Conversation) (*schema.Conversation, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateID(conv.ID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	convs, err := s.load(userID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	if existing, ok := convs[conv.ID]; ok {
		if conv.CreatedAt == 0 {
			conv.CreatedAt = existing.CreatedAt
		}
	} else if conv.CreatedAt == 0 {
		conv.CreatedAt = now
	}
	conv.UpdatedAt = now

	convs[conv.ID] = conv
	if err := s.save(userID, convs); err != nil {
		return nil, err
	}
	return &conv, nil
}

// ReplaceAll overwrites the entire conversation set for a user. The React UI
// uses this for bulk sync after a multi-tab merge or after importing from
// localStorage on first connect.
func (s *Store) ReplaceAll(userID string, convs []schema.Conversation) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	for _, c := range convs {
		if err := validateID(c.ID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	out := make(map[string]schema.Conversation, len(convs))
	for _, c := range convs {
		if c.CreatedAt == 0 {
			c.CreatedAt = now
		}
		if c.UpdatedAt == 0 {
			c.UpdatedAt = now
		}
		out[c.ID] = c
	}
	s.cache[userID] = out
	return s.save(userID, out)
}

// Delete removes a conversation, returning ErrNotFound if it does not exist.
func (s *Store) Delete(userID, id string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	if err := validateID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	convs, err := s.load(userID)
	if err != nil {
		return err
	}
	if _, ok := convs[id]; !ok {
		return ErrNotFound
	}
	delete(convs, id)
	return s.save(userID, convs)
}

// DeleteAll wipes all conversations for a user.
func (s *Store) DeleteAll(userID string) error {
	if err := validateUserID(userID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache[userID] = map[string]schema.Conversation{}
	path := s.userFile(userID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove conversations file: %w", err)
	}
	return nil
}
