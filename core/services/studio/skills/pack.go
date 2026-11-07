package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Import of skills in the public Agent Skills format
// (https://agentskills.io/specification): a folder with a SKILL.md and
// optional scripts/, references/ and assets/ folders, sent as a zip archive.
//
// ReadPacks only reads and checks. It never writes a file and never runs
// anything: a script in an archive is data, kept as bytes. The archive is
// untrusted, so every limit is counted on the bytes that are really read, not
// on the sizes the archive declares.

// Errors of ReadPacks. The text after the sentinel is written for the person.
var (
	// ErrPackBad means the archive is not a valid skill archive.
	ErrPackBad = errors.New("not a valid skill archive")
	// ErrPackLimit means the archive is over one of the size limits.
	ErrPackLimit = errors.New("skill archive too big")
)

// PackLimits bound one archive. A zero field uses the default.
type PackLimits struct {
	// MaxEntries is the most entries (files and folders) of the archive.
	MaxEntries int
	// MaxFileBytes is the most bytes of one file after it is unpacked.
	MaxFileBytes int64
	// MaxTotalBytes is the most bytes of all files after they are unpacked.
	MaxTotalBytes int64
}

// Default limits of an archive of skills.
const (
	DefaultPackEntries   = 500
	DefaultPackFileBytes = 5 << 20
	DefaultPackBytes     = 20 << 20

	packMaxDepth = 8
	packMaxPath  = 512
	// packMaxSkills is the most skills in one archive.
	packMaxSkills = 50
)

func (l PackLimits) withDefaults() PackLimits {
	if l.MaxEntries <= 0 {
		l.MaxEntries = DefaultPackEntries
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultPackFileBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultPackBytes
	}
	return l
}

// PackFile is one file of a skill.
type PackFile struct {
	// Path is relative to the skill folder, with forward slashes. The file
	// SKILL.md is at "SKILL.md".
	Path string
	Data []byte
}

// Pack is one skill read from an archive and found valid.
type Pack struct {
	Name        string
	Description string
	// Files holds every file of the skill, SKILL.md included, sorted by path.
	Files []PackFile
}

// Bytes returns the size of all files of the pack.
func (p Pack) Bytes() int64 {
	var n int64
	for _, f := range p.Files {
		n += int64(len(f.Data))
	}
	return n
}

// agentName is the rule of the specification for the name field: lowercase
// letters and digits in words joined by single hyphens, at most 64 characters.
var agentName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Limits of the optional fields, from the specification.
const (
	maxCompatibility = 500
	maxNameLen       = 64
)

// CheckSkillFile checks the SKILL.md text against the Agent Skills
// specification and returns the name and the cleaned description. It is
// stricter than Parse: the name has no leading, trailing or double hyphen, the
// frontmatter is real YAML, license and compatibility are text of the
// documented size, metadata maps text to text and allowed-tools is text.
// Fields the specification does not name are ignored. The text must also be
// read back the same by Parse, which is what the chats use.
func CheckSkillFile(data []byte) (name, description string, err error) {
	if len(data) > MaxFileSize {
		return "", "", fmt.Errorf("SKILL.md is larger than %d KiB", MaxFileSize>>10)
	}
	if !utf8.Valid(data) {
		return "", "", errors.New("SKILL.md is not valid UTF-8 text")
	}
	lines, ok := frontmatter(string(data))
	if !ok {
		return "", "", errors.New("SKILL.md must start with a frontmatter block between two --- lines")
	}
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &fm); err != nil {
		return "", "", errors.New("the frontmatter of SKILL.md is not valid YAML")
	}
	name, err = textField(fm, "name", true)
	if err != nil {
		return "", "", err
	}
	if len(name) > maxNameLen || !agentName.MatchString(name) {
		return "", "", fmt.Errorf("name %q is not valid: use 1 to 64 lowercase letters, digits and single hyphens, not starting or ending with a hyphen", truncate(name))
	}
	description, err = textField(fm, "description", true)
	if err != nil {
		return "", "", err
	}
	description = Clean(description)
	switch {
	case description == "":
		return "", "", errors.New("description must not be empty")
	case utf8.RuneCountInString(description) > MaxDescription:
		return "", "", fmt.Errorf("description is longer than %d characters", MaxDescription)
	}
	if _, err := textField(fm, "license", false); err != nil {
		return "", "", err
	}
	compat, err := textField(fm, "compatibility", false)
	if err != nil {
		return "", "", err
	}
	if _, present := fm["compatibility"]; present && (compat == "" || utf8.RuneCountInString(compat) > maxCompatibility) {
		return "", "", fmt.Errorf("compatibility must have 1 to %d characters", maxCompatibility)
	}
	if _, err := textField(fm, "allowed-tools", false); err != nil {
		return "", "", err
	}
	if m, present := fm["metadata"]; present && m != nil {
		mm, isMap := m.(map[string]any)
		if !isMap {
			return "", "", errors.New("metadata must map names to text")
		}
		for k, v := range mm {
			if _, isText := v.(string); !isText {
				return "", "", fmt.Errorf("metadata value %q must be text (quote numbers and booleans)", truncate(k))
			}
		}
	}
	// The chats read the file with Parse: both must agree.
	pn, pd, perr := Parse(data)
	if perr != nil || pn != name || pd != description {
		return "", "", errors.New("the description cannot be read by LocalAI as written: put it on one line, or use a > or | block")
	}
	return name, description, nil
}

// textField reads a frontmatter field that must be text.
func textField(fm map[string]any, key string, required bool) (string, error) {
	v, present := fm[key]
	if !present || v == nil {
		if required {
			return "", fmt.Errorf("SKILL.md frontmatter needs a %s", key)
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be text (quote it)", key)
	}
	return strings.TrimSpace(s), nil
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= 40 {
		return s
	}
	return string([]rune(s)[:40]) + "..."
}

// ReadPacks reads a zip archive of one or several skills and checks all of it.
// Nothing is returned when any part is not valid.
//
// Two layouts are read. SKILL.md at the top of the archive is one skill, named
// by its frontmatter. Otherwise every top-level folder is a skill: it holds a
// SKILL.md and its name must equal the folder name, as the specification
// requires. A __MACOSX folder and .DS_Store files are ignored.
//
// An entry is refused (the whole archive is) when its path is absolute, has a
// ".." or empty part, a backslash, a drive letter or a control character, is
// too deep or long, repeats another path (also ignoring case and Unicode
// form), or is a symbolic link, a device, or encrypted.
func ReadPacks(r io.ReaderAt, size int64, lim PackLimits) ([]Pack, error) {
	lim = lim.withDefaults()
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: it is not a readable zip file", ErrPackBad)
	}
	if len(zr.File) > lim.MaxEntries {
		return nil, fmt.Errorf("%w: it has more than %d entries", ErrPackLimit, lim.MaxEntries)
	}

	type entry struct {
		path string // cleaned, slash separated
		file *zip.File
	}
	var entries []entry
	seen := map[string]string{} // folded path to the path as written
	dirs := map[string]bool{}   // folded folder paths, implied or listed
	for _, f := range zr.File {
		p, skip, err := packPath(f.Name)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPackBad, err)
		}
		if skip {
			continue
		}
		mode := f.Mode()
		switch {
		case mode&modeLinkOrSpecial != 0:
			return nil, fmt.Errorf("%w: %q is a link or special file, which a skill may not hold", ErrPackBad, p)
		case f.Flags&0x1 != 0:
			return nil, fmt.Errorf("%w: %q is encrypted", ErrPackBad, p)
		}
		fold := workspace.FoldName(p)
		if f.FileInfo().IsDir() {
			dirs[fold] = true
			continue
		}
		if prev, dup := seen[fold]; dup {
			return nil, fmt.Errorf("%w: %q and %q have the same name", ErrPackBad, prev, p)
		}
		if dirs[fold] {
			return nil, fmt.Errorf("%w: %q is both a file and a folder", ErrPackBad, p)
		}
		seen[fold] = p
		if f.UncompressedSize64 > uint64(lim.MaxFileBytes) {
			return nil, fmt.Errorf("%w: %q is larger than %d MiB", ErrPackLimit, p, lim.MaxFileBytes>>20)
		}
		entries = append(entries, entry{path: p, file: f})
	}
	// A file must not sit where another entry needs a folder.
	for fold, p := range seen {
		for parent := parentOf(fold); parent != ""; parent = parentOf(parent) {
			if q, clash := seen[parent]; clash {
				return nil, fmt.Errorf("%w: %q is a file, and %q is below it", ErrPackBad, q, p)
			}
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: it holds no files", ErrPackBad)
	}

	// Read every file with a hard cap on the bytes really read.
	var total int64
	data := make(map[string][]byte, len(entries))
	for _, e := range entries {
		rc, err := e.file.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: %q could not be read", ErrPackBad, e.path)
		}
		b, err := io.ReadAll(io.LimitReader(rc, lim.MaxFileBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: %q could not be unpacked", ErrPackBad, e.path)
		}
		if int64(len(b)) > lim.MaxFileBytes {
			return nil, fmt.Errorf("%w: %q is larger than %d MiB", ErrPackLimit, e.path, lim.MaxFileBytes>>20)
		}
		total += int64(len(b))
		if total > lim.MaxTotalBytes {
			return nil, fmt.Errorf("%w: it unpacks to more than %d MiB", ErrPackLimit, lim.MaxTotalBytes>>20)
		}
		data[e.path] = b
	}

	// Group the files by skill.
	groups := map[string][]PackFile{} // skill folder ("" for the top level) to files
	_, rootSkill := data[SkillFile]
	for p, b := range data {
		if rootSkill {
			groups[""] = append(groups[""], PackFile{Path: p, Data: b})
			continue
		}
		top, rest, nested := strings.Cut(p, "/")
		if !nested {
			return nil, fmt.Errorf("%w: %q is at the top of the archive; put each skill in a folder, or put SKILL.md at the top for one skill", ErrPackBad, p)
		}
		groups[top] = append(groups[top], PackFile{Path: rest, Data: b})
	}
	if len(groups) > packMaxSkills {
		return nil, fmt.Errorf("%w: it holds more than %d skills", ErrPackLimit, packMaxSkills)
	}

	folders := make([]string, 0, len(groups))
	for k := range groups {
		folders = append(folders, k)
	}
	sort.Strings(folders)
	var packs []Pack
	names := map[string]bool{}
	for _, folder := range folders {
		files := groups[folder]
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		var text []byte
		for _, f := range files {
			if f.Path == SkillFile {
				text = f.Data
			}
		}
		label := folder
		if label == "" {
			label = SkillFile
		}
		if text == nil {
			return nil, fmt.Errorf("%w: the folder %q has no SKILL.md", ErrPackBad, folder)
		}
		name, desc, err := CheckSkillFile(text)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrPackBad, label, err)
		}
		if folder != "" && folder != name {
			return nil, fmt.Errorf("%w: the folder %q holds a skill named %q; the specification requires the same name", ErrPackBad, folder, name)
		}
		if names[name] {
			return nil, fmt.Errorf("%w: the archive holds the skill %q twice", ErrPackBad, name)
		}
		names[name] = true
		packs = append(packs, Pack{Name: name, Description: desc, Files: files})
	}
	return packs, nil
}

// modeLinkOrSpecial are the mode bits of everything but a plain file or folder.
const modeLinkOrSpecial = fs.ModeSymlink | fs.ModeDevice | fs.ModeCharDevice | fs.ModeNamedPipe | fs.ModeSocket | fs.ModeIrregular

func parentOf(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}

// packPath cleans the name of an entry. skip is true for the metadata that
// archivers add (a __MACOSX folder, .DS_Store). A name that could leave the
// skill folder is an error.
func packPath(name string) (p string, skip bool, err error) {
	if !utf8.ValidString(name) {
		return "", false, errors.New("an entry has a name that is not valid UTF-8")
	}
	if name == "" || len(name) > packMaxPath {
		return "", false, errors.New("an entry has an empty or too long name")
	}
	if strings.ContainsRune(name, '\\') {
		return "", false, fmt.Errorf("%q has a backslash in its name", truncate(name))
	}
	if strings.HasPrefix(name, "/") {
		return "", false, fmt.Errorf("%q is an absolute path", truncate(name))
	}
	trimmed := strings.TrimSuffix(name, "/")
	segs := strings.Split(trimmed, "/")
	if len(segs) > packMaxDepth {
		return "", false, fmt.Errorf("%q is nested deeper than %d folders", truncate(name), packMaxDepth)
	}
	for _, s := range segs {
		clean, cerr := workspace.CleanName(s)
		switch {
		case s == "" || s == "." || s == "..":
			return "", false, fmt.Errorf("%q has an empty, . or .. part", truncate(name))
		case cerr != nil:
			return "", false, fmt.Errorf("%q has a name that is not allowed", truncate(name))
		case clean != s:
			return "", false, fmt.Errorf("%q is not in normalized Unicode form", truncate(name))
		case strings.ContainsFunc(s, unicode.IsControl) || strings.ContainsRune(s, ':'):
			return "", false, fmt.Errorf("%q has a character that is not allowed", truncate(name))
		}
	}
	if segs[0] == "__MACOSX" || segs[len(segs)-1] == ".DS_Store" {
		return "", true, nil
	}
	return strings.Join(segs, "/"), false, nil
}

// NewMarker is the name of an empty file that an import puts in the folder of
// a skill. It marks the skill as imported and not yet used by a chat: the host
// removes it when a chat loads the skill for the first time. It is not a part
// of the skill and an export leaves it out.
const NewMarker = ".localai-new"

// WriteZip writes a pack as a zip archive in the Agent Skills layout: one
// folder named after the skill, with SKILL.md and the other files below it.
// ReadPacks reads the result back to the same pack. Entry times are fixed, so
// the same pack gives the same bytes.
func WriteZip(p Pack) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range p.Files {
		if f.Path == NewMarker {
			continue
		}
		h := &zip.FileHeader{Name: p.Name + "/" + f.Path, Method: zip.Deflate}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
