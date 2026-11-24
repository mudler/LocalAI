package projects

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A project is shared with a person when the person has a live workspace grant
// that covers the project folder /users/<owner>/Projects/<slug>: a grant on
// the folder, on Projects/, or on the home of the owner, to the person or to
// everyone. Nothing else shares a project (the manifest field sharedWith
// grants nothing). The grants are asked from the workspace store on every call
// and nothing is kept, so a revoke or a lowering holds from the next call.
//
// The viewer cannot read the index of the owner, so a shared project is
// identified by the workspace node of its folder (see Shared.Ref), never by
// the id in its manifest: that id is the owner's text and may be copied. The
// manifest is read through the viewer's own grant, and every path comes from
// nodes the store returned for the viewer.

const (
	// sharedMax bounds the project folders one call looks at.
	sharedMax = 500
	// fallbackOwnerName is shown when the owner has no display name.
	fallbackOwnerName = "Another user"
	usersRoot         = "/users"
)

// Mode names of a shared project.
const (
	ModeRead  = "read"
	ModeWrite = "write"
)

// Shared is a project of another user that is shared with the viewer.
//
// The embedded Project has no Owner: the account of the owner is not for the
// viewer. FilesPath and MemoryPath are workspace paths and name the owner's
// account, so they are for server code that opens the folders with the
// viewer's actor. They must not be sent to a client.
type Shared struct {
	Project
	// OwnerName is a display name, never an account ID.
	OwnerName string
	// Description is the text of the manifest, empty for a broken project.
	Description string
	// OwnerPrincipal is the account of the owner. It is for server code that
	// must act for the owner (a change the owner confirms). It is never
	// serialised and never shown to the viewer.
	OwnerPrincipal string `json:"-"`
	// Mode is the effective mode of the viewer on the project folder: "read"
	// or "write".
	Mode string
	// FilesPath and MemoryPath are workspace-absolute.
	//
	// The two paths are not serialised: an accidental encoding must not
	// show /users/<owner>/.
	FilesPath  string `json:"-"`
	MemoryPath string `json:"-"`
	// Ref is "s:<node id>", the only reference to keep for a shared project.
	// The node id of the project folder is the identity of a shared project:
	// it is the store's, it survives a rename and a move, and nobody can
	// choose it. The manifest id is the owner's text and anyone may copy it,
	// so it must never be kept or used to find a shared project.
	Ref string
}

// SharedWith returns the projects of other users that are shared with viewer,
// sorted by name. A folder directly under a Projects/ folder with a
// project.json file is listed: an unreadable, invalid or too large manifest as
// StatusBroken with the reason and an empty ID. The ID is the owner's text
// and may be copied: find a shared project by Ref, never by ID. At most 500
// folders are looked at; a longer list is cut and a warning is logged. A folder
// without a project.json is not a project. The projects of the viewer are
// never listed. Only manifests of folders the viewer may read are read.
func (s *Service) SharedWith(ctx context.Context, viewer string) ([]Shared, error) {
	if err := s.permit(ctx, viewer); err != nil {
		return nil, err
	}
	if !viewerOK(viewer) {
		return nil, nil
	}
	st := s.o.Files.Store()
	a := s.o.Files.Actor(viewer)
	prefixes, err := st.ReadablePrefixes(ctx, a)
	if err != nil {
		return nil, err
	}
	var cands []workspace.Node
	seen := map[int64]bool{}
	truncated := false
	add := func(n workspace.Node) {
		if n.Kind != workspace.KindDir || seen[n.ID] {
			return
		}
		if len(cands) >= sharedMax {
			truncated = true
			return
		}
		seen[n.ID] = true
		cands = append(cands, n)
	}
	for _, p := range prefixes {
		if err := s.candidates(ctx, a, viewer, p, add); err != nil {
			return nil, err
		}
	}
	if truncated {
		xlog.Warn("projects: the list of shared projects was cut", "limit", sharedMax)
	}
	names := s.ownerNames(ctx)
	out := []Shared{}
	for _, n := range cands {
		sh, ok, err := s.describeShared(ctx, a, viewer, n, names)
		if errors.Is(err, errManifestIO) {
			continue // not listed: its state is unknown
		}
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, sh)
		}
	}
	slices.SortStableFunc(out, func(x, y Shared) int {
		if c := strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)); c != 0 {
			return c
		}
		if c := strings.Compare(x.OwnerName, y.OwnerName); c != 0 {
			return c
		}
		return cmp.Compare(x.Node, y.Node)
	})
	return out, nil
}

// errManifestIO means the manifest of a shared project could not be read for
// a reason that is not its content. It has no detail on purpose.
var errManifestIO = errors.New("projects: the shared project could not be read")

// refPrefix starts the reference of a shared project.
const refPrefix = "s:"

// maxRefDigits bounds the digits of a reference: a node id is an int64.
const maxRefDigits = 18

// IsSharedRef says whether id has the form of the reference of a shared
// project. It does not say the reference is valid or shared with anyone.
func IsSharedRef(id string) bool { return strings.HasPrefix(id, refPrefix) }

// SharedRef returns the reference of the shared project folder node.
func SharedRef(node int64) string { return refPrefix + strconv.FormatInt(node, 10) }

// ResolveSharedRef resolves a reference made by SharedRef. A reference that is
// not "s:" and 1 to 18 digits with a value above zero is ErrNotFound, the same
// error as for a project that is not shared.
func (s *Service) ResolveSharedRef(ctx context.Context, viewer, ref string) (Shared, error) {
	digits, ok := strings.CutPrefix(ref, refPrefix)
	// A reference is canonical: no leading zero, so one node has one text.
	if !ok || digits == "" || len(digits) > maxRefDigits || digits[0] == '0' {
		return Shared{}, ErrNotFound
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return Shared{}, ErrNotFound
		}
	}
	node, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || node <= 0 {
		return Shared{}, ErrNotFound
	}
	return s.ResolveSharedNode(ctx, viewer, node)
}

// ResolveSharedNode returns the project whose folder is the workspace node
// node, as the viewer sees it now. It looks the node up as the viewer and
// applies every check of SharedWith to that one node, reading one manifest:
// the work does not grow with the number of projects shared with the viewer.
//
// It is ErrNotFound, with one text, for a node that is not shared with the
// viewer, does not exist, is deleted, is no longer directly under a Projects/
// folder of another user, is a project of the viewer, or is not a project
// folder (no project.json). A project whose manifest is broken is ErrInvalid.
// Because the node id is the identity, a project that another user made by
// copying a manifest can never be reached through the reference of the first.
func (s *Service) ResolveSharedNode(ctx context.Context, viewer string, node int64) (Shared, error) {
	if err := s.permit(ctx, viewer); err != nil {
		return Shared{}, err
	}
	if !viewerOK(viewer) || node <= 0 {
		return Shared{}, ErrNotFound
	}
	a := s.o.Files.Actor(viewer)
	n, err := s.o.Files.Store().NodeByID(ctx, a, node)
	if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
		return Shared{}, ErrNotFound
	}
	if err != nil {
		return Shared{}, err
	}
	sh, ok, err := s.describeShared(ctx, a, viewer, n, s.ownerNames(ctx))
	if err != nil {
		return Shared{}, err
	}
	if !ok {
		return Shared{}, ErrNotFound
	}
	if sh.Status == StatusBroken {
		return sh, fmt.Errorf("%w: %s", ErrInvalid, sh.Error)
	}
	return sh, nil
}

// ownerNames returns a function that asks the display-name hook once for each
// owner during one call.
func (s *Service) ownerNames(ctx context.Context) func(owner string) string {
	seen := map[string]string{}
	return func(owner string) string {
		if n, ok := seen[owner]; ok {
			return n
		}
		n := s.ownerName(ctx, owner)
		seen[owner] = n
		return n
	}
}

// ParseProjectPath splits a workspace path of the form
// /users/<owner>/Projects/<slug>[/<rest>] into its parts. ok is false for any
// other path: a path whose first segment is not "users", an empty owner or
// slug, a Projects folder that is not spelled exactly "Projects", and a path
// where /users/<owner>/Projects is not at the start (such as
// /users/a/users/b/Projects/x). It is the one rule for the shape of a project
// folder: describeShared, the fence of an agent and the file triggers all use
// it. It looks at the text only; whether the folder holds a manifest is for
// the caller to read.
func ParseProjectPath(p string) (owner, slug, rest string, ok bool) {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if len(segs) < 4 || "/"+segs[0] != usersRoot || segs[1] == "" || segs[2] != RootDir || segs[3] == "" {
		return "", "", "", false
	}
	return segs[1], segs[3], strings.Join(segs[4:], "/"), true
}

// CoversOtherProjects says whether the workspace path p, as a root of a view
// for viewer, reaches the project folders of another user: it is a project
// folder of another user or inside one, or it is the Projects folder or the
// home of another user, or /users or /, which hold them. A root like this
// must stay out of the fence of an agent that has a folder. It looks at the
// text only. A path that only looks like the Projects folder (another case)
// is not one: the store does not treat it as one either.
func CoversOtherProjects(p, viewer string) bool {
	if owner, _, _, ok := ParseProjectPath(p); ok {
		return owner != viewer
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if segs[0] == "" { // "/"
		return true
	}
	if "/"+segs[0] != usersRoot {
		return false
	}
	switch {
	case len(segs) == 1:
		return true
	case segs[1] == viewer:
		return false
	case len(segs) == 2:
		return true
	case len(segs) == 3 && segs[2] == RootDir:
		return true
	}
	return false
}

// OwnsProjectPath says whether p is inside a project folder of owner.
func OwnsProjectPath(p, owner string) bool {
	o, _, _, ok := ParseProjectPath(p)
	return ok && o == owner
}

// viewerOK says whether viewer is a person: not empty, not the administrator
// or a group, and not a name that could be read as a path.
func viewerOK(viewer string) bool {
	return viewer != "" && !strings.HasPrefix(viewer, "@") && viewer != "." && viewer != ".." &&
		!strings.ContainsAny(viewer, "/\\\x00")
}

// candidates passes to add the project folders that the readable folder p
// covers, as nodes of the viewer's own listing.
func (s *Service) candidates(ctx context.Context, a workspace.Actor, viewer, p string, add func(workspace.Node)) error {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if len(segs) < 2 || "/"+segs[0] != usersRoot || segs[1] == viewer {
		return nil // the root, all homes (nobody has them), or the viewer's home
	}
	st := s.o.Files.Store()
	listDirs := func(dir string, fn func(workspace.Node)) error {
		kids, err := st.List(ctx, a, dir)
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, k := range kids {
			if k.Kind == workspace.KindDir {
				fn(k)
			}
		}
		return nil
	}
	switch {
	case len(segs) == 2: // a home: look for its Projects/ folder
		var roots []workspace.Node
		if err := listDirs(p, func(k workspace.Node) {
			if k.Name == RootDir {
				roots = append(roots, k)
			}
		}); err != nil {
			return err
		}
		for _, r := range roots {
			if err := listDirs(r.Path, add); err != nil {
				return err
			}
		}
		return nil
	case len(segs) == 3 && segs[2] == RootDir:
		return listDirs(p, add)
	case len(segs) == 4 && segs[2] == RootDir:
		n, err := st.Stat(ctx, a, p)
		if errors.Is(err, workspace.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		add(n)
	}
	// Deeper: a grant inside a project does not reach its manifest.
	return nil
}

// describeShared reads the manifest of a candidate through the viewer's grant.
// ok is false for a folder that is not a project folder of another user or
// that the viewer may not read.
func (s *Service) describeShared(ctx context.Context, a workspace.Actor, viewer string, n workspace.Node, ownerName func(string) string) (Shared, bool, error) {
	// The path is the store's, not the caller's: check its shape once more.
	owner, slug, rest, isProject := ParseProjectPath(n.Path)
	if !isProject || rest != "" || owner == viewer || slug != n.Name || n.Name == "" {
		return Shared{}, false, nil
	}
	st := s.o.Files.Store()
	// The viewer's effective mode: the store clamps the asked mode to the
	// grant the viewer has on the node or any folder above it.
	v, err := st.View(ctx, a, []workspace.MountRequest{{Path: n.Path, At: "x", Mode: workspace.ModeWrite}})
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrInvalid) {
			return Shared{}, false, nil
		}
		return Shared{}, false, err
	}
	// The store refuses a node below read, so a mount here is readable.
	if len(v.Mounts) != 1 || v.Mounts[0].NodeID != n.ID {
		return Shared{}, false, nil
	}
	mode := ModeRead
	if v.Mounts[0].Mode >= workspace.ModeWrite {
		mode = ModeWrite
	}
	mn, err := st.Stat(ctx, a, n.Path+"/"+ManifestFile)
	if errors.Is(err, workspace.ErrNotFound) {
		return Shared{}, false, nil
	}
	if err != nil {
		return Shared{}, false, err
	}
	if mn.Kind != workspace.KindFile {
		return Shared{}, false, nil // a project.json that is a folder: not a project
	}
	// The path was the node's when it was listed. A rename or move between
	// the checks above and the reads below could point the path at another
	// folder, so the reads are trusted only if the node still has this path.
	sh := Shared{
		Project:        Project{Slug: n.Name, Node: n.ID, Updated: mn.Modified, Status: StatusActive},
		OwnerName:      ownerName(owner),
		OwnerPrincipal: owner,
		Ref:            SharedRef(n.ID),
		Mode:           mode,
		FilesPath:      n.Path + "/files",
		MemoryPath:     n.Path + "/memory",
	}
	m, perr := s.readSharedManifest(ctx, a, mn)
	if cur, err := st.NodeByID(ctx, a, n.ID); err != nil || cur.Path != n.Path {
		if err != nil && !errors.Is(err, workspace.ErrNotFound) && !errors.Is(err, workspace.ErrInvalid) {
			return Shared{}, false, err
		}
		return Shared{}, false, nil
	}
	if perr != nil && !errors.Is(perr, ErrInvalid) {
		// A store, blob or IO error is not a problem of the manifest. Its text
		// may name a local path or an object-store key, so it is logged by the
		// caller's own logs only as a fact, never copied into the answer.
		xlog.Warn("projects: a shared project manifest could not be read", "ref", sh.Ref)
		return Shared{}, false, errManifestIO
	}
	if perr != nil {
		sh.Status, sh.Error = StatusBroken, perr.Error()
		sh.Name = n.Name
		return sh, true, nil
	}
	sh.ID, sh.Name, sh.Status, sh.Description = m.ID, m.Name, statusOf(m), m.Description
	return sh, true, nil
}

// readSharedManifest reads and parses the manifest node mn as the viewer. A
// manifest with no ID is broken: the ID is still the project's own name in
// its owner's index, but the identity of a shared project is its folder node,
// not this ID.
func (s *Service) readSharedManifest(ctx context.Context, a workspace.Actor, mn workspace.Node) (Manifest, error) {
	b, err := s.readSharedBytes(ctx, a, mn)
	if err != nil {
		return Manifest{}, err
	}
	m, err := ParseManifest(b)
	if err != nil {
		return Manifest{}, err
	}
	if m.ID == "" {
		return Manifest{}, fmt.Errorf("%w: the project has no id", ErrInvalid)
	}
	return m, nil
}

func (s *Service) readSharedBytes(ctx context.Context, a workspace.Actor, mn workspace.Node) ([]byte, error) {
	if mn.Kind != workspace.KindFile {
		return nil, fmt.Errorf("%w: %s is not a file", ErrInvalid, ManifestFile)
	}
	if mn.Size > manifestMax {
		return nil, fmt.Errorf("%w: %s is too large", ErrInvalid, ManifestFile)
	}
	rc, _, err := s.o.Files.Store().Open(ctx, a, mn.Path)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s is not readable", ErrInvalid, ManifestFile)
		}
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, manifestMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) > manifestMax {
		return nil, fmt.Errorf("%w: %s is too large", ErrInvalid, ManifestFile)
	}
	return b, nil
}

// DisplayName returns a display name for a person, from the host's hook, and
// never the account: a neutral text when the person has no name.
func (s *Service) DisplayName(ctx context.Context, user string) string { return s.ownerName(ctx, user) }

// ownerName returns a display name for an owner and never the account.
func (s *Service) ownerName(ctx context.Context, owner string) string {
	if s.o.OwnerName != nil {
		if n := strings.TrimSpace(s.o.OwnerName(ctx, owner)); n != "" && n != owner {
			return n
		}
	}
	return fallbackOwnerName
}
