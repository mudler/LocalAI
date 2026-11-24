package config

import (
	"encoding/json"
	"sync"

	"github.com/mudler/xlog"
)

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

// legacyKeyNames lists the old keys, in the order they are reported.
var legacyKeyNames = []string{
	"agent_pool_enabled", "agent_pool_default_model", "agent_pool_embedding_model", "agent_pool_max_chunking_size",
	"agent_pool_chunk_overlap", "agent_pool_collection_db_path", "agent_pool_vector_engine", "agent_pool_database_url",
}

// legacyKeysWarned makes the deprecation warning appear once per process, however
// many times the settings file is read.
var legacyKeysWarned = &sync.Once{}

// logLegacyKeys says which old keys a settings file still uses. A test replaces it.
var logLegacyKeys = func(keys []string) {
	xlog.Warn("Runtime settings: the agent_pool_* keys are deprecated and still read; rename them to collections_* the next time you save the settings", "keys", keys)
}

// warnLegacyKeys reports the old keys found in a settings document, once.
func warnLegacyKeys(data []byte) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	var found []string
	for _, k := range legacyKeyNames {
		if _, ok := raw[k]; ok {
			found = append(found, k)
		}
	}
	if len(found) > 0 {
		legacyKeysWarned.Do(func() { logLegacyKeys(found) })
	}
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
