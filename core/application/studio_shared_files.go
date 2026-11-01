// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/storage"
)

// workspaceObjects resolves workspace bytes only; model/input staging is unchanged.
// The constructor is local to this call so tests need neither network nor global hooks.
func (a *Application) workspaceObjects(newS3 func(context.Context, storage.S3Config) (storage.ObjectStore, error)) (storage.ObjectStore, error) {
	cfg := a.applicationConfig.Distributed
	if cfg.StorageURL == "" {
		return nil, nil
	}
	if cfg.StorageBucket == "" {
		return nil, fmt.Errorf("workspace S3 storage bucket must be set when storage URL is configured")
	}
	if (cfg.StorageAccessKey == "") != (cfg.StorageSecretKey == "") {
		return nil, fmt.Errorf("workspace S3 requires both access key and secret key, or neither for the SDK credential chain")
	}
	if dist := a.Distributed(); dist != nil && dist.Store != nil {
		return dist.Store, nil
	}
	store, err := newS3(a.backgroundContext(), storage.S3Config{
		Endpoint: cfg.StorageURL, Region: cfg.StorageRegion, Bucket: cfg.StorageBucket,
		AccessKeyID: cfg.StorageAccessKey, SecretAccessKey: cfg.StorageSecretKey, ForcePathStyle: true,
	})
	if err != nil {
		return nil, fmt.Errorf("initializing workspace S3 storage: %w", err)
	}
	return store, nil
}
