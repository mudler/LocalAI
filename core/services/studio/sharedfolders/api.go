package sharedfolders

import (
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The API words of a mode. The REST endpoints and the assistant's tools share
// them, so both answer with the same JSON.
const (
	ModeRead  = "read"
	ModeWrite = "write"
)

// ModeName returns the API word of a mode.
func ModeName(m workspace.Mode) string {
	if m >= workspace.ModeWrite {
		return ModeWrite
	}
	return ModeRead
}

// ParseMode reads the API word of a mode. Any other word is ModeNone, which
// SetMember refuses with ErrBadMode.
func ParseMode(s string) workspace.Mode {
	switch s {
	case ModeRead:
		return workspace.ModeRead
	case ModeWrite:
		return workspace.ModeWrite
	}
	return workspace.ModeNone
}

// ToSchema returns the JSON of a folder: with its members for an
// administrator, with the viewer's mode for a member.
func ToSchema(f Folder, admin bool) schema.SharedFolder {
	out := schema.SharedFolder{ID: f.ID, Name: f.Name, Path: f.Path, Pages: f.Pages}
	if !admin {
		out.Mode = ModeName(f.Mode)
		out.Owner, out.Everyone = f.Owner, f.Everyone
		return out
	}
	out.Members = make([]schema.SharedFolderMember, 0, len(f.Members))
	for _, m := range f.Members {
		out.Members = append(out.Members, schema.SharedFolderMember{Principal: m.Principal, Name: m.Name,
			Everyone: m.Principal == Everyone, Mode: ModeName(m.Mode)})
	}
	return out
}

// EventToSchema returns the JSON of an audit line. name gives a user's name.
func EventToSchema(e Event, name func(id string) string) schema.SharedFolderEvent {
	out := schema.SharedFolderEvent{Actor: e.Actor, Action: e.Action, FolderID: e.FolderID, Folder: e.Folder,
		Principal: e.Principal, Rev: e.Rev, At: e.At}
	if e.Actor != ActorAssistant && name != nil {
		out.ActorName = name(e.Actor)
	}
	if e.Action == ActionGrant {
		out.Mode = ModeName(e.Mode)
	}
	return out
}

// ShareToSchema returns the owner's summary of a shared folder of their home.
func ShareToSchema(f Folder) schema.FolderShare {
	out := schema.FolderShare{ID: f.ID, Name: f.Name, Path: f.Path, SharedWith: len(f.Members)}
	for _, m := range f.Members {
		if m.Principal == Everyone {
			out.Everyone = true
		}
	}
	return out
}

// GrantsToSchema returns the grants of a folder of the owner's home in the
// shape of the sharing routes.
func GrantsToSchema(f Folder) schema.FolderGrantList {
	out := schema.FolderGrantList{ID: f.ID, Name: f.Name, Path: f.Path, Grants: make([]schema.FolderGrant, 0, len(f.Members))}
	for _, m := range f.Members {
		name := m.Name
		if m.Principal == Everyone {
			name = ""
		}
		out.Grants = append(out.Grants, schema.FolderGrant{UserID: m.Principal, DisplayName: name, Role: ModeName(m.Mode)})
	}
	return out
}
