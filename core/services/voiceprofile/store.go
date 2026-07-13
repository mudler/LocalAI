// Package voiceprofile stores reusable voice-cloning reference clips and
// their transcripts. Profiles are immutable after creation so an in-flight
// synthesis request always sees the same audio and text it selected.
package voiceprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-audio/wav"
	"github.com/google/uuid"
	"github.com/mudler/xlog"
)

const (
	// DirectoryName is the persistent subdirectory created below DataPath.
	DirectoryName = "voice-profiles"

	// MaxAudioBytes caps a stored reference clip at 50 MiB. The HTTP and MCP
	// surfaces share this service-level limit so neither can bypass it.
	MaxAudioBytes int64 = 50 * 1024 * 1024

	MinAudioDuration = time.Second
	MaxAudioDuration = 2 * time.Minute

	maxNameRunes        = 80
	maxDescriptionRunes = 500
	maxTranscriptRunes  = 4000
	maxLanguageRunes    = 35

	metadataFileName = "profile.json"
	audioFileName    = "reference.wav"
	referencePrefix  = "localai://voice-profiles/"
)

var (
	ErrNotFound        = errors.New("voice profile not found")
	ErrInvalidInput    = errors.New("invalid voice profile")
	ErrConsentRequired = errors.New("voice cloning consent must be confirmed")
	ErrAudioTooLarge   = errors.New("voice reference exceeds the maximum size")
	ErrUnsupportedWAV  = errors.New("voice reference must be a PCM WAV file")
)

// AudioMetadata describes the validated reference clip without exposing its
// filesystem location.
type AudioMetadata struct {
	DurationMilliseconds int64  `json:"duration_ms"`
	SampleRate           uint32 `json:"sample_rate"`
	Channels             uint16 `json:"channels"`
	BitDepth             uint16 `json:"bit_depth"`
	SizeBytes            int64  `json:"size_bytes"`
	MIMEType             string `json:"mime_type"`
}

// Profile is the public, path-free representation of a saved cloned voice.
type Profile struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Description        string        `json:"description,omitempty"`
	Language           string        `json:"language,omitempty"`
	Transcript         string        `json:"transcript"`
	Voice              string        `json:"voice"`
	ConsentConfirmedAt time.Time     `json:"consent_confirmed_at"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	Audio              AudioMetadata `json:"audio"`
}

// CreateInput contains the administrator-provided profile metadata.
type CreateInput struct {
	Name             string
	Description      string
	Language         string
	Transcript       string
	ConsentConfirmed bool
}

// Store persists voice profiles below one root-confined directory. A Store is
// safe for concurrent use.
type Store struct {
	baseDir  string
	leaseDir string
	initErr  error
	now      func() time.Time

	initOnce sync.Once
	mu       sync.RWMutex
}

// NewStore creates a profile store rooted at <dataPath>/voice-profiles. It
// performs no filesystem I/O; errors are reported by the first operation.
func NewStore(dataPath string) *Store {
	s := &Store{
		leaseDir: filepath.Join(".leases", uuid.NewString()),
		now:      time.Now,
	}
	if strings.TrimSpace(dataPath) == "" {
		s.initErr = errors.New("voiceprofile: data path is empty")
		return s
	}
	abs, err := filepath.Abs(filepath.Join(dataPath, DirectoryName))
	if err != nil {
		s.initErr = fmt.Errorf("voiceprofile: resolve data path: %w", err)
		return s
	}
	s.baseDir = abs
	return s
}

// Reference returns the stable value clients pass in TTSRequest.voice.
func Reference(id string) string {
	return referencePrefix + id
}

// IsReference reports whether value uses the saved-profile URI scheme. It is
// useful for distinguishing a malformed profile URI from a backend speaker
// ID before ParseReference validates the UUID.
func IsReference(value string) bool {
	return strings.HasPrefix(value, referencePrefix)
}

// ParseReference recognizes a saved-profile voice value. The bool is false
// for ordinary backend speaker IDs and paths, which remain backwards
// compatible.
func ParseReference(value string) (string, bool) {
	if !IsReference(value) {
		return "", false
	}
	id := strings.TrimPrefix(value, referencePrefix)
	if validateID(id) != nil {
		return "", false
	}
	return id, true
}

func (s *Store) initialize() error {
	s.initOnce.Do(func() {
		if s.initErr != nil {
			return
		}
		if err := os.MkdirAll(s.baseDir, 0o750); err != nil {
			s.initErr = fmt.Errorf("voiceprofile: create store: %w", err)
			return
		}
		root, err := os.OpenRoot(s.baseDir)
		if err != nil {
			s.initErr = fmt.Errorf("voiceprofile: open store: %w", err)
			return
		}
		defer func() { _ = root.Close() }()
		if err := root.MkdirAll(s.leaseDir, 0o700); err != nil {
			s.initErr = fmt.Errorf("voiceprofile: create lease directory: %w", err)
		}
	})
	return s.initErr
}

func (s *Store) openRoot() (*os.Root, error) {
	if err := s.initialize(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("voiceprofile: open store: %w", err)
	}
	return root, nil
}

// Create validates and atomically persists a profile and its WAV clip.
func (s *Store) Create(ctx context.Context, input CreateInput, audio io.Reader) (Profile, error) {
	input = normalizeInput(input)
	if err := validateInput(input); err != nil {
		return Profile{}, err
	}
	if audio == nil {
		return Profile{}, fmt.Errorf("%w: audio is required", ErrInvalidInput)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	root, err := s.openRoot()
	if err != nil {
		return Profile{}, err
	}
	defer func() { _ = root.Close() }()

	id := uuid.NewString()
	tempDir := ".creating-" + uuid.NewString()
	if err := root.Mkdir(tempDir, 0o700); err != nil {
		return Profile{}, fmt.Errorf("voiceprofile: create temporary profile: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.RemoveAll(tempDir)
		}
	}()

	audioPath := filepath.Join(tempDir, audioFileName)
	dst, err := root.OpenFile(audioPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Profile{}, fmt.Errorf("voiceprofile: create reference audio: %w", err)
	}

	written, copyErr := copyWithContext(ctx, dst, io.LimitReader(audio, MaxAudioBytes+1))
	if syncErr := dst.Sync(); copyErr == nil && syncErr != nil {
		copyErr = syncErr
	}
	if closeErr := dst.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return Profile{}, fmt.Errorf("voiceprofile: store reference audio: %w", copyErr)
	}
	if written > MaxAudioBytes {
		return Profile{}, ErrAudioTooLarge
	}

	storedAudio, err := root.Open(audioPath)
	if err != nil {
		return Profile{}, fmt.Errorf("voiceprofile: reopen reference audio: %w", err)
	}
	audioMeta, validateErr := validateWAV(storedAudio, written)
	_ = storedAudio.Close()
	if validateErr != nil {
		return Profile{}, validateErr
	}

	now := s.now().UTC()
	profile := Profile{
		ID:                 id,
		Name:               input.Name,
		Description:        input.Description,
		Language:           input.Language,
		Transcript:         input.Transcript,
		Voice:              Reference(id),
		ConsentConfirmedAt: now,
		CreatedAt:          now,
		UpdatedAt:          now,
		Audio:              audioMeta,
	}
	if err := writeJSON(root, filepath.Join(tempDir, metadataFileName), profile); err != nil {
		return Profile{}, err
	}
	if err := root.Rename(tempDir, id); err != nil {
		return Profile{}, fmt.Errorf("voiceprofile: commit profile: %w", err)
	}
	committed = true
	_ = syncDirectory(root)
	return profile, nil
}

// List returns every readable profile, newest first. One damaged entry is
// skipped and logged rather than making the entire library unavailable.
func (s *Store) List(_ context.Context) ([]Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("voiceprofile: list store: %w", err)
	}
	profiles := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || validateID(entry.Name()) != nil {
			continue
		}
		profile, err := readProfile(root, entry.Name())
		if err != nil {
			xlog.Warn("Skipping unreadable voice profile", "id", entry.Name(), "error", err)
			continue
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].UpdatedAt.Equal(profiles[j].UpdatedAt) {
			return profiles[i].Name < profiles[j].Name
		}
		return profiles[i].UpdatedAt.After(profiles[j].UpdatedAt)
	})
	return profiles, nil
}

// Get returns one profile by opaque UUID.
func (s *Store) Get(_ context.Context, id string) (Profile, error) {
	if err := validateID(id); err != nil {
		return Profile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	root, err := s.openRoot()
	if err != nil {
		return Profile{}, err
	}
	defer func() { _ = root.Close() }()
	return readProfile(root, id)
}

// OpenAudio opens a profile's reference clip for authenticated preview.
func (s *Store) OpenAudio(_ context.Context, id string) (*os.File, Profile, error) {
	if err := validateID(id); err != nil {
		return nil, Profile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	root, err := s.openRoot()
	if err != nil {
		return nil, Profile{}, err
	}
	defer func() { _ = root.Close() }()
	profile, err := readProfile(root, id)
	if err != nil {
		return nil, Profile{}, err
	}
	file, err := root.Open(filepath.Join(id, audioFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Profile{}, ErrNotFound
	}
	if err != nil {
		return nil, Profile{}, fmt.Errorf("voiceprofile: open reference audio: %w", err)
	}
	return file, profile, nil
}

// LeaseAudio creates a request-scoped hard link to the immutable clip. This
// keeps an in-flight local or remote-worker synthesis safe if an administrator
// deletes the library entry concurrently. The caller must invoke release.
func (s *Store) LeaseAudio(_ context.Context, id string) (Profile, string, func(), error) {
	if err := validateID(id); err != nil {
		return Profile{}, "", nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	root, err := s.openRoot()
	if err != nil {
		return Profile{}, "", nil, err
	}
	defer func() { _ = root.Close() }()

	profile, err := readProfile(root, id)
	if err != nil {
		return Profile{}, "", nil, err
	}
	source := filepath.Join(id, audioFileName)
	lease := filepath.Join(s.leaseDir, uuid.NewString()+".wav")
	if err := root.Link(source, lease); err != nil {
		if err := copyWithinRoot(root, source, lease); err != nil {
			return Profile{}, "", nil, fmt.Errorf("voiceprofile: lease reference audio: %w", err)
		}
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			leaseRoot, openErr := s.openRoot()
			if openErr != nil {
				xlog.Warn("Failed to open voice profile store while releasing audio lease", "error", openErr)
				return
			}
			defer func() { _ = leaseRoot.Close() }()
			if removeErr := leaseRoot.Remove(lease); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				xlog.Warn("Failed to release voice profile audio", "error", removeErr)
			}
		})
	}
	return profile, filepath.Join(s.baseDir, lease), release, nil
}

// Delete removes one profile. Existing audio leases remain valid until their
// synthesis requests complete.
func (s *Store) Delete(_ context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Stat(id); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("voiceprofile: stat profile: %w", err)
	}
	if err := root.RemoveAll(id); err != nil {
		return fmt.Errorf("voiceprofile: delete profile: %w", err)
	}
	_ = syncDirectory(root)
	return nil
}

// Close removes this process's empty request-lease directory. Profiles are
// persistent and are never removed by Close.
func (s *Store) Close() error {
	if s == nil || s.baseDir == "" {
		return nil
	}
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.RemoveAll(s.leaseDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("voiceprofile: remove lease directory: %w", err)
	}
	return nil
}

func normalizeInput(input CreateInput) CreateInput {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Language = strings.TrimSpace(input.Language)
	input.Transcript = strings.TrimSpace(input.Transcript)
	return input
}

func validateInput(input CreateInput) error {
	if input.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > maxNameRunes {
		return fmt.Errorf("%w: name must be valid UTF-8 and at most %d characters", ErrInvalidInput, maxNameRunes)
	}
	if !utf8.ValidString(input.Description) || utf8.RuneCountInString(input.Description) > maxDescriptionRunes {
		return fmt.Errorf("%w: description must be valid UTF-8 and at most %d characters", ErrInvalidInput, maxDescriptionRunes)
	}
	if !utf8.ValidString(input.Language) || utf8.RuneCountInString(input.Language) > maxLanguageRunes {
		return fmt.Errorf("%w: language must be valid UTF-8 and at most %d characters", ErrInvalidInput, maxLanguageRunes)
	}
	if input.Transcript == "" {
		return fmt.Errorf("%w: transcript is required", ErrInvalidInput)
	}
	if !utf8.ValidString(input.Transcript) || utf8.RuneCountInString(input.Transcript) > maxTranscriptRunes {
		return fmt.Errorf("%w: transcript must be valid UTF-8 and at most %d characters", ErrInvalidInput, maxTranscriptRunes)
	}
	if !input.ConsentConfirmed {
		return ErrConsentRequired
	}
	return nil
}

func validateID(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return fmt.Errorf("%w: invalid id", ErrInvalidInput)
	}
	return nil
}

func validateWAV(file *os.File, size int64) (AudioMetadata, error) {
	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return AudioMetadata{}, ErrUnsupportedWAV
	}
	duration, err := decoder.Duration()
	if err != nil {
		return AudioMetadata{}, fmt.Errorf("%w: cannot read duration: %v", ErrUnsupportedWAV, err)
	}
	if decoder.WavAudioFormat != 1 || decoder.BitDepth != 16 {
		return AudioMetadata{}, fmt.Errorf("%w: expected 16-bit PCM", ErrUnsupportedWAV)
	}
	if decoder.NumChans < 1 || decoder.NumChans > 2 {
		return AudioMetadata{}, fmt.Errorf("%w: expected mono or stereo audio", ErrUnsupportedWAV)
	}
	if decoder.SampleRate < 8000 || decoder.SampleRate > 192000 {
		return AudioMetadata{}, fmt.Errorf("%w: sample rate must be between 8 kHz and 192 kHz", ErrUnsupportedWAV)
	}
	if duration < MinAudioDuration || duration > MaxAudioDuration {
		return AudioMetadata{}, fmt.Errorf("%w: duration must be between %s and %s", ErrInvalidInput, MinAudioDuration, MaxAudioDuration)
	}
	return AudioMetadata{
		DurationMilliseconds: duration.Milliseconds(),
		SampleRate:           decoder.SampleRate,
		Channels:             decoder.NumChans,
		BitDepth:             decoder.BitDepth,
		SizeBytes:            size,
		MIMEType:             "audio/wav",
	}, nil
}

func readProfile(root *os.Root, id string) (Profile, error) {
	raw, err := root.ReadFile(filepath.Join(id, metadataFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("voiceprofile: read profile metadata: %w", err)
	}
	var profile Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return Profile{}, fmt.Errorf("voiceprofile: decode profile metadata: %w", err)
	}
	if profile.ID != id {
		return Profile{}, errors.New("voiceprofile: metadata id does not match directory")
	}
	profile.Voice = Reference(id)
	return profile, nil
}

func writeJSON(root *os.Root, name string, value any) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("voiceprofile: create metadata: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(true)
	encodeErr := encoder.Encode(value)
	if syncErr := file.Sync(); encodeErr == nil && syncErr != nil {
		encodeErr = syncErr
	}
	if closeErr := file.Close(); encodeErr == nil && closeErr != nil {
		encodeErr = closeErr
	}
	if encodeErr != nil {
		return fmt.Errorf("voiceprofile: write metadata: %w", encodeErr)
	}
	return nil
}

func copyWithinRoot(root *os.Root, source, destination string) error {
	src, err := root.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := root.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	if syncErr := dst.Sync(); copyErr == nil && syncErr != nil {
		copyErr = syncErr
	}
	if closeErr := dst.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = root.Remove(destination)
	}
	return copyErr
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			m, writeErr := dst.Write(buf[:n])
			written += int64(m)
			if writeErr != nil {
				return written, writeErr
			}
			if m != n {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func syncDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
