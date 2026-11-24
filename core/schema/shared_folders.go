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
	// Owner is the display name of the person who shared a folder of their
	// own home with you. Such a folder has no path: it would name the
	// owner's account. Only in your own list.
	Owner string `json:"owner,omitempty"`
	// Everyone is true when you reach a folder of someone's home only
	// through its grant to every account. Only in your own list.
	Everyone bool `json:"everyone,omitempty"`
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

// FolderShare is a folder of your home and how many it is shared with.
type FolderShare struct {
	// ID is the folder's stable ID in the sharing routes.
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Path is the folder's path in your home, for example /Projects.
	Path string `json:"path"`
	// SharedWith counts the grants, the one to every account included.
	SharedWith int `json:"sharedWith"`
	// Everyone is true when the folder is shared with every account.
	Everyone bool `json:"everyone"`
}

// FolderShareList is the list of the folders of your home that are shared.
type FolderShareList struct {
	// Sharing is false when folders cannot be shared on this server
	// (local-owner mode, no accounts, files off).
	Sharing bool          `json:"sharing"`
	Folders []FolderShare `json:"folders"`
}

// FolderSharePathRequest names a folder of your home by its path.
type FolderSharePathRequest struct {
	Path string `json:"path"`
}

// FolderGrant is one grant on a folder of your home, in the shape of the
// sharing routes: the account ID (or @everyone), its display name and the
// role read or write.
type FolderGrant struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

// FolderGrantList is the grants on a folder of your home.
type FolderGrantList struct {
	ID     int64         `json:"id"`
	Name   string        `json:"name"`
	Path   string        `json:"path"`
	Grants []FolderGrant `json:"grants"`
}

// FolderGrantRequest is the role of a grant: read or write.
type FolderGrantRequest struct {
	Role string `json:"role"`
}

// FolderShareUser is one result of the user picker: the account ID and the
// display name, nothing else.
type FolderShareUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// FolderShareUserList is the answer of the user picker.
type FolderShareUserList struct {
	Users []FolderShareUser `json:"users"`
}
