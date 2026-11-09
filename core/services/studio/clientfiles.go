package studio

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// chatFiles is the FileStore of the client tools in one chat. It reads and
// writes through the chat's place and as the chat's actor, so the fence of an
// agent's folder applies exactly as it does to the chat's own file tools and to
// the media tools: a reference outside the fence is not found, and a result
// lands inside the agent's folder (or the chat's own folder), never in the rest
// of the user's home.
type chatFiles struct {
	f *files.Service
	x session.Access
	// changed tells the chat's clients that its files changed, so the Files
	// panel reads the list again. Nil: nobody is told.
	changed func()
}

// clientFilesOf returns the fenced file store of a chat. Without the files
// service, or without an access, it returns a store that refuses everything:
// the client tools must never fall back to the whole home.
func (s *Service) clientFilesOf(x session.Access) localaitools.FileStore {
	if !s.d.Files.Available() || x.Actor().Principal == "" {
		return noFiles{}
	}
	a := x.Actor()
	return chatFiles{f: s.d.Files, x: x, changed: func() {
		s.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindFiles})
	}}
}

// noFiles refuses every read and write.
type noFiles struct{}

var errNoFiles = errors.New("the user's files are not available in this chat")

func (noFiles) Read(_ context.Context, ref string, max int64) (localaitools.File, error) {
	if len(ref) > 5 && ref[:5] == "data:" {
		return localaitools.ReadDataURL(ref, max)
	}
	return localaitools.File{}, localaitools.ErrFileNotFound
}

func (noFiles) Write(context.Context, string, []byte, string) (string, error) {
	return "", errNoFiles
}

func (c chatFiles) Read(ctx context.Context, ref string, max int64) (localaitools.File, error) {
	if len(ref) > 5 && ref[:5] == "data:" {
		return localaitools.ReadDataURL(ref, max)
	}
	t, err := c.x.Place().Resolve(ref)
	if err != nil {
		// Not found, not allowed and a bad name look the same: the tool must
		// not tell which paths exist outside the fence.
		return localaitools.File{}, localaitools.ErrFileNotFound
	}
	r, n, err := c.f.Store().Open(ctx, c.x.Actor(), t.Abs)
	if err != nil {
		return localaitools.File{}, localaitools.ErrFileNotFound
	}
	defer func() { _ = r.Close() }()
	if n.Size > max {
		return localaitools.File{}, localaitools.ErrFileTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return localaitools.File{}, err
	}
	return localaitools.File{Name: n.Name, Data: data}, nil
}

// Write stores a result in the "generated" folder of the chat's primary place
// (the agent's folder for a folder-scoped agent, the home otherwise). When
// that place is read-only, the chat's own folder takes it.
func (c chatFiles) Write(ctx context.Context, name string, data []byte, _ string) (string, error) {
	unique := localaitools.UniqueName(name)
	t, err := c.x.Place().Resolve("generated/" + unique)
	if err != nil || !t.Write {
		if t, err = c.x.Place().Resolve(session.GuestRoot + "/" + session.GuestChat + "/generated/" + unique); err != nil || !t.Write {
			return "", errors.New("there is no folder in this chat where a result may be saved")
		}
	}
	a := c.x.Actor()
	n, err := c.f.Store().Put(ctx, a, t.Abs, bytes.NewReader(data), workspace.PutOptions{Title: "Generated " + unique})
	if err != nil {
		return "", err
	}
	c.f.Written(ctx, n, a.Principal)
	// The file is in the chat's index already (the change set carries the
	// chat), but a client only reads the index again when it is told. The file
	// tools tell it; a client tool that saves a result must too.
	if c.changed != nil {
		c.changed()
	}
	return t.Guest(t.Abs), nil
}
