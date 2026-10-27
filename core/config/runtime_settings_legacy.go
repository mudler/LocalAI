package config

import (
	"encoding/json"
	"sync"

	"github.com/mudler/xlog"
)

// legacyKeyReplacement maps each old key to the key that replaces it.
var legacyKeyReplacement = map[string]string{
	"agent_pool_enabled":            "collections_enabled",
	"agent_pool_default_model":      "collections_default_model",
	"agent_pool_embedding_model":    "collections_embedding_model",
	"agent_pool_max_chunking_size":  "collections_max_chunking_size",
	"agent_pool_chunk_overlap":      "collections_chunk_overlap",
	"agent_pool_collection_db_path": "collections_db_path",
	"agent_pool_vector_engine":      "collections_vector_engine",
	"agent_pool_database_url":       "collections_database_url",
}

var (
	legacyKeyMu     sync.Mutex
	legacyKeyWarned = map[string]bool{}
)

// warnLegacyKeys logs one warning for each old key found in data. The file is
// read again each time it changes, so a key is named once per process.
func warnLegacyKeys(data []byte) {
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil {
		return
	}
	legacyKeyMu.Lock()
	defer legacyKeyMu.Unlock()
	for old, replacement := range legacyKeyReplacement {
		if _, ok := keys[old]; !ok || legacyKeyWarned[old] {
			continue
		}
		legacyKeyWarned[old] = true
		xlog.Warn("Deprecated runtime setting used", "old", old, "new", replacement,
			"message", "please switch to the new name; the old name will be removed in a future release")
	}
}

// legacyCollectionsKeys are the names the collections settings had while they
// shared the keys of the old agent pool. Files written by earlier versions
// still use them, so they stay readable. They are never written.
type legacyCollectionsKeys struct {
	Enabled          *bool   `json:"agent_pool_enabled"`
	DefaultModel     *string `json:"agent_pool_default_model"`
	EmbeddingModel   *string `json:"agent_pool_embedding_model"`
	MaxChunkingSize  *int    `json:"agent_pool_max_chunking_size"`
	ChunkOverlap     *int    `json:"agent_pool_chunk_overlap"`
	CollectionDBPath *string `json:"agent_pool_collection_db_path"`
	VectorEngine     *string `json:"agent_pool_vector_engine"`
	DatabaseURL      *string `json:"agent_pool_database_url"`
}

// UnmarshalJSON reads the settings, accepting the old agent_pool_* keys of the
// collections settings. When both an old and a new key are present, the new
// one wins.
func (s *RuntimeSettings) UnmarshalJSON(data []byte) error {
	type plain RuntimeSettings // no methods, so no recursion
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var l legacyCollectionsKeys
	if err := json.Unmarshal(data, &l); err != nil {
		return err
	}
	warnLegacyKeys(data)
	if p.CollectionsEnabled == nil {
		p.CollectionsEnabled = l.Enabled
	}
	if p.CollectionsDefaultModel == nil {
		p.CollectionsDefaultModel = l.DefaultModel
	}
	if p.CollectionsEmbeddingModel == nil {
		p.CollectionsEmbeddingModel = l.EmbeddingModel
	}
	if p.CollectionsMaxChunkingSize == nil {
		p.CollectionsMaxChunkingSize = l.MaxChunkingSize
	}
	if p.CollectionsChunkOverlap == nil {
		p.CollectionsChunkOverlap = l.ChunkOverlap
	}
	if p.CollectionsDBPath == nil {
		p.CollectionsDBPath = l.CollectionDBPath
	}
	if p.CollectionsVectorEngine == nil {
		p.CollectionsVectorEngine = l.VectorEngine
	}
	if p.CollectionsDatabaseURL == nil {
		p.CollectionsDatabaseURL = l.DatabaseURL
	}
	*s = RuntimeSettings(p)
	return nil
}
