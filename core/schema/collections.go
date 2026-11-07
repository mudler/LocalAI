package schema

// CollectionList is the answer to a list of collections.
type CollectionList struct {
	Collections []string `json:"collections"`
	Count       int      `json:"count"`
	// EmbeddingModel is the model that indexes and searches the collections.
	// EmbeddingModelInstalled is false when this server does not have it:
	// adding files and searching then fail until it is installed.
	EmbeddingModel          string `json:"embedding_model"`
	EmbeddingModelInstalled bool   `json:"embedding_model_installed"`
	// SearchMode is how a search finds passages: "semantic" (by meaning only)
	// or "hybrid" (by meaning and by keyword).
	SearchMode string `json:"search_mode"`
	// MaxChunkSize and ChunkOverlap are the chunking of a text, in characters.
	// A text is cut into passages of at most MaxChunkSize characters, and each
	// passage is embedded once.
	MaxChunkSize int `json:"max_chunk_size"`
	ChunkOverlap int `json:"chunk_overlap"`
	// UserGroups holds the collections of the other users. Only an administrator
	// who asks for all users gets it.
	UserGroups map[string]CollectionGroup `json:"user_groups,omitempty"`
	// Shared are the collections of other people shared with the caller.
	Shared []SharedCollection `json:"shared"`
	// Shares are the shares of the caller's own collections, by name, for
	// those that were made shareable.
	Shares map[string]CollectionShare `json:"shares"`
	// Sharing is false when this server cannot share collections.
	Sharing bool `json:"sharing"`
}

// SharedCollection is a collection of another person shared with the caller.
type SharedCollection struct {
	// Name is the name to use in the other calls: the collection's own name,
	// or shared:<id> when that name is taken.
	Name string `json:"name"`
	// Title is the collection's own name.
	Title string `json:"title"`
	// Owner is the display name of the person who shared it.
	Owner string `json:"owner"`
	// Role is viewer (search and read) or editor (also add and remove entries).
	Role string `json:"role"`
	// Everyone says that the role comes from the grant to every account.
	Everyone bool `json:"everyone,omitempty"`
}

// CollectionShare is the share of one of the caller's own collections.
type CollectionShare struct {
	// ResourceID names the collection in /api/shares/collection.
	ResourceID string `json:"resource_id"`
	// SharedWith counts the grants, the one to everyone included.
	SharedWith int `json:"shared_with"`
}

// CollectionGroup is the collections of one user.
type CollectionGroup struct {
	Collections []string `json:"collections"`
}

// CollectionCreateRequest makes a collection.
type CollectionCreateRequest struct {
	Name string `json:"name"`
}

// CollectionStatus is the answer to a collection change.
type CollectionStatus struct {
	Status   string `json:"status"`
	Name     string `json:"name,omitempty"`
	Filename string `json:"filename,omitempty"`
	// Key names the new entry.
	Key string `json:"key,omitempty"`
}

// CollectionEntries lists the entries of a collection.
type CollectionEntries struct {
	Entries []string `json:"entries"`
	Count   int      `json:"count"`
}

// CollectionEntryContent is the text of one entry.
type CollectionEntryContent struct {
	Content    string `json:"content"`
	ChunkCount int    `json:"chunk_count"`
}

// CollectionSearchRequest searches one collection.
type CollectionSearchRequest struct {
	Query string `json:"query"`
	// MaxResults is how many passages to return. A value below 1 picks a
	// default, and a value above the number of stored passages returns them all.
	MaxResults int `json:"max_results"`
}

// CollectionHit is one passage a search found.
type CollectionHit struct {
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	ID         string            `json:"id,omitempty"`
	Similarity float32           `json:"similarity,omitempty"`
}

// CollectionSearchResponse is the answer to a search.
type CollectionSearchResponse struct {
	Results []CollectionHit `json:"results"`
	Count   int             `json:"count"`
}

// CollectionEntryDeleteRequest names the entry to delete.
type CollectionEntryDeleteRequest struct {
	Entry string `json:"entry"`
}

// CollectionEntryDeleteResponse lists the entries left.
type CollectionEntryDeleteResponse struct {
	RemainingEntries []string `json:"remaining_entries"`
	Count            int      `json:"count"`
}

// CollectionSourceRequest adds a URL a collection follows, or names one to remove.
type CollectionSourceRequest struct {
	URL string `json:"url"`
	// UpdateInterval is the minutes between two fetches. Default 60.
	UpdateInterval int `json:"update_interval,omitempty"`
}

// CollectionSource is one URL a collection follows.
type CollectionSource struct {
	URL            string `json:"url"`
	UpdateInterval int    `json:"update_interval"`
	LastUpdate     string `json:"last_update"`
}

// CollectionSources lists the URLs a collection follows.
type CollectionSources struct {
	Sources []CollectionSource `json:"sources"`
	Count   int                `json:"count"`
}
