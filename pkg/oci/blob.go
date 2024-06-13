package oci

import (
	"context"
	"fmt"
	"io"
	"os"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry/remote"
)

func FetchImageBlob(r, reference, dst string, statusReader func(ocispec.Descriptor) io.Writer) error {
	// 0. Create a file store for the output
	fs, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer fs.Close()

	// 1. Connect to a remote repository
	ctx := context.Background()
	repo, err := remote.NewRepository(r)
	if err != nil {
		return fmt.Errorf("failed to create repository: %v", err)
	}
	repo.SkipReferrersGC = true

	// https://github.com/oras-project/oras/blob/main/cmd/oras/internal/option/remote.go#L364
	// https://github.com/oras-project/oras/blob/main/cmd/oras/root/blob/fetch.go#L136
	desc, reader, err := oras.Fetch(ctx, repo.Blobs(), reference, oras.DefaultFetchOptions)
	if err != nil {
		return fmt.Errorf("failed to fetch image: %v", err)
	}

	if statusReader != nil {
		// 3. Write the file to the file store
		_, err = io.Copy(io.MultiWriter(fs, statusReader(desc)), reader)
		if err != nil {
			return err
		}
	} else {
		_, err = io.Copy(fs, reader)
		if err != nil {
			return err
		}
	}

	return nil
}
