package config

import "encoding/json"

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
