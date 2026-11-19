package gallery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/atomicfile"
	"github.com/mudler/LocalAI/pkg/vram"
	"gopkg.in/yaml.v3"
)

// SidecarVersion is the schema version of the gallery file
// (._gallery_<name>.yaml) that this release writes. A file without a version
// was written by an older release: it does not say where the model came from,
// so the update check reports its state as unknown. Such a file is read as
// before and is never rewritten at startup; the first install or upgrade by a
// newer release writes version 2.
//
// Distributed mode: the gallery file lives in the models directory, which every
// frontend mounts as one shared directory (the model configs beside it are
// reconciled from that directory, see modeladmin.DirectoryResync). The origin,
// the hold flag and the upgrade journal are therefore shared through the
// filesystem, with no database copy. A later database mirror would read and
// write through ReadSidecar and WriteSidecar, which are the only accessors.
const SidecarVersion = 2

// Roles of a managed file.
const (
	RoleWeights  = "weights"
	RoleMMProj   = "mmproj"
	RoleTemplate = "template"
	RoleArtifact = "artifact"
)

// Origin records the gallery entry a model was installed from, so a later
// check can tell whether the gallery has changed it.
type Origin struct {
	// Gallery is the name of the configured gallery that listed the entry.
	Gallery string `yaml:"gallery,omitempty" json:"gallery,omitempty"`
	// EntryName is the name of the gallery entry (not the installed name).
	EntryName string `yaml:"entry_name,omitempty" json:"entry_name,omitempty"`
	// ConfigURL is the url of the entry's base config, when it has one.
	ConfigURL string `yaml:"config_url,omitempty" json:"config_url,omitempty"`
	// EntryDigest is EntryDigest of the entry when it was installed.
	EntryDigest string `yaml:"entry_digest,omitempty" json:"entry_digest,omitempty"`
	// BaseConfigSHA256 is the hash of the base config bytes fetched from
	// ConfigURL. A url ending in @master gives different bytes over time, so
	// the bytes are what the install saw.
	BaseConfigSHA256 string `yaml:"base_config_sha256,omitempty" json:"base_config_sha256,omitempty"`
	// OverridesSHA256 is the hash of the overrides the install applied: the
	// entry's own merged with the request's.
	OverridesSHA256 string `yaml:"overrides_sha256,omitempty" json:"overrides_sha256,omitempty"`
	// RequestOverrides are the overrides of the install request (for example
	// the context size picked at install time), so an upgrade applies them
	// again.
	RequestOverrides map[string]any `yaml:"request_overrides,omitempty" json:"request_overrides,omitempty"`
	// ConfigYAML is the model config the install wrote. The update check
	// compares it with the config on disk to find the keys a person changed.
	ConfigYAML string `yaml:"config_yaml,omitempty" json:"-"`
	// InstalledAt is when the install or upgrade wrote this origin.
	InstalledAt time.Time `yaml:"installed_at,omitempty" json:"installed_at,omitzero"`
}

// ManagedFile is one file that the install owns.
type ManagedFile struct {
	Path   string `yaml:"path" json:"path"`
	SHA256 string `yaml:"sha256,omitempty" json:"sha256,omitempty"`
	URI    string `yaml:"uri,omitempty" json:"uri,omitempty"`
	Role   string `yaml:"role" json:"role"`
}

// Journal phases. Terminal phases are Confirmed, RolledBack and Failed.
const (
	PhaseStaging     = "staging"
	PhasePreflight   = "preflight"
	PhaseCommitting  = "committing"
	PhaseUnloading   = "unloading"
	PhaseLoading     = "loading"
	PhaseConfirmed   = "confirmed"
	PhaseRollingBack = "rolling-back"
	PhaseRolledBack  = "rolled-back"
	PhaseFailed      = "failed"
)

// Journal kinds.
const (
	// JournalInstall guards the two writes of an install: the gallery file and
	// the model config. It exists only between them.
	JournalInstall = "install"
	// JournalUpgrade is the durable state of an upgrade (modelupdates).
	JournalUpgrade = "upgrade"
)

// StagedFile is a file an upgrade replaces or retires. Next is the staged new
// copy, Prev the name the old copy is kept under after the commit.
type StagedFile struct {
	Path   string `yaml:"path" json:"path"`
	Next   string `yaml:"next,omitempty" json:"next,omitempty"`
	Prev   string `yaml:"prev,omitempty" json:"prev,omitempty"`
	SHA256 string `yaml:"sha256,omitempty" json:"sha256,omitempty"`
	URI    string `yaml:"uri,omitempty" json:"uri,omitempty"`
	Role   string `yaml:"role,omitempty" json:"role,omitempty"`
	// Content is set for a prompt template, which is written, not downloaded.
	Content string `yaml:"content,omitempty" json:"-"`
	// Retire is set for an old file the new version no longer lists.
	Retire bool `yaml:"retire,omitempty" json:"retire,omitempty"`
}

// Previous is what a rollback restores: the origin, the managed files and the
// config hash of the version before the change.
type Previous struct {
	Origin       *Origin       `yaml:"origin,omitempty" json:"origin,omitempty"`
	ConfigSHA256 string        `yaml:"config_sha256,omitempty" json:"config_sha256,omitempty"`
	Managed      []ManagedFile `yaml:"managed,omitempty" json:"managed,omitempty"`
	// ConfigYAML is the model config before the change, base64 encoded so the
	// bytes come back exactly. Empty with HadConfig false: there was none.
	ConfigYAML string `yaml:"config_yaml,omitempty" json:"-"`
	HadConfig  bool   `yaml:"had_config,omitempty" json:"-"`
	// Sidecar is the whole gallery file before an install, base64 encoded,
	// for an install journal. Empty with HadSidecar false: there was none.
	Sidecar    string `yaml:"sidecar,omitempty" json:"-"`
	HadSidecar bool   `yaml:"had_sidecar,omitempty" json:"-"`
}

// Journal is the durable state of a change to an installed model. It lets a
// restart finish or undo a change that a crash interrupted.
type Journal struct {
	Kind  string `yaml:"kind" json:"kind"`
	Phase string `yaml:"phase" json:"phase"`
	// OpID is the operation that runs the change.
	OpID string `yaml:"op_id,omitempty" json:"op_id,omitempty"`
	// Instance is the frontend that runs the change. Another frontend acts on
	// the journal only when it is stale (see Stale).
	Instance  string    `yaml:"instance,omitempty" json:"instance,omitempty"`
	StartedAt time.Time `yaml:"started_at,omitempty" json:"started_at,omitzero"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty" json:"updated_at,omitzero"`

	FromEntryDigest string `yaml:"from_entry_digest,omitempty" json:"from_entry_digest,omitempty"`
	ToEntryDigest   string `yaml:"to_entry_digest,omitempty" json:"to_entry_digest,omitempty"`
	// ToConfigSHA256 is the hash of the model config the change writes. After
	// a crash, a config with this hash means the commit wrote it.
	ToConfigSHA256 string `yaml:"to_config_sha256,omitempty" json:"to_config_sha256,omitempty"`
	ConfigOnly     bool   `yaml:"config_only,omitempty" json:"config_only,omitempty"`
	// Choices are the keep-mine (mine) or take-gallery (gallery) choices for
	// the keys a person edited, by dotted key.
	Choices map[string]string `yaml:"choices,omitempty" json:"choices,omitempty"`

	Files []StagedFile `yaml:"files,omitempty" json:"files,omitempty"`
	// To is the origin, config and managed files the change installs.
	To       *Previous `yaml:"to,omitempty" json:"-"`
	Previous *Previous `yaml:"previous,omitempty" json:"-"`
	// WasLoaded is true when the model was loaded before the change, so a
	// rollback loads the old version again.
	WasLoaded bool `yaml:"was_loaded,omitempty" json:"was_loaded,omitempty"`
	// RetainedFiles are the old files kept after a confirmed upgrade, until a
	// person removes them.
	RetainedFiles []string `yaml:"retained_files,omitempty" json:"retained_files,omitempty"`
	Error         string   `yaml:"error,omitempty" json:"error,omitempty"`
	Attempts      int      `yaml:"attempts,omitempty" json:"attempts,omitempty"`
	// Note is what a person should know about how the change ran, for
	// example that the strategy asked for is not available yet.
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
}

// Terminal reports whether the journal is in a phase no process acts on.
func (j *Journal) Terminal() bool {
	if j == nil {
		return true
	}
	switch j.Phase {
	case PhaseConfirmed, PhaseRolledBack, PhaseFailed:
		return true
	}
	return false
}

// Stale reports whether a running journal was last touched more than after
// ago, so its frontend is presumed dead and another may finish or undo it.
func (j *Journal) Stale(now time.Time, after time.Duration) bool {
	if j == nil || j.Terminal() {
		return false
	}
	return now.Sub(j.UpdatedAt) > after
}

// SHA256Hex is the hex SHA-256 of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// IsV2 reports whether the gallery file has the version 2 schema.
func (c *ModelConfig) IsV2() bool { return c != nil && c.Version >= SidecarVersion }

// SidecarPath is the path of the gallery file of an installed model.
func SidecarPath(modelsPath, name string) string {
	return filepath.Join(modelsPath, galleryFileName(strings.ReplaceAll(name, string(os.PathSeparator), "__")))
}

// SidecarNames lists, sorted, the models that have a gallery file in
// modelsPath. It fails when the directory cannot be read.
func SidecarNames(modelsPath string) ([]string, error) {
	entries, err := os.ReadDir(modelsPath)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "._gallery_") || !strings.HasSuffix(n, ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(n, "._gallery_"), ".yaml"))
	}
	sort.Strings(names)
	return names, nil
}

// ErrNoSidecar is returned by ReadSidecar when the model has no gallery file.
var ErrNoSidecar = errors.New("the model has no gallery file")

// ReadSidecar reads the gallery file of an installed model. It returns
// ErrNoSidecar when there is none.
func ReadSidecar(modelsPath, name string) (*ModelConfig, error) {
	data, err := os.ReadFile(SidecarPath(modelsPath, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSidecar
	}
	if err != nil {
		return nil, fmt.Errorf("read gallery file: %w", err)
	}
	var c ModelConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse gallery file: %w", err)
	}
	return &c, nil
}

// WriteSidecar writes the gallery file of an installed model atomically (a
// temporary file and a rename), so a crash leaves the old file or the new
// one, never a truncated one. It does not change the version: the caller
// decides whether it writes a version 2 file.
func WriteSidecar(modelsPath, name string, c *ModelConfig) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal gallery file: %w", err)
	}
	return atomicfile.Write(SidecarPath(modelsPath, name), data, 0644)
}

// ManagedFileSHA256 returns the sha256 that the gallery files of the installed
// models record for the file at path (inside modelsPath), or "" when no
// version 2 gallery file manages it. A cluster frontend sends it with each
// staged file, so a worker can tell an old cached copy from the file the
// model has now.
//
// The caller knows the file, not the model it belongs to, so every gallery
// file is a candidate. Each is parsed once: the parsed list of managed files
// is kept with the size and modification time of the gallery file it came
// from, and read again only when those change.
func ManagedFileSHA256(modelsPath, path string) string {
	rel, err := filepath.Rel(modelsPath, path)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	rel = filepath.ToSlash(rel)
	names, err := SidecarNames(modelsPath)
	if err != nil {
		return ""
	}
	for _, name := range names {
		if sum := managedSHA256Of(modelsPath, name)[rel]; sum != "" {
			return sum
		}
	}
	return ""
}

type managedSHA256Entry struct {
	size    int64
	modTime time.Time
	sums    map[string]string // slash path -> lowercase sha256
}

var (
	managedSHA256Mu    sync.Mutex
	managedSHA256Cache = map[string]managedSHA256Entry{} // gallery file path -> entry
)

// managedSHA256Of is the checksums the version 2 gallery file of a model
// records, by slash path. It is nil for a gallery file that is missing,
// unreadable or not version 2.
func managedSHA256Of(modelsPath, name string) map[string]string {
	file := SidecarPath(modelsPath, name)
	info, err := os.Stat(file)
	if err != nil {
		return nil
	}
	managedSHA256Mu.Lock()
	cached, ok := managedSHA256Cache[file]
	managedSHA256Mu.Unlock()
	if ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.sums
	}
	var sums map[string]string
	if sc, err := ReadSidecar(modelsPath, name); err == nil && sc.IsV2() {
		sums = map[string]string{}
		for _, m := range sc.Managed {
			if m.SHA256 != "" {
				sums[filepath.ToSlash(m.Path)] = strings.ToLower(m.SHA256)
			}
		}
	}
	managedSHA256Mu.Lock()
	managedSHA256Cache[file] = managedSHA256Entry{size: info.Size(), modTime: info.ModTime(), sums: sums}
	managedSHA256Mu.Unlock()
	return sums
}

// FileRole classifies a file the install owns by its name.
func FileRole(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".tmpl"):
		return RoleTemplate
	case strings.Contains(lower, "mmproj"):
		return RoleMMProj
	case vram.IsWeightFile(filename) || strings.HasSuffix(lower, ".bin") || strings.HasSuffix(lower, ".pt") || strings.HasSuffix(lower, ".onnx"):
		return RoleWeights
	}
	return RoleArtifact
}

// ManagedFileOf is the record of a download an install owns.
func ManagedFileOf(f File) ManagedFile {
	return ManagedFile{Path: f.Filename, SHA256: f.SHA256, URI: f.URI, Role: FileRole(f.Filename)}
}

// ManagedTemplateOf is the record of a prompt template an install owns,
// written next to the model as <name>.tmpl.
func ManagedTemplateOf(t PromptTemplate) ManagedFile {
	return ManagedFile{Path: t.Name + ".tmpl", SHA256: SHA256Hex([]byte(t.Content)), Role: RoleTemplate}
}

// ManagedFiles lists the files a gallery config makes an install own: its
// downloads and its prompt templates.
func ManagedFiles(cfg *ModelConfig) []ManagedFile {
	out := make([]ManagedFile, 0, len(cfg.Files)+len(cfg.PromptTemplates))
	for _, f := range cfg.Files {
		out = append(out, ManagedFileOf(f))
	}
	for _, t := range cfg.PromptTemplates {
		out = append(out, ManagedTemplateOf(t))
	}
	return out
}

// canonicalSHA256 hashes the canonical JSON of v (map keys sorted, which
// encoding/json does). It returns "" for a nil or empty value, so "no
// overrides" has one form. A value that cannot be marshalled is an error: a
// digest of nothing would read as "no overrides" and hide a change.
func canonicalSHA256(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	if m, ok := v.(map[string]any); ok && len(m) == 0 {
		return "", nil
	}
	data, err := json.Marshal(normalizeJSON(v))
	if err != nil {
		return "", fmt.Errorf("hash the overrides: %w", err)
	}
	return SHA256Hex(data), nil
}

// normalizeJSON turns the map[any]any a YAML v2 decoder can produce into
// map[string]any, which encoding/json can marshal.
func normalizeJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeJSON(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalizeJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeJSON(val)
		}
		return out
	}
	return v
}
