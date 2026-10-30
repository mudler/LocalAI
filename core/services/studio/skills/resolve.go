package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Layer is where a skill comes from. A nearer layer wins over a farther one.
type Layer string

// The four layers, nearest first.
const (
	LayerFolder   Layer = "folder"
	LayerPersonal Layer = "personal"
	LayerShared   Layer = "shared"
	LayerBuiltin  Layer = "builtin"
)

// rank orders the layers; a lower rank is nearer.
func (l Layer) rank() int {
	switch l {
	case LayerFolder:
		return 0
	case LayerPersonal:
		return 1
	case LayerShared:
		return 2
	case LayerBuiltin:
		return 3
	}
	return 4
}

// SkillsDir is the name of the folder that holds the skills of a folder.
const SkillsDir = ".skills"

// SkillFile is the name of the file that makes a folder a skill.
const SkillFile = "SKILL.md"

// Limits of one Resolve call. They are variables so that tests can lower them.
var (
	maxSkillFiles  = 200  // SKILL.md files read
	maxProbes      = 1000 // folders looked into for a SKILL.md, found or not
	maxWalkDepth   = 6    // levels below a walk root
	maxWalkFolders = 2000 // folders listed by the walk of the folder layer
)

// Notes that Resolve returns.
const (
	NoteNotRead     = "some skills were not read"
	NoteNotSearched = "some folders were not searched for skills"
)

// Root names a place to look for skills.
type Root struct {
	Layer Layer
	// Path is the workspace path of the layer folder (built-in, shared and
	// personal: the folder that holds one subfolder per skill), or, for the
	// folder layer, a folder to walk for ".skills" folders.
	Path string
	// Guest is the same place as the guest sees it. The index shows guest
	// paths, so the agent can read a skill with its file tools.
	Guest string
	// Skip lists workspace paths that the folder walk of this root does not
	// enter, for example a folder that holds many chat uploads. It is read
	// only for the folder layer. A ".skills" folder inside a skipped folder is
	// not found.
	Skip []string
}

// Skill is one skill that Resolve saw.
type Skill struct {
	Name        string
	Description string
	Layer       Layer
	Path        string // workspace path of the skill folder
	GuestPath   string // guest path of its SKILL.md
	Hash        string // hex SHA-256 of the SKILL.md content; empty when it was not read
	Shadowed    string // the layer that overrides it, or ""
	Invalid     string // the reason it was skipped, or ""
	// Off is true when Options.Off turned the skill off. An off skill takes
	// no part in the precedence rule and is not in the index.
	Off bool
	// Requires are the capabilities the skill needs (the "requires" key of
	// its frontmatter), for example RequireExec.
	Requires []string
	// Unavailable, set by the product, says why the skill cannot be used in
	// this chat, for example "needs command execution". Index lists such a
	// skill with the reason and without the path of its SKILL.md, so the
	// model explains instead of starting it.
	Unavailable string

	depth int // folders below the walked root, for the order inside the folder layer
}

// Options changes how Resolve works.
type Options struct {
	// Off reports a skill that a chat switched off. It runs before the
	// precedence rule, so turning off a nearer skill uncovers the one below
	// it. It is called for valid skills only, nearest layer first, so for the
	// skills of one name the winner comes first. Nil means all are on.
	Off func(Skill) bool
	// Logger, when set, gets one debug line per call with the number of
	// folders that the folder walks listed. Nil logs nothing.
	Logger *slog.Logger
}

// container is a folder that holds one subfolder per skill.
type container struct {
	layer Layer
	path  string
	guest string
	depth int // folders between the walk root and the container's parent
}

// Resolve reads the skills that the actor can see in roots and applies the
// precedence rule. It returns every skill it saw, valid or not, ordered by
// layer (nearest first), then name, then the deeper folder, then path, plus notes for the turn. It
// returns an error only when the store fails. A folder that the actor cannot
// read is skipped without an error.
//
// It reads only SKILL.md files, at most 200 per call, nearest layers first.
// A name that two skills share belongs to the nearest one; between two
// folder skills the deeper folder wins, and at equal depth the path that
// sorts first. The others get Shadowed set to the layer of the winner.
func Resolve(ctx context.Context, s workspace.Store, a workspace.Actor, roots []Root, o Options) ([]Skill, []string, error) {
	var notes []string
	addNote := func(n string) {
		for _, have := range notes {
			if have == n {
				return
			}
		}
		notes = append(notes, n)
	}

	// The walk must not enter a layer folder that has its own root.
	skip := map[string]bool{}
	for _, r := range roots {
		if r.Layer != LayerFolder {
			skip[cleanPath(r.Path)] = true
		}
	}
	var boxes []container
	walked := 0
	for _, r := range roots {
		if r.Layer == LayerFolder {
			rskip := skip
			if len(r.Skip) > 0 {
				rskip = make(map[string]bool, len(skip)+len(r.Skip))
				for p := range skip {
					rskip[p] = true
				}
				for _, p := range r.Skip {
					rskip[cleanPath(p)] = true
				}
			}
			found, cut, err := walk(ctx, s, a, r, rskip, &walked)
			if err != nil {
				return nil, nil, err
			}
			if cut {
				addNote(NoteNotSearched)
			}
			boxes = append(boxes, found...)
			continue
		}
		boxes = append(boxes, container{layer: r.Layer, path: cleanPath(r.Path), guest: strings.TrimSuffix(r.Guest, "/")})
	}
	if o.Logger != nil {
		o.Logger.Debug("skills: folder walk", "folders", walked, "limit", maxWalkFolders)
	}
	boxes = dedupe(boxes)
	// Nearest first, so that the cap on files never drops a skill that would win.
	sort.SliceStable(boxes, func(i, j int) bool { return less(boxes[i].key(), boxes[j].key()) })

	var out []Skill
	reads, probes := 0, 0
scan:
	for _, b := range boxes {
		kids, err := s.List(ctx, a, b.path)
		if err != nil {
			// ErrInvalid: the layer folder is a file, for example a user wrote
			// a file named ".skills" (see TestAnOddShapeIsSkippedNotFatal).
			if skippable(err) || errors.Is(err, workspace.ErrInvalid) {
				continue
			}
			return nil, nil, fmt.Errorf("skills: list %s: %w", b.path, err)
		}
		for _, k := range kids {
			if k.Kind != workspace.KindDir {
				continue
			}
			if probes >= maxProbes || reads >= maxSkillFiles {
				addNote(NoteNotRead)
				break scan
			}
			probes++
			sk, ok, err := readSkill(ctx, s, a, b, k)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			reads++ // a too-large file counts although it is not read: the cap is conservative
			out = append(out, sk)
		}
	}

	// Off is asked in precedence order, so a caller that turns off the first
	// skill of a name turns off the one that wins.
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	for i := range out {
		if out[i].Invalid == "" && o.Off != nil && o.Off(out[i]) {
			out[i].Off = true
		}
	}
	shadow(out)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out, notes, nil
}

// less is the one order of skills: nearest layer first, then name, then the
// deeper folder (depth counts below the walked root, not in the workspace),
// then path. Within one name it is the precedence rule. Index reuses it.
func less(a, b Skill) bool {
	if ra, rb := a.Layer.rank(), b.Layer.rank(); ra != rb {
		return ra < rb
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.depth != b.depth {
		return a.depth > b.depth
	}
	return a.Path < b.Path
}

// dedupe keeps one container per workspace path. Nested folder roots find the
// same ".skills" folder twice; the copy with the smallest depth below its root
// is kept (its guest path wins), and on a tie the first root. The order of the
// kept containers is the order of first appearance.
func dedupe(boxes []container) []container {
	at := map[string]int{}
	var out []container
	for _, b := range boxes {
		i, seen := at[b.path]
		if !seen {
			at[b.path] = len(out)
			out = append(out, b)
		} else if b.depth < out[i].depth {
			out[i] = b
		}
	}
	return out
}

// key is a stand-in skill with no name, so that less orders containers.
func (b container) key() Skill { return Skill{Layer: b.layer, depth: b.depth, Path: b.path} }

// skippable reports whether a store error means "nothing to see here": the
// path is gone or the actor has no grant for it (the store reports both as
// ErrNotFound). Any other error is returned to the caller.
func skippable(err error) bool {
	return errors.Is(err, workspace.ErrNotFound)
}

func cleanPath(p string) string { return path.Clean("/" + strings.Trim(p, "/")) }

// walk looks for ".skills" folders below r.Path, breadth first. It does not
// enter a ".skills" folder, a layer folder that has its own root, or a folder
// deeper than maxWalkDepth (cut is then set even if that folder is empty). walked counts the folders listed in all walks of
// one Resolve call. cut is true when a limit ended the walk early.
func walk(ctx context.Context, s workspace.Store, a workspace.Actor, r Root, skip map[string]bool, walked *int) (found []container, cut bool, err error) {
	base := cleanPath(r.Path)
	guestBase := strings.TrimSuffix(r.Guest, "/")
	type item struct {
		path  string
		depth int
	}
	queue := []item{{base, 0}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if *walked >= maxWalkFolders {
			return found, true, nil
		}
		*walked++
		kids, err := s.List(ctx, a, it.path)
		if err != nil {
			if skippable(err) {
				continue
			}
			return nil, false, fmt.Errorf("skills: list %s: %w", it.path, err)
		}
		for _, k := range kids {
			if k.Kind != workspace.KindDir {
				continue
			}
			kp := k.Path
			if kp == "" {
				kp = path.Join(it.path, k.Name)
			}
			if skip[kp] {
				continue
			}
			if k.Name == SkillsDir {
				rel := strings.TrimPrefix(kp, strings.TrimSuffix(base, "/"))
				found = append(found, container{layer: LayerFolder, path: kp, guest: guestBase + rel, depth: it.depth})
				continue
			}
			if it.depth+1 > maxWalkDepth {
				cut = true
				continue
			}
			queue = append(queue, item{kp, it.depth + 1})
		}
	}
	return found, cut, nil
}

// readSkill reads <container>/<dir>/SKILL.md. ok is false when the folder has
// no SKILL.md, which is not an error.
func readSkill(ctx context.Context, s workspace.Store, a workspace.Actor, b container, dir workspace.Node) (Skill, bool, error) {
	dp := dir.Path
	if dp == "" {
		dp = path.Join(b.path, dir.Name)
	}
	sk := Skill{
		Name:      dir.Name,
		Layer:     b.layer,
		Path:      dp,
		GuestPath: b.guest + "/" + dir.Name + "/" + SkillFile,
		depth:     b.depth,
	}
	rc, node, err := s.Open(ctx, a, dp+"/"+SkillFile)
	if err != nil {
		// ErrInvalid: SKILL.md is a folder, not a file (see
		// TestAnOddShapeIsSkippedNotFatal). The folder is not a skill.
		if skippable(err) || errors.Is(err, workspace.ErrInvalid) {
			return Skill{}, false, nil
		}
		return Skill{}, false, fmt.Errorf("skills: open %s: %w", dp, err)
	}
	defer rc.Close()
	if node.Size > MaxFileSize {
		sk.Invalid = ErrTooLarge.Error()
		return sk, true, nil
	}
	data, err := io.ReadAll(io.LimitReader(rc, MaxFileSize+1))
	if err != nil {
		return Skill{}, false, fmt.Errorf("skills: read %s: %w", dp, err)
	}
	if len(data) > MaxFileSize { // the file grew after Open
		sk.Invalid = ErrTooLarge.Error()
		return sk, true, nil
	}
	sum := sha256.Sum256(data)
	sk.Hash = hex.EncodeToString(sum[:])
	name, desc, perr := Parse(data)
	if perr != nil {
		sk.Invalid = perr.Error()
		return sk, true, nil
	}
	sk.Name, sk.Description = name, desc
	sk.Requires = Requires(data)
	return sk, true, nil
}

// shadow sets Shadowed on every valid skill that is not off and loses to
// another skill with the same name.
func shadow(skills []Skill) {
	byName := map[string][]int{}
	for i, sk := range skills {
		if sk.Invalid == "" && !sk.Off {
			byName[sk.Name] = append(byName[sk.Name], i)
		}
	}
	for _, idx := range byName {
		if len(idx) < 2 {
			continue
		}
		sort.SliceStable(idx, func(x, y int) bool { return less(skills[idx[x]], skills[idx[y]]) })
		winner := skills[idx[0]].Layer
		for _, i := range idx[1:] {
			skills[i].Shadowed = string(winner)
		}
	}
}
