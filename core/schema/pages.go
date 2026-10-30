package schema

import "time"

// PagesPage is one page of the user's workspace: the folder pages/<name>/ of
// the home, with an index.html.
type PagesPage struct {
	Name        string `json:"name"`
	Path        string `json:"path"` // inside the home: /pages/<name>
	Title       string `json:"title"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
	// Source is "own" for a page in the user's home and "shared" for a page of
	// a shared folder.
	Source string `json:"source"`
	// Author is who wrote the current index.html: "you" or "assistant" for an
	// own page. A shared page also has AuthorID and AuthorName, the account
	// that last wrote its index.html.
	Author     string `json:"author"`
	AuthorID   string `json:"author_id,omitempty"`
	AuthorName string `json:"author_name,omitempty"`
	// A shared page has its shared folder (Folder, a workspace path, and
	// FolderID, the id the shared-pages routes take), whether you may write in
	// it, the hash of its code (index.html and page.json; a consent is kept
	// against it) and your consent: none, allowed or changed.
	Folder   string `json:"folder,omitempty"`
	FolderID int64  `json:"folder_id,omitempty"`
	Writable bool   `json:"writable,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Consent  string `json:"consent,omitempty"`
	MetaSize int64  `json:"meta_size,omitempty"`
	// UpdatedAt and UpdatedBy are the last change to anything in the folder.
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
	Rev       int64     `json:"rev"`
	Size      int64     `json:"size"`
}

// PagesList is the user's pages, sorted by name.
type PagesList struct {
	Pages []PagesPage `json:"pages"`
}

// PagesSource is a page with the text of its index.html.
type PagesSource struct {
	Page PagesPage `json:"page"`
	HTML string    `json:"html"`
}

// PagesFile is an entry of a page's folder. Path is relative to the folder.
type PagesFile struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"` // file or dir
	Size       int64  `json:"size"`
	Version    int64  `json:"version"`
	ConflictOf string `json:"conflict_of,omitempty"`
}

// PagesFileList is a folder of a page.
type PagesFileList struct {
	Entries []PagesFile `json:"entries"`
}

// PagesFileContent is a text file of a page's folder.
type PagesFileContent struct {
	PagesFile
	Content string `json:"content"`
}

// PagesWriteRequest writes a text file of a page's folder.
type PagesWriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Base is the version the page read. A file that changed since is kept and
	// the content goes to a conflict copy. Zero replaces the file.
	Base int64 `json:"base,omitempty"`
}

// PagesChatRequest sends a message from a page to a chat.
type PagesChatRequest struct {
	Text string `json:"text"`
	// ChatID is a chat of the user. Empty means the page's own chat, which is
	// created on first use.
	ChatID string `json:"chat_id,omitempty"`
	// Model is the model of a new page chat. Empty takes the model of the
	// user's most recent chat.
	Model string `json:"model,omitempty"`
}

// PagesChatResponse says where the message went.
type PagesChatResponse struct {
	ChatID    string `json:"chat_id"`
	Title     string `json:"title"`
	Started   bool   `json:"started"`
	PendingID string `json:"pending_id,omitempty"`
}

// PagesStarter is a page that ships with the server.
type PagesStarter struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
}

// PagesStarterList lists the starter pages.
type PagesStarterList struct {
	Starters []PagesStarter `json:"starters"`
}

// PagesSharedList is the pages of your shared folders. Enabled is false when an
// administrator switched shared pages off: the pages are listed, but none
// opens.
type PagesSharedList struct {
	Enabled bool        `json:"enabled"`
	Pages   []PagesPage `json:"pages"`
}

// PagesConsent is your consent to a shared page.
type PagesConsent struct {
	// State is none, allowed (this version) or changed (an earlier version).
	State string `json:"state"`
	// The version you allowed: when, and the size of its index.html and
	// page.json and the lines of its index.html, to say what changed.
	AllowedAt *time.Time `json:"allowed_at,omitempty"`
	Size      int64      `json:"size,omitempty"`
	MetaSize  int64      `json:"meta_size,omitempty"`
	Lines     int        `json:"lines,omitempty"`
}

// PagesSharedSource is a shared page with the text of its index.html, its
// lines, and your consent. The UI runs the HTML only after a consent to Hash.
type PagesSharedSource struct {
	Page    PagesPage    `json:"page"`
	HTML    string       `json:"html"`
	Lines   int          `json:"lines"`
	Consent PagesConsent `json:"consent"`
}

// PagesConsentRequest allows the version Hash of a shared page, for good or
// for one visit (Once).
type PagesConsentRequest struct {
	Hash string `json:"hash"`
	Once bool   `json:"once,omitempty"`
}

// PagesConsentResponse is the consent you gave. A visit (kind once) has a
// token that the bridge calls of that visit send in X-LocalAI-Page-Visit.
type PagesConsentResponse struct {
	State   string     `json:"state"`
	Kind    string     `json:"kind"`
	Hash    string     `json:"hash"`
	Token   string     `json:"token,omitempty"`
	Expires *time.Time `json:"expires,omitempty"`
}
