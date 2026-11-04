// SPDX-License-Identifier: MIT

// Package policy is the rule set of the Home Assistant add-on: which services
// a model may call, which entities its tools never return or touch, and the
// fingerprints of the self-signed certificates the person pinned.
//
// Two layers decide. The hard exclusions are fixed in this code: no setting
// and no file can lift them. The allowlist and the exclusions are the person's
// own, kept in policy.json in the add-on's data folder, and an administrator
// edits them on the add-on page. A service must pass both: not hard-excluded,
// and on the allowlist. The default allowlist is small on purpose.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
)

// Limits of a policy. A bigger one is refused when it is saved.
const (
	MaxAllow        = 200
	MaxExclusions   = 100
	MaxPatternBytes = 200
	MaxPins         = 20
	MaxTargetsCap   = 20
	DefaultTargets  = 5
	DefaultLabel    = "sensitive"
)

// HardDomains are the service domains that no policy can allow: a service of
// one of them can open a door, disarm an alarm, run a program on the machine,
// restart or reconfigure the system, or reach outside it. The model can read
// what a lock reports when the person lets it; it can never operate one.
var HardDomains = []string{
	"lock", "alarm_control_panel", "siren", "valve", "water_heater", "camera",
	"hassio", "homeassistant", "shell_command", "python_script", "command_line", "rest_command",
	"mqtt", "recorder", "logger", "system_log", "backup", "update",
}

// HardServiceNames are service names that no policy can allow in any domain,
// because they restart, stop or reload the system or change its configuration.
// Any name that starts with "reload" is excluded too (see HardService).
var HardServiceNames = []string{"restart", "stop", "reload", "check_config", "set_location", "install", "update_config", "remove", "create", "delete"}

// SafeCoverClasses are the only device classes of a cover that a policy can
// allow to move. A garage door, a gate, a door, and a cover that reports no
// class at all (it may be any of those) are never moved by this add-on.
var SafeCoverClasses = []string{"awning", "blind", "curtain", "damper", "shade", "shutter", "window"}

// DefaultAllow is the allowlist of a new install. A domain.* entry stands for
// the services of the domain except the ones HardService refuses.
var DefaultAllow = []string{
	"light.turn_on", "light.turn_off", "light.toggle",
	"switch.turn_on", "switch.turn_off", "switch.toggle",
	"climate.set_temperature", "climate.set_hvac_mode",
	"media_player.media_play", "media_player.media_pause", "media_player.media_play_pause", "media_player.media_stop",
	"media_player.media_next_track", "media_player.media_previous_track",
	"media_player.volume_up", "media_player.volume_down", "media_player.volume_set", "media_player.volume_mute",
	"scene.turn_on", "script.turn_on",
	"input_boolean.*",
}

// DefaultExcludeDomains are the entity domains that the read tools do not
// return and no tool touches until the person removes them from the list.
var DefaultExcludeDomains = []string{"lock", "alarm_control_panel", "camera"}

// quiet marks the services that change little and can be undone with the same
// tool at once. Every other allowed service is a physical effect and waits for
// a person's confirmation, whatever the write mode is.
var quietDomains = map[string]bool{"light": true, "switch": true, "input_boolean": true, "media_player": true}

// Physical reports whether a service must wait for a person's confirmation on
// each call. Lights, switches, input booleans and media transport are quiet;
// everything else (climate, cover, scene, script, notify, fan, vacuum ...) is
// a physical effect.
func Physical(domain, service string) bool {
	if domain == "media_player" && (service == "play_media" || service == "select_source" || service == "join" || service == "unjoin") {
		return true
	}
	return !quietDomains[domain]
}

// HardService reports a service that no policy can allow.
func HardService(domain, service string) bool {
	domain, service = strings.ToLower(domain), strings.ToLower(service)
	if slices.Contains(HardDomains, domain) {
		return true
	}
	return service == "" || strings.HasPrefix(service, "reload") || slices.Contains(HardServiceNames, service)
}

// HardCover reports a cover that must not move: its device class is missing or
// not one of SafeCoverClasses.
func HardCover(deviceClass string) bool {
	return !slices.Contains(SafeCoverClasses, strings.ToLower(deviceClass))
}

// Policy is what the person chose.
type Policy struct {
	// Allow lists "domain.service" entries and "domain.*".
	Allow []string `json:"allow"`
	// ExcludeDomains are entity domains the tools never return or touch.
	ExcludeDomains []string `json:"excludeDomains"`
	// ExcludeAreas are area names or IDs whose entities the tools never return or touch.
	ExcludeAreas []string `json:"excludeAreas"`
	// ExcludePatterns are regular expressions (RE2) matched against the whole entity ID.
	ExcludePatterns []string `json:"excludePatterns"`
	// ExcludeLabel is the name of the Home Assistant label that marks an entity
	// as sensitive. Empty turns the label check off.
	ExcludeLabel string `json:"excludeLabel"`
	// MaxTargets is the most entities one service call may name.
	MaxTargets int `json:"maxTargets"`
	// Pins maps host:port to the SHA-256 of the DER certificate (64 hex digits)
	// that the instance must present, for a self-signed certificate.
	Pins map[string]string `json:"pins,omitempty"`
}

// Defaults is the policy of a new install.
func Defaults() Policy {
	return Policy{
		Allow:          slices.Clone(DefaultAllow),
		ExcludeDomains: slices.Clone(DefaultExcludeDomains),
		ExcludeLabel:   DefaultLabel,
		MaxTargets:     DefaultTargets,
	}
}

var (
	domainShape  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	serviceShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	pinShape     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hostPortRe   = regexp.MustCompile(`^[a-z0-9.\-\[\]:]{1,260}$`)
	// EntityID is the shape of an entity ID.
	EntityID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}\.[a-z0-9][a-z0-9_]{0,127}$`)
)

// SafeName reports a name that can sit inside a quoted string of a template
// without escaping: letters, digits, space and a few marks.
func SafeName(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > 64 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '_' || r == '-' || r == '.' || r == '\'') {
			return false
		}
	}
	return strings.TrimSpace(s) == s
}

// ParseAllow splits an allowlist entry. The service is "*" for all the
// services of the domain.
func ParseAllow(e string) (domain, service string, err error) {
	d, s, ok := strings.Cut(e, ".")
	if !ok || !domainShape.MatchString(d) || (s != "*" && !serviceShape.MatchString(s)) {
		return "", "", fmt.Errorf("%q is not domain.service or domain.*", clip(e))
	}
	return d, s, nil
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// Validate checks a policy before it is saved. It refuses, with the entry
// named, an allowlist entry that a hard exclusion forbids: nothing is saved
// and nothing is silently dropped.
func (p Policy) Validate() error {
	if len(p.Allow) > MaxAllow || len(p.ExcludeDomains) > MaxExclusions || len(p.ExcludeAreas) > MaxExclusions || len(p.ExcludePatterns) > MaxExclusions || len(p.Pins) > MaxPins {
		return errors.New("the policy has too many entries")
	}
	for _, e := range p.Allow {
		d, s, err := ParseAllow(e)
		if err != nil {
			return err
		}
		if s == "*" && slices.Contains(HardDomains, d) {
			return fmt.Errorf("%s can never be allowed: %s", e, hardWhy)
		}
		if s != "*" && HardService(d, s) {
			return fmt.Errorf("%s can never be allowed: %s", e, hardWhy)
		}
	}
	for _, d := range p.ExcludeDomains {
		if !domainShape.MatchString(d) {
			return fmt.Errorf("%q is not a domain", clip(d))
		}
	}
	for _, a := range p.ExcludeAreas {
		if !SafeName(a) {
			return fmt.Errorf("%q is not an area name", clip(a))
		}
	}
	for _, pat := range p.ExcludePatterns {
		if len(pat) == 0 || len(pat) > MaxPatternBytes {
			return fmt.Errorf("a pattern has 1 to %d bytes", MaxPatternBytes)
		}
		if _, err := regexp.Compile("^(?:" + pat + ")$"); err != nil {
			return fmt.Errorf("%q is not a valid pattern", clip(pat))
		}
	}
	if p.ExcludeLabel != "" && !SafeName(p.ExcludeLabel) {
		return errors.New("the label is not a plain name")
	}
	if p.MaxTargets < 1 || p.MaxTargets > MaxTargetsCap {
		return fmt.Errorf("maxTargets is 1 to %d", MaxTargetsCap)
	}
	for hp, pin := range p.Pins {
		if !hostPortRe.MatchString(hp) || hp != strings.ToLower(hp) || !strings.Contains(hp, ":") {
			return fmt.Errorf("%q is not host:port", clip(hp))
		}
		if !pinShape.MatchString(pin) {
			return fmt.Errorf("the pin for %s is not 64 hex digits", clip(hp))
		}
	}
	return nil
}

const hardWhy = "locks, alarms, sirens, valves, water heaters, cameras, restarts, reloads and shell, system and add-on management are never operated by this add-on"

// Normalize lowercases and sorts the lists, drops repeats and fills a missing
// limit with the default.
func (p Policy) Normalize() Policy {
	p.Allow = lowerSet(p.Allow)
	p.ExcludeDomains = lowerSet(p.ExcludeDomains)
	p.ExcludeAreas = dedupe(p.ExcludeAreas)
	p.ExcludePatterns = dedupe(p.ExcludePatterns)
	if p.MaxTargets == 0 {
		p.MaxTargets = DefaultTargets
	}
	if len(p.Pins) > 0 {
		n := map[string]string{}
		for k, v := range p.Pins {
			n[strings.ToLower(strings.TrimSpace(k))] = strings.ToLower(strings.TrimSpace(v))
		}
		p.Pins = n
	}
	return p
}

func lowerSet(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Hash is a short digest of the policy, kept in the revision of a proposal so
// a change of the policy after the preview makes the approval stale.
func (p Policy) Hash() string {
	b, _ := json.Marshal(p.Normalize())
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// ServiceAllowed reports whether the policy lets a model call domain.service:
// the service is not hard-excluded and an allowlist entry names it. Runs again
// whatever the file says, so a hand-edited file cannot widen the hard layer.
func (p Policy) ServiceAllowed(domain, service string) bool {
	domain, service = strings.ToLower(domain), strings.ToLower(service)
	if HardService(domain, service) {
		return false
	}
	for _, e := range p.Allow {
		d, s, err := ParseAllow(e)
		if err == nil && d == domain && (s == "*" || s == service) {
			return true
		}
	}
	return false
}

// Compiled is a policy with its patterns ready.
type Compiled struct {
	Policy
	patterns []*regexp.Regexp
}

// Compile prepares the patterns. A pattern that does not compile fails the
// whole policy, so a damaged file never reads as "no exclusions".
func (p Policy) Compile() (*Compiled, error) {
	c := &Compiled{Policy: p}
	for _, pat := range p.ExcludePatterns {
		re, err := regexp.Compile("^(?:" + pat + ")$")
		if err != nil {
			return nil, errors.New("the exclusion patterns of the policy are not valid")
		}
		c.patterns = append(c.patterns, re)
	}
	return c, nil
}

// EntityExcluded reports an entity that the domain or the pattern exclusions
// cover. Areas and labels are asked of Home Assistant by the caller.
func (c *Compiled) EntityExcluded(id string) bool {
	id = strings.ToLower(id)
	domain, _, _ := strings.Cut(id, ".")
	if slices.Contains(c.ExcludeDomains, domain) {
		return true
	}
	for _, re := range c.patterns {
		if re.MatchString(id) {
			return true
		}
	}
	return false
}

// AreaExcluded reports an area name or ID that the policy excludes (any case).
func (p Policy) AreaExcluded(nameOrID string) bool {
	for _, a := range p.ExcludeAreas {
		if strings.EqualFold(a, nameOrID) {
			return true
		}
	}
	return false
}

// Store keeps the policy in policy.json in dir, mode 0600, written atomically.
// The file is read on each call, so a change is seen by the next one.
type Store struct {
	mu   sync.Mutex
	path string
	mem  *Policy
}

// Open returns a store in dir. An empty dir keeps the policy in memory.
func Open(dir string) *Store {
	s := &Store{}
	if dir != "" {
		s.path = filepath.Join(dir, "policy.json")
	}
	return s
}

// Load returns the saved policy, or the defaults when none was saved. A file
// that cannot be read or does not validate is an error: the caller refuses to
// serve rather than fall back to something wider than the person chose.
func (s *Store) Load() (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		if s.mem != nil {
			return *s.mem, nil
		}
		return Defaults().Normalize(), nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults().Normalize(), nil
	}
	if err != nil {
		return Policy{}, errors.New("the policy file cannot be read")
	}
	var p Policy
	if err := json.Unmarshal(b, &p); err != nil {
		return Policy{}, errors.New("the policy file is damaged")
	}
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return Policy{}, errors.New("the policy file does not hold a valid policy")
	}
	return p, nil
}

// Save validates and stores a policy.
func (s *Store) Save(p Policy) (Policy, error) {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		c := p
		s.mem = &c
		return p, nil
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return Policy{}, err
	}
	if err := atomicfile.Write(s.path, b, 0o600); err != nil {
		return Policy{}, errors.New("the policy could not be saved")
	}
	return p, nil
}
