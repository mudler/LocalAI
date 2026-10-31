package schema

import "encoding/json"

// StudioHistoryTypes are the Studio workspaces whose history is kept
// server-side, as named by the Studio add-on (addons/studio/ui/src/utils/
// mediaHistoryStore.js). 3D is included: its entries carry the URL of the
// generated mesh, the GLB itself stays under /generated-3d.
var StudioHistoryTypes = []string{"image", "video", "tts", "sound", "audio-transform", "diarization", "threed"}

// StudioHistoryEntry is one Studio generation (a picture, a clip, a voice
// line...) persisted server-side, so the Studio's history survives a browser
// change or a cleared cache like chat history does (issue #9432).
//
// Entry is the record exactly as the Studio writes it (prompt, model,
// params, results, lineage); it is stored opaquely so the server stays
// agnostic to the shape of each workspace. ID, Type and CreatedAt are
// copied out of it into columns for listing and ordering.
type StudioHistoryEntry struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Entry     json.RawMessage `json:"entry" swaggertype:"object"`
	CreatedAt int64           `json:"createdAt"` // Unix milliseconds, client-managed
	UpdatedAt int64           `json:"updatedAt"` // Unix milliseconds, client-managed
	// Version is assigned by the server: 1 on the first save, +1 on every
	// later write including the delete. Ignored on input.
	Version int64 `json:"version,omitempty"`
}

// StudioHistoryTombstone marks an entry the user deleted. The server keeps it
// (without content) so another browser holding a stale copy drops it instead
// of uploading it again.
type StudioHistoryTombstone struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Version   int64  `json:"version"`
	DeletedAt int64  `json:"deletedAt"` // Unix milliseconds
}

// StudioHistoryList is the GET /api/studio/history response.
//
// CacheKey is an opaque tag for the signed-in account, derived from the user
// id. The Studio, which is not told who is signed in, uses it to keep one
// browser cache per account, so two people sharing a browser never see or
// upload each other's history.
//
// Enabled is false when this LocalAI keeps no Studio history (no user
// authentication): the list is then empty and the browser stays the record.
// The Studio asks on every load, so that answer is a 200, not a 404 that the
// browser would log as an error.
type StudioHistoryList struct {
	Enabled  bool                     `json:"enabled"`
	Entries  []StudioHistoryEntry     `json:"entries"`
	Deleted  []StudioHistoryTombstone `json:"deleted"`
	CacheKey string                   `json:"cacheKey,omitempty"`
}

// StudioHistoryImportRequest is the POST /api/studio/history/import payload:
// entries a browser had before its history was kept on the server.
type StudioHistoryImportRequest struct {
	Entries []StudioHistoryEntry `json:"entries"`
}

// StudioHistoryImportResponse reports how many entries an import stored and
// which ids it refused because they had been deleted.
type StudioHistoryImportResponse struct {
	Status  string   `json:"status"`
	Count   int      `json:"count"`
	Skipped []string `json:"skipped"`
}
