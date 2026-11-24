package importers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	"github.com/mudler/xlog"
)

// pirateFaceTimeout bounds the index lookups an import makes.
const pirateFaceTimeout = 30 * time.Second

// pirateFaceImportRef recognises the two ways a user names a Pirate Face model
// in the import form: a model page URL (https://pirateface.co/<owner>/<repo>)
// and the pirateface://<owner>/<repo>[/<file>] shorthand.
func pirateFaceImportRef(uri string) (downloader.PirateFaceRef, bool) {
	uri = strings.TrimSpace(uri)
	if ref, ok := downloader.PirateFaceRefFromPageURL(uri); ok {
		return ref, true
	}
	if downloader.URI(uri).LooksLikePirateFace() {
		if ref, err := downloader.ParsePirateFaceURI(uri); err == nil {
			return ref, true
		}
	}
	return downloader.PirateFaceRef{}, false
}

// hfMetadata fetches Hugging Face's view of a repository. A variable so tests
// run without network access.
var hfMetadata = func(repoID string) (*hfapi.ModelDetails, error) {
	return hfapi.NewClient().GetModelDetails(repoID)
}

// pirateFaceModelDetails lists a repository's files from the Pirate Face index
// in the shape the importers read. Each file's URL is a pirateface:// URI, so
// the generated config downloads it over BitTorrent. The index gives paths and
// SHA-256 digests but no sizes. The pipeline tag and library name, which the
// index does not carry, come from Hugging Face when it knows the repository.
func pirateFaceModelDetails(ref downloader.PirateFaceRef) (*hfapi.ModelDetails, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pirateFaceTimeout)
	defer cancel()
	files, err := downloader.PirateFaceFiles(ctx, ref.RepoID())
	if err != nil {
		return nil, fmt.Errorf("listing %s on Pirate Face: %w", ref.RepoID(), err)
	}
	details := &hfapi.ModelDetails{ModelID: ref.RepoID(), Author: ref.Owner}
	for _, f := range files {
		mf := hfapi.ModelFile{
			Path:     f.Path,
			SHA256:   f.SHA256,
			IsReadme: strings.Contains(strings.ToLower(f.Path), "readme"),
			URL:      string(downloader.PirateFaceRef{Owner: ref.Owner, Repo: ref.Repo, Path: f.Path}.URI()),
		}
		details.Files = append(details.Files, mf)
		if mf.IsReadme && details.ReadmeFile == nil {
			copied := mf
			details.ReadmeFile = &copied
		}
	}
	if hfMeta, err := hfMetadata(ref.RepoID()); err == nil {
		details.PipelineTag = hfMeta.PipelineTag
		details.LibraryName = hfMeta.LibraryName
	} else {
		xlog.Debug("no Hugging Face metadata for Pirate Face model", "repo", ref.RepoID(), "error", err)
	}
	return details, nil
}
