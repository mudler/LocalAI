package schema

import "time"

// SharedFolderMember is one member of a shared folder: a user, or "@everyone"
// for every user, and what they may do.
type SharedFolderMember struct {
	// Principal is the user ID, or "@everyone".
	Principal string `json:"principal"`
	// Name is the user's name; for "@everyone" it is "@everyone".
	Name string `json:"name"`
	// Everyone is true for the member that stands for every user.
	Everyone bool `json:"everyone,omitempty"`
	// Mode is read or write.
	Mode string `json:"mode"`
}

// SharedFolder is a folder an administrator shares with users.
type SharedFolder struct {
	// ID is the folder's stable ID. The shared pages use it as folder_id.
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Path is the folder's workspace path, /shared/<name>.
	Path string `json:"path"`
	// Pages is how many pages (pages/<name>/index.html) the folder has.
	Pages int `json:"pages"`
	// Mode is what you may do in the folder: read or write. Only in your
	// own list.
	Mode string `json:"mode,omitempty"`
	// Members is who may use the folder. Only in the administrator's view.
	Members []SharedFolderMember `json:"members,omitempty"`
}

// SharedFolderList is a list of shared folders.
type SharedFolderList struct {
	Folders []SharedFolder `json:"folders"`
	// MaxFolders and MaxMembers are the limits, in the administrator's view.
	MaxFolders int `json:"max_folders,omitempty"`
	MaxMembers int `json:"max_members,omitempty"`
}

// SharedFolderNameRequest names a shared folder: 1 to 64 letters, digits,
// spaces, dots, dashes or underscores, starting with a letter or a digit.
type SharedFolderNameRequest struct {
	Name string `json:"name"`
}

// SharedFolderRemoved is the answer to the removal of a shared folder.
type SharedFolderRemoved struct {
	Folder SharedFolder `json:"folder"`
	// UndoRev is the change to undo with POST /api/chats/admin/shared-folders/restore.
	UndoRev int64 `json:"undo_rev"`
}

// SharedFolderRestoreRequest names the removal to undo.
type SharedFolderRestoreRequest struct {
	Rev int64 `json:"rev"`
}

// SharedFolderMemberRequest sets what a member may do: read or write.
type SharedFolderMemberRequest struct {
	Mode string `json:"mode"`
}

// SharedFolderEvent is one line of the audit of the shared folders.
type SharedFolderEvent struct {
	// Actor is the user who made the change, or "@assistant".
	Actor     string `json:"actor"`
	ActorName string `json:"actor_name,omitempty"`
	// Action is create, rename, delete, restore, grant or revoke.
	Action    string    `json:"action"`
	FolderID  int64     `json:"folder_id"`
	Folder    string    `json:"folder"`
	Principal string    `json:"principal,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	Rev       int64     `json:"rev,omitempty"`
	At        time.Time `json:"at"`
}

// SharedFolderEventList is the newest lines of the audit, newest first.
type SharedFolderEventList struct {
	Events []SharedFolderEvent `json:"events"`
}
