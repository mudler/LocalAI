package studio

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// maxContextFolders bounds how many agent or channel folders one source lists,
// so a user with very many channels still gets a short answer.
const maxContextFolders = 100

// ContextFile is one file of a context group: the index the prompt carries or
// a topic file next to it.
type ContextFile struct {
	// Path is the path inside the user's home.
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Size     int64      `json:"size"`
	Index    bool       `json:"index,omitempty"`
	Lines    int        `json:"lines,omitempty"`
	Modified *time.Time `json:"modified,omitempty"`
}

// ContextGroup is the files of one source for one agent or channel. Key is the
// agent id or the channel folder name; it is empty for the user scope.
type ContextGroup struct {
	Source string             `json:"source"`
	Scope  contextfiles.Scope `json:"scope"`
	Key    string             `json:"key,omitempty"`
	// MaxBytes and MaxLines are the caps of the index. MaxLines is zero when
	// the file has no line cap.
	MaxBytes int           `json:"max_bytes"`
	MaxLines int           `json:"max_lines,omitempty"`
	Files    []ContextFile `json:"files"`
}

// ContextLimits are the caps that keep a turn small.
type ContextLimits struct {
	IndexLines   int `json:"index_lines"`
	IndexBytes   int `json:"index_bytes"`
	ProfileBytes int `json:"profile_bytes"`
	TurnBytes    int `json:"turn_bytes"`
}

// ContextFiles is what the Memory page shows.
type ContextFiles struct {
	Limits ContextLimits  `json:"limits"`
	Groups []ContextGroup `json:"groups"`
}

// ListContextFiles lists the context files of the user: for every registered
// source the files of its folder, for every agent or channel that has one, and
// the agent profiles. It only reads.
func (s *Service) ListContextFiles(ctx context.Context, user string) (ContextFiles, error) {
	out := ContextFiles{
		Limits: ContextLimits{IndexLines: contextfiles.MaxIndexLines, IndexBytes: contextfiles.MaxIndexBytes,
			ProfileBytes: contextfiles.MaxProfileBytes, TurnBytes: contextfiles.MaxTotalBytes},
		Groups: []ContextGroup{},
	}
	if !s.d.Files.Available() {
		return out, nil
	}
	principal := s.Principal(user)
	for _, src := range s.ctxfiles.Sources(s.allAddonContextSources(ctx, user)...) {
		limit := src.Cap
		if limit == 0 {
			limit = contextfiles.MaxIndexBytes
		}
		for _, rel := range expandSource(ctx, s.d.Files, principal, src) {
			g := ContextGroup{Source: src.ID, Scope: src.Scope, Key: rel.key, MaxBytes: limit, MaxLines: contextfiles.MaxIndexLines}
			g.Files = s.listContextDir(ctx, principal, path.Dir(rel.path), path.Base(rel.path))
			if len(g.Files) > 0 {
				out.Groups = append(out.Groups, g)
			}
		}
	}
	for _, agent := range listNames(ctx, s.d.Files, principal, "LocalAI/agents", true) {
		rel := contextfiles.ProfilePath(agent)
		g := ContextGroup{Source: "profile", Scope: contextfiles.ScopeAgent, Key: agent, MaxBytes: contextfiles.MaxProfileBytes}
		for _, f := range s.listContextDir(ctx, principal, path.Dir(rel), path.Base(rel)) {
			// The profile is the only file the prompt carries here; skills and
			// other files of the agent folder are not context.
			if f.Index {
				g.Files = append(g.Files, f)
			}
		}
		if len(g.Files) > 0 {
			out.Groups = append(out.Groups, g)
		}
	}
	return out, nil
}

type contextPath struct{ path, key string }

// expandSource turns the path of a source into the paths of the folders that
// exist: the placeholder segment is replaced with each folder name found.
func expandSource(ctx context.Context, f *files.Service, principal string, src contextfiles.Source) []contextPath {
	segs := strings.Split(src.Path, "/")
	paths := []contextPath{{path: ""}}
	for _, seg := range segs {
		var next []contextPath
		for _, p := range paths {
			if seg != "{agent}" && seg != "{channel}" {
				next = append(next, contextPath{path: path.Join(p.path, seg), key: p.key})
				continue
			}
			for _, name := range listNames(ctx, f, principal, p.path, true) {
				next = append(next, contextPath{path: path.Join(p.path, name), key: name})
			}
		}
		paths = next
	}
	return paths
}

// listNames returns the names of the folders (dirs) or files in a folder of
// the home. A missing folder is an empty list.
func listNames(ctx context.Context, f *files.Service, principal, rel string, dirs bool) []string {
	abs, err := f.Abs(principal, rel)
	if err != nil {
		return nil
	}
	kids, err := f.Store().List(ctx, f.Actor(principal), abs)
	if err != nil {
		return nil
	}
	var names []string
	for _, k := range kids {
		if (k.Kind == workspace.KindDir) == dirs {
			names = append(names, k.Name)
		}
		if len(names) == maxContextFolders {
			break
		}
	}
	return names
}

// listContextDir lists the Markdown files of a folder; the file named index is
// marked and its non-blank lines counted.
func (s *Service) listContextDir(ctx context.Context, principal, dir, index string) []ContextFile {
	f := s.d.Files
	abs, err := f.Abs(principal, dir)
	if err != nil {
		return nil
	}
	kids, err := f.Store().List(ctx, f.Actor(principal), abs)
	if err != nil {
		return nil
	}
	var out []ContextFile
	for _, k := range kids {
		if k.Kind != workspace.KindFile || !strings.HasSuffix(k.Name, ".md") {
			continue
		}
		cf := ContextFile{Path: path.Join(dir, k.Name), Name: k.Name, Size: k.Size, Index: k.Name == index}
		if !k.Modified.IsZero() {
			m := k.Modified
			cf.Modified = &m
		}
		if cf.Index {
			cf.Lines = s.countLines(ctx, principal, cf.Path)
			// The index comes first.
			out = append([]ContextFile{cf}, out...)
			continue
		}
		out = append(out, cf)
	}
	return out
}

func (s *Service) countLines(ctx context.Context, principal, rel string) int {
	abs, err := s.d.Files.Abs(principal, rel)
	if err != nil {
		return 0
	}
	r, _, err := s.d.Files.Store().Open(ctx, s.d.Files.Actor(principal), abs)
	if err != nil {
		return 0
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, 64<<10))
	if err != nil && !errors.Is(err, io.EOF) {
		return 0
	}
	n := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
