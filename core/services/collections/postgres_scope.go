// SPDX-License-Identifier: MIT
package collections

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/localrecall/rag/engine"
	"github.com/mudler/xlog"
)

var ErrLegacyCollectionActionRequired = errors.New("legacy collection requires explicit operator ownership resolution; no automatic migration")

type LegacyStatus struct {
	ActionRequired bool   `json:"action_required"`
	Message        string `json:"message,omitempty"`
}

func (s *Service) scopeConfig(cfg *lagi.Config, user string) {
	if cfg.VectorEngine != "postgres" {
		return
	}
	// JSON encoding separates deployment and principal without delimiter aliases.
	identity, _ := json.Marshal([]string{"localai.collections.v1", cmp.Or(s.appConfig.Collections.PostgresNamespace, "default"), user})
	cfg.PostgresScope = engine.NewPostgresScope(string(identity))
	cfg.LegacyCollectionDBPath = cfg.CollectionDBPath
	cfg.CollectionDBPath = filepath.Join(cfg.CollectionDBPath, "scoped-v1", cfg.PostgresScope.Schema)
	cfg.FileAssets = filepath.Join(cfg.FileAssets, "scoped-v1", cfg.PostgresScope.Schema)
}
func (s *Service) admitLegacy(cfg *lagi.Config, name string) error {
	if name == "" || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return errors.New("invalid collection name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), collectionRegistryTimeout)
	defer cancel()
	exists, err := engine.PostgresCollectionExists(ctx, cfg.DatabaseURL, name)
	if err != nil {
		return err
	}
	if !exists {
		_, err = os.Stat(filepath.Join(cfg.LegacyCollectionDBPath, "collection-"+name+".json"))
		exists = err == nil
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if exists {
		return fmt.Errorf("%w: %s", ErrLegacyCollectionActionRequired, name)
	}
	return nil
}
func (s *Service) LegacyStatusForUser(user string) (LegacyStatus, error) {
	if err := ValidateUserID(user); err != nil {
		return LegacyStatus{}, err
	}
	if s.appConfig.Collections.VectorEngine != "postgres" {
		return LegacyStatus{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), collectionRegistryTimeout)
	defer cancel()
	names, err := engine.ListPostgresCollections(ctx, s.appConfig.Collections.DatabaseURL)
	if err != nil {
		return LegacyStatus{}, err
	}
	dir := s.storage.CollectionsDir(user)
	if user == "" {
		dir = cmp.Or(s.appConfig.Collections.CollectionDBPath, filepath.Join(s.stateDir, "collections"))
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return LegacyStatus{}, err
	}
	affected := len(names) > 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "collection-") && strings.HasSuffix(entry.Name(), ".json") {
			affected = true
		}
	}
	if affected {
		xlog.Warn("Unresolved legacy PostgreSQL collections require explicit operator ownership resolution; scoped collections remain available")
		return LegacyStatus{ActionRequired: true, Message: ErrLegacyCollectionActionRequired.Error()}, nil
	}
	return LegacyStatus{}, nil
}

// CheckForUser checks only the requested destination, not unrelated legacy data.
func (s *Service) CheckForUser(ctx context.Context, user, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := s.Backend(user)
	if err != nil {
		return err
	}
	if shared, ok := b.(*sharedCollections); ok {
		return shared.ensure(name)
	}
	names, err := b.ListCollections()
	if err != nil {
		return err
	}
	for _, n := range names {
		if n == name {
			return nil
		}
	}
	return fmt.Errorf("collection not found: %s", name)
}
