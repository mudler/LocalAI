package downloader

import (
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// MagnetPrefix starts every BitTorrent magnet link.
	MagnetPrefix = "magnet:"
	// PirateFacePrefix is the LocalAI shorthand for a model that Pirate Face
	// (https://pirateface.co) indexes: pirateface://<owner>/<repo>[/<file>].
	PirateFacePrefix = "pirateface://"
	// TorrentFileSuffix ends a .torrent metainfo file name.
	TorrentFileSuffix = ".torrent"
)

// LooksLikeMagnet reports whether the URI is a BitTorrent magnet link.
func (u URI) LooksLikeMagnet() bool {
	return strings.HasPrefix(strings.ToLower(string(u)), MagnetPrefix)
}

// LooksLikePirateFace reports whether the URI uses the pirateface:// shorthand.
func (u URI) LooksLikePirateFace() bool {
	return strings.HasPrefix(string(u), PirateFacePrefix)
}

// LooksLikeTorrentFile reports whether the URI names a .torrent metainfo file:
// an http(s) URL, a file:// URL or a local path whose path ends in ".torrent".
// A "#selector" fragment (see TorrentSelector) is ignored.
func (u URI) LooksLikeTorrentFile() bool {
	s := string(u)
	if u.LooksLikeMagnet() || u.LooksLikePirateFace() {
		return false
	}
	s, _ = splitSelector(s)
	if i := strings.IndexAny(s, "?"); i >= 0 && (strings.HasPrefix(s, HTTPPrefix) || strings.HasPrefix(s, HTTPSPrefix)) {
		s = s[:i]
	}
	if !strings.HasSuffix(strings.ToLower(s), TorrentFileSuffix) {
		return false
	}
	return strings.HasPrefix(s, HTTPPrefix) || strings.HasPrefix(s, HTTPSPrefix) ||
		strings.HasPrefix(s, LocalPrefix) || filepath.IsAbs(s) || !strings.Contains(s, "://")
}

// LooksLikeTorrent reports whether the URI is fetched with BitTorrent: a magnet
// link, a pirateface:// shorthand or a .torrent file.
func (u URI) LooksLikeTorrent() bool {
	return u.LooksLikeMagnet() || u.LooksLikePirateFace() || u.LooksLikeTorrentFile()
}

// splitSelector splits "<source>#<selector>". Magnet links and URLs use the
// fragment, which no server ever sees, to say which file of a multi-file
// torrent a LocalAI file entry wants.
func splitSelector(s string) (source, selector string) {
	if i := strings.LastIndex(s, "#"); i >= 0 {
		sel, err := url.PathUnescape(s[i+1:])
		if err != nil {
			sel = s[i+1:]
		}
		return s[:i], sel
	}
	return s, ""
}

// TorrentSelector returns the "#fragment" of a torrent URI: the path, or a
// unique path suffix, of the file to take out of a multi-file torrent.
func (u URI) TorrentSelector() string {
	_, sel := splitSelector(string(u))
	return sel
}

// Magnet is the part of a magnet link the downloader uses.
type Magnet struct {
	// InfoHash is the hex SHA-1 info hash (BEP 3). It is empty for a link that
	// only has a v2 hash.
	InfoHash    string
	DisplayName string
	Trackers    []string
	WebSeeds    []string
	// PeerAddrs are "host:port" peers named by x.pe (BEP 9).
	PeerAddrs []string
	// SelectOnly holds the file indices of a "so=" parameter (BEP 53).
	SelectOnly []int
}

// ErrInvalidMagnet is returned for a magnet link without a usable info hash.
var ErrInvalidMagnet = errors.New("invalid magnet link")

// ParseMagnet parses a magnet link. A fragment after "#" is ignored.
func ParseMagnet(raw string) (*Magnet, error) {
	raw, _ = splitSelector(raw)
	if !URI(raw).LooksLikeMagnet() {
		return nil, fmt.Errorf("%w: missing magnet: scheme", ErrInvalidMagnet)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMagnet, err)
	}
	q := u.Query()
	m := &Magnet{
		DisplayName: q.Get("dn"),
		Trackers:    nonEmpty(q["tr"]),
		WebSeeds:    nonEmpty(q["ws"]),
		PeerAddrs:   nonEmpty(q["x.pe"]),
	}
	for _, xt := range q["xt"] {
		const prefix = "urn:btih:"
		if !strings.HasPrefix(strings.ToLower(xt), prefix) {
			continue
		}
		h, err := normalizeInfoHash(xt[len(prefix):])
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidMagnet, err)
		}
		m.InfoHash = h
		break
	}
	if m.InfoHash == "" {
		hasV2 := false
		for _, xt := range q["xt"] {
			if strings.HasPrefix(strings.ToLower(xt), "urn:btmh:") {
				hasV2 = true
			}
		}
		if !hasV2 {
			return nil, fmt.Errorf("%w: no xt=urn:btih: info hash", ErrInvalidMagnet)
		}
	}
	for _, so := range q["so"] {
		idx, err := parseSelectOnly(so)
		if err != nil {
			return nil, fmt.Errorf("%w: so=%q: %v", ErrInvalidMagnet, so, err)
		}
		m.SelectOnly = append(m.SelectOnly, idx...)
	}
	return m, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// normalizeInfoHash accepts the 40-character hex and the 32-character base32
// forms of a BitTorrent info hash and returns lowercase hex.
func normalizeInfoHash(s string) (string, error) {
	switch len(s) {
	case 40:
		if _, err := hex.DecodeString(s); err != nil {
			return "", fmt.Errorf("info hash is not hex: %v", err)
		}
		return strings.ToLower(s), nil
	case 32:
		b, err := base32.StdEncoding.DecodeString(strings.ToUpper(s))
		if err != nil || len(b) != 20 {
			return "", errors.New("info hash is not valid base32")
		}
		return hex.EncodeToString(b), nil
	}
	return "", fmt.Errorf("info hash has %d characters, want 40 (hex) or 32 (base32)", len(s))
}

// parseSelectOnly reads a BEP 53 list such as "0,2,4-6".
func parseSelectOnly(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 0 {
			return nil, errors.New("bad file index")
		}
		b := a
		if isRange {
			b, err = strconv.Atoi(hi)
			if err != nil || b < a || b-a > 100000 {
				return nil, errors.New("bad file range")
			}
		}
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
	}
	return out, nil
}

// PirateFaceRef is a parsed pirateface:// URI.
type PirateFaceRef struct {
	Owner string
	Repo  string
	// Path is the file inside the repository; empty names the whole repository.
	Path string
}

// ParsePirateFaceURI parses pirateface://<owner>/<repo>[/<file>]. A fragment is
// ignored.
func ParsePirateFaceURI(raw string) (PirateFaceRef, error) {
	raw, _ = splitSelector(raw)
	rest, ok := strings.CutPrefix(raw, PirateFacePrefix)
	if !ok {
		return PirateFaceRef{}, fmt.Errorf("not a %s URI: %q", PirateFacePrefix, raw)
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return PirateFaceRef{}, fmt.Errorf("invalid %s URI %q: want %s<owner>/<repo>[/<file>]", PirateFacePrefix, raw, PirateFacePrefix)
	}
	ref := PirateFaceRef{Owner: parts[0], Repo: parts[1]}
	if len(parts) == 3 {
		if err := checkRelativePath(parts[2]); err != nil {
			return PirateFaceRef{}, fmt.Errorf("invalid %s URI %q: %w", PirateFacePrefix, raw, err)
		}
		ref.Path = parts[2]
	}
	return ref, nil
}

// RepoID returns "owner/repo".
func (r PirateFaceRef) RepoID() string { return r.Owner + "/" + r.Repo }

// URI returns the pirateface:// form of the reference.
func (r PirateFaceRef) URI() URI {
	s := PirateFacePrefix + r.RepoID()
	if r.Path != "" {
		s += "/" + r.Path
	}
	return URI(s)
}

// PirateFaceRefFromPageURL turns a model page URL such as
// https://pirateface.co/<owner>/<repo> into a reference. ok is false for any
// other URL, including the site's own non-model pages.
func PirateFaceRefFromPageURL(raw string) (ref PirateFaceRef, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return PirateFaceRef{}, false
	}
	endpoint, err := url.Parse(PirateFaceEndpoint)
	if err != nil {
		return PirateFaceRef{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host != strings.ToLower(endpoint.Hostname()) && host != "www."+strings.ToLower(endpoint.Hostname()) {
		return PirateFaceRef{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return PirateFaceRef{}, false
	}
	switch parts[0] {
	case "api", "s", "_next", "account", "licenses":
		return PirateFaceRef{}, false
	}
	return PirateFaceRef{Owner: parts[0], Repo: parts[1]}, true
}

// checkRelativePath rejects a path that is empty, absolute or escapes its
// directory. Names inside a torrent and pirateface:// file paths pass through
// here before anything is written.
func checkRelativePath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if strings.ContainsRune(p, 0) || strings.Contains(p, "\\") {
		return fmt.Errorf("unsafe path %q", p)
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return fmt.Errorf("absolute path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q escapes its directory", p)
		}
	}
	if clean := path.Clean(p); clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("path %q escapes its directory", p)
	}
	return nil
}

// torrentFilename returns the file name a torrent URI maps to when a caller
// has nothing better: the display name of a magnet, the file of a
// pirateface:// URI, the base name of a .torrent source.
func torrentFilename(u URI) string {
	switch {
	case u.LooksLikeMagnet():
		if m, err := ParseMagnet(string(u)); err == nil {
			if _, sel := splitSelector(string(u)); sel != "" {
				return path.Base(sel)
			}
			if m.DisplayName != "" {
				return path.Base(m.DisplayName)
			}
			if m.InfoHash != "" {
				return m.InfoHash
			}
		}
	case u.LooksLikePirateFace():
		if ref, err := ParsePirateFaceURI(string(u)); err == nil {
			if ref.Path != "" {
				return path.Base(ref.Path)
			}
			return ref.Owner + "__" + ref.Repo
		}
	default:
		if _, sel := splitSelector(string(u)); sel != "" {
			return path.Base(sel)
		}
	}
	return ""
}
