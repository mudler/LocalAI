package session

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// FileOrigin says how a file came into a chat.
type FileOrigin string

// File origins.
const (
	// FileFromUser is a file the user attached to a message, or put in the
	// chat folder.
	FileFromUser FileOrigin = "user"
	// FileFromModel is a file the model wrote in the sandbox: a turn's
	// checkpoint committed it.
	FileFromModel FileOrigin = "model"
	// FileFromTool is a file a host tool of the chat wrote, such as write_file.
	FileFromTool FileOrigin = "tool"
)

// Limits of a chat's file index.
const (
	// maxChatHistory is how many change sets of one chat the index reads.
	maxChatHistory = 1000
	// maxFolderNodes bounds the walk of the chat folder.
	maxFolderNodes = 2000
)

// ChatFile is one file of a chat: in the chat folder, or written elsewhere in
// the home folder by the chat's turns. Path is the current workspace path, so
// it follows a rename of the chat folder.
type ChatFile struct {
	NodeID    int64
	Path      string // workspace path, now
	GuestPath string // the path the model's tools use
	Name      string
	Size      int64
	Mime      string
	Origin    FileOrigin
	// Changed is true when the chat changed a file it did not create: a file
	// the user attached, or one that was in the home folder before.
	Changed bool
	// Turn is the turn that brought the file in (0 when not known), and At
	// the time of that change.
	Turn int
	At   time.Time
}

// ChatFiles is the file index of a chat.
type ChatFiles struct {
	Files []ChatFile
	// Folder is the guest path of the chat folder, or "" when the chat has
	// none yet.
	Folder string
	// Stale are chat folder paths (guest paths, ending in "/") that earlier
	// messages name and that no longer exist, such as the name of the chat
	// folder before a rename.
	Stale []string
}

// guestPath turns a workspace path inside home into the path the tools see.
// A path outside home gives "".
func guestPath(home, p string) string {
	if p != home && !strings.HasPrefix(p, home+"/") {
		return ""
	}
	return GuestRoot + "/" + GuestHome + strings.TrimPrefix(p, home)
}

// workspacePath is guestPath reversed. A path outside the home mount gives "".
func workspacePath(home, guest string) string {
	root := GuestRoot + "/" + GuestHome
	if guest != root && !strings.HasPrefix(guest, root+"/") {
		return ""
	}
	return home + strings.TrimPrefix(guest, root)
}

// textTypes are the types of the text files a chat writes most. The table of
// the mime package reads the types of the operating system, and these differ:
// macOS, for one, knows no ".md". The prompt and the file list must say the
// same on every host.
var textTypes = map[string]string{
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".csv":      "text/csv",
	".txt":      "text/plain",
}

// MimeOf guesses the type of a file from its name.
func MimeOf(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := textTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		if i := strings.IndexByte(t, ';'); i > 0 {
			t = t[:i]
		}
		return t
	}
	return "application/octet-stream"
}

// staleRe finds the chat folders that a transcript names.
var staleRe = regexp.MustCompile(regexp.QuoteMeta(GuestRoot+"/"+GuestHome+"/"+ChatsDir+"/") + `([^/\n]+)/`)

// attachment is a file that a user message named.
type attachment struct {
	turn int
	at   time.Time
}

// Files returns the file index of a chat of principal: the files in the chat
// folder and the files that the chat's turns created or changed elsewhere in
// the home folder, with their current paths. It is computed from the
// transcript and the workspace history, so every frontend gives the same
// answer and nothing is kept in memory. Another principal's chat is
// ErrNotFound. Without a workspace the index is empty.
func (m *Manager) Files(ctx context.Context, principal, chatID string) (ChatFiles, error) {
	rec, err := m.o.Store.GetChat(ctx, principal, chatID)
	if err != nil {
		return ChatFiles{}, err
	}
	if m.o.Workspace == nil || !validPrincipal(principal) {
		return ChatFiles{}, nil
	}
	entries, err := m.entries(ctx, chatID)
	if err != nil {
		return ChatFiles{}, fmt.Errorf("session: read the transcript: %w", err)
	}
	ix := fileIndex{
		ws: m.o.Workspace, user: workspace.Actor{Principal: principal}, home: HomePath(principal),
		entries: entries, byID: map[int64]*ChatFile{},
	}
	if err := ix.build(ctx, rec); err != nil {
		return ChatFiles{}, err
	}
	return ix.result(rec), nil
}

// entries reads the whole transcript of a chat.
func (m *Manager) entries(ctx context.Context, chatID string) ([]Entry, error) {
	var all []Entry
	var after int64
	for {
		page, err := m.o.Store.Entries(ctx, chatID, after, 500)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 500 {
			return all, nil
		}
		after = page[len(page)-1].Seq
	}
}

// fileIndex builds one ChatFiles.
type fileIndex struct {
	ws      workspace.Store
	user    workspace.Actor
	home    string
	entries []Entry
	byID    map[int64]*ChatFile
	stale   []string
}

// agentNode is what the chat's own change sets did to one node.
type agentNode struct {
	origin  FileOrigin
	created bool // the first change of the chat created the node
	gone    bool // the last change of the chat deleted it
	path    string
	turn    int
	at      time.Time
}

func (ix *fileIndex) build(ctx context.Context, rec ChatRecord) error {
	agent, err := ix.agentNodes(ctx, rec.ID)
	if err != nil {
		return err
	}
	attached := ix.attachments()
	if rec.Folder != "" {
		if err := ix.walkFolder(ctx, ChatsPath(rec.Principal)+"/"+rec.Folder, agent, attached); err != nil {
			return err
		}
	}
	// Files that a message named outside the folder, such as the source
	// chat's files in a fork.
	for p, a := range attached {
		if err := ix.addPath(ctx, p, func(f *ChatFile) {
			f.Origin, f.Turn, f.At = FileFromUser, a.turn, a.at
		}); err != nil {
			return err
		}
	}
	for id, n := range agent {
		if n.gone || ix.byID[id] != nil {
			continue
		}
		if err := ix.addPath(ctx, n.path, func(f *ChatFile) {
			if f.NodeID != id {
				return
			}
			f.Origin, f.Changed, f.Turn, f.At = n.origin, !n.created, n.turn, n.at
		}); err != nil {
			return err
		}
	}
	// A file found by path that is not the node the chat wrote has no origin.
	for id, f := range ix.byID {
		if f.Origin == "" {
			delete(ix.byID, id)
		}
	}
	return ix.findStale(ctx, rec)
}

// agentNodes reads the change sets that the chat made and folds them per node.
// A change set that a turn's checkpoint names is the model's work in the
// sandbox; any other is a host tool's.
func (ix *fileIndex) agentNodes(ctx context.Context, chatID string) (map[int64]*agentNode, error) {
	sets, err := ix.ws.History(ctx, ix.user, workspace.HistoryQuery{Path: ix.home, ChatID: chatID, Limit: maxChatHistory})
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, nil // no home folder yet
		}
		return nil, fmt.Errorf("session: read the chat's changes: %w", err)
	}
	checkpoints := map[int64]int{}
	for _, e := range ix.entries {
		if e.Role == RoleCheckpoint && e.Rev != 0 {
			checkpoints[e.Rev] = e.Turn
		}
	}
	slices.Reverse(sets) // oldest first
	out := map[int64]*agentNode{}
	for _, cs := range sets {
		origin := FileFromTool
		turn, ok := checkpoints[cs.Rev]
		if ok {
			origin = FileFromModel
		} else {
			turn = ix.turnAt(cs.CreatedAt)
		}
		for _, op := range cs.Ops {
			n := out[op.NodeID]
			if n == nil {
				n = &agentNode{origin: origin, created: op.Kind == workspace.OpCreate, turn: turn, at: cs.CreatedAt}
				out[op.NodeID] = n
			}
			n.path, n.gone = op.NewPath, op.Kind == workspace.OpDelete
		}
	}
	return out, nil
}

// turnAt is the turn of the last transcript entry stored at or before t: a
// tool's change is made after the user message of its turn is stored. The
// workspace keeps times to the millisecond, so the entries are compared at
// that precision.
func (ix *fileIndex) turnAt(t time.Time) int {
	i := sort.Search(len(ix.entries), func(i int) bool { return ix.entries[i].At.Truncate(time.Millisecond).After(t) })
	if i == 0 {
		return 0
	}
	return ix.entries[i-1].Turn
}

// attachments returns the workspace paths that user messages attached, with
// the turn of the message. A later message wins.
func (ix *fileIndex) attachments() map[string]attachment {
	out := map[string]attachment{}
	for _, e := range ix.entries {
		if e.Role != RoleUser {
			continue
		}
		// An old message holds the list in its text.
		for _, a := range DisplayEntry(e).Attachments {
			if p := workspacePath(ix.home, a.GuestPath()); p != "" {
				out[p] = attachment{turn: e.Turn, at: e.At}
			}
		}
	}
	return out
}

// walkFolder adds every file below the chat folder.
func (ix *fileIndex) walkFolder(ctx context.Context, folder string, agent map[int64]*agentNode, attached map[string]attachment) error {
	// An attachment is told by its name: the folder may have been renamed
	// since the message named it.
	byName := map[string]attachment{}
	for p, a := range attached {
		byName[path.Base(p)] = a
	}
	queue := []string{folder}
	seen := 0
	for len(queue) > 0 && seen < maxFolderNodes {
		dir := queue[0]
		queue = queue[1:]
		kids, err := ix.ws.List(ctx, ix.user, dir)
		if err != nil {
			if errors.Is(err, workspace.ErrNotFound) {
				continue
			}
			return fmt.Errorf("session: list the chat folder: %w", err)
		}
		for _, k := range kids {
			if seen++; seen > maxFolderNodes {
				break
			}
			if k.Kind == workspace.KindDir {
				queue = append(queue, k.Path)
				continue
			}
			f := ix.fileOf(k)
			f.Origin = FileFromUser
			if a, ok := byName[k.Name]; ok {
				f.Turn, f.At = a.turn, a.at
			}
			if n := agent[k.ID]; n != nil {
				if n.created {
					f.Origin, f.Turn, f.At = n.origin, n.turn, n.at
				} else {
					f.Changed = true
				}
			}
			ix.byID[k.ID] = f
		}
	}
	return nil
}

// addPath adds the file at p when it exists and is not in the index yet; set
// fills its origin. A missing path is skipped: the file was moved or deleted.
func (ix *fileIndex) addPath(ctx context.Context, p string, set func(*ChatFile)) error {
	if guestPath(ix.home, p) == "" {
		return nil
	}
	n, err := ix.ws.Stat(ctx, ix.user, p)
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("session: read a chat file: %w", err)
	case n.Kind != workspace.KindFile || ix.byID[n.ID] != nil:
		return nil
	}
	f := ix.fileOf(n)
	set(f)
	ix.byID[n.ID] = f
	return nil
}

func (ix *fileIndex) fileOf(n workspace.Node) *ChatFile {
	return &ChatFile{
		NodeID: n.ID, Path: n.Path, GuestPath: guestPath(ix.home, n.Path), Name: n.Name,
		Size: n.Size, Mime: MimeOf(n.Name),
	}
}

// findStale lists the chat folders that the transcript names and that are
// gone: after a rename, the model has seen paths that no longer work.
func (ix *fileIndex) findStale(ctx context.Context, rec ChatRecord) error {
	if rec.Folder == "" {
		return nil
	}
	seen := map[string]bool{rec.Folder: true}
	chats := ChatsPath(rec.Principal)
	for _, e := range ix.entries {
		if e.Role == RoleReasoning || e.Role == RoleCheckpoint {
			continue
		}
		for _, m := range staleRe.FindAllStringSubmatch(ModelText(e), -1) {
			name := m[1]
			if seen[name] {
				continue
			}
			seen[name] = true
			_, err := ix.ws.Stat(ctx, ix.user, chats+"/"+name)
			switch {
			case errors.Is(err, workspace.ErrNotFound):
				ix.stale = append(ix.stale, m[0])
			case err != nil && !errors.Is(err, workspace.ErrBadName):
				return fmt.Errorf("session: check an old chat folder: %w", err)
			}
		}
	}
	return nil
}

// result sorts the index: by turn, then by path.
func (ix *fileIndex) result(rec ChatRecord) ChatFiles {
	out := ChatFiles{Files: make([]ChatFile, 0, len(ix.byID)), Stale: ix.stale}
	for _, f := range ix.byID {
		out.Files = append(out.Files, *f)
	}
	sort.Slice(out.Files, func(i, j int) bool {
		a, b := out.Files[i], out.Files[j]
		if a.Turn != b.Turn {
			return a.Turn < b.Turn
		}
		return a.Path < b.Path
	})
	if rec.Folder != "" {
		out.Folder = GuestRoot + "/" + GuestHome + "/" + ChatsDir + "/" + rec.Folder + "/"
	}
	return out
}

// MaxPromptFiles is how many files the prompt block lists. The rest are
// counted, so a chat with many files costs a bounded number of tokens.
const MaxPromptFiles = 50

// FilesPrompt is the block that tells the model which files the chat has, by
// their current paths. It lists the newest MaxPromptFiles files, grouped by
// folder so that a long folder path is written once, and counts the rest. It
// is "" for a chat with no files and no stale folder.
func FilesPrompt(cf ChatFiles) string {
	if len(cf.Files) == 0 && len(cf.Stale) == 0 {
		return ""
	}
	var b strings.Builder
	if len(cf.Files) > 0 {
		b.WriteString("Files in this conversation (current paths):\n")
		files := cf.Files
		if len(files) > MaxPromptFiles {
			files = files[len(files)-MaxPromptFiles:]
		}
		var dirs []string
		byDir := map[string][]ChatFile{}
		for _, f := range files {
			d := path.Dir(f.GuestPath)
			if byDir[d] == nil {
				dirs = append(dirs, d)
			}
			byDir[d] = append(byDir[d], f)
		}
		for _, d := range dirs {
			fmt.Fprintf(&b, "%s/\n", d)
			for _, f := range byDir[d] {
				fmt.Fprintf(&b, "- %s (%s, %s) %s", f.Name, SizeLabel(f.Size), f.Mime, originLabel(f))
				if f.Turn > 0 {
					fmt.Fprintf(&b, ", turn %d", f.Turn)
				}
				b.WriteByte('\n')
			}
		}
		if n := len(cf.Files) - len(files); n > 0 {
			fmt.Fprintf(&b, "and %d older files; list %s to see them.\n", n, cf.Folder)
		}
	}
	if len(cf.Stale) > 0 && cf.Folder != "" {
		fmt.Fprintf(&b, "Earlier messages name %s, which no longer exists: this chat's folder was renamed to %s. Use the paths above.\n",
			strings.Join(cf.Stale, ", "), cf.Folder)
	}
	return strings.TrimRight(b.String(), "\n")
}

func originLabel(f ChatFile) string {
	switch {
	case f.Origin == FileFromUser && f.Changed:
		return "uploaded by the user, changed by you"
	case f.Origin == FileFromUser:
		return "uploaded by the user"
	case f.Changed:
		return "changed by you"
	}
	return "created by you"
}

// SizeLabel writes a size in B, KB or MB, short enough for a prompt line.
func SizeLabel(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
