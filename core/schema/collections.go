package schema

// CollectionList is the answer to a list of collections.
type CollectionList struct {
	Collections []string `json:"collections"`
	Count       int      `json:"count"`
	// UserGroups holds the collections of the other users. Only an administrator
	// who asks for all users gets it.
	UserGroups map[string]CollectionGroup `json:"user_groups,omitempty"`
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
