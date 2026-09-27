package nodes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/mudler/xlog"
)

// ggufSplitRe matches llama.cpp's split naming ("<prefix>-00001-of-00004.gguf",
// see llama_split_path). The shard number and count share the same width.
var ggufSplitRe = regexp.MustCompile(`^(.+)-(\d{5})-of-(\d{5})\.gguf$`)

// ggufSplitShards returns every shard of the split GGUF that path belongs to,
// in order and including path itself, or nil when path is not part of a split.
//
// A model config names only the first shard; llama.cpp then opens the others
// from the same directory by name. Anything that moves or sizes a model file
// by its configured path alone therefore sees a fraction of the weights.
func ggufSplitShards(path string) []string {
	dir, name := filepath.Split(path)
	m := ggufSplitRe.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	total, err := strconv.Atoi(m[3])
	if err != nil || total < 2 {
		return nil
	}
	shards := make([]string, 0, total)
	for i := 1; i <= total; i++ {
		shards = append(shards, filepath.Join(dir, fmt.Sprintf("%s-%0*d-of-%s.gguf", m[1], len(m[3]), i, m[3])))
	}
	return shards
}

// stageSplitShards uploads the shards of a split GGUF other than localPath,
// which the caller has already staged. Keys come from the same mapper as the
// first shard, so all shards land in one remote directory where llama.cpp
// expects them. A missing shard is an error: the worker cannot load a split
// with a gap, and failing here names the file instead of surfacing llama.cpp's
// "failed to load GGUF split" after the rest of the upload.
func (r *SmartRouter) stageSplitShards(ctx context.Context, node *BackendNode, trackingKey, localPath string, keyFn func(string) string, fileIdx *int, totalFiles int) error {
	for _, shard := range ggufSplitShards(localPath) {
		if shard == localPath {
			continue
		}
		if _, err := os.Stat(shard); err != nil {
			return fmt.Errorf("split GGUF shard %s: %w", shard, err)
		}
		*fileIdx++
		fileName := filepath.Base(shard)
		stageCtx := r.withStagingCallback(ctx, trackingKey, fileName, *fileIdx, totalFiles)
		xlog.Info("Staging split GGUF shard", "model", trackingKey, "node", node.Name, "file", fileName, "fileIndex", *fileIdx, "totalFiles", totalFiles)
		if _, err := r.fileStager.EnsureRemote(stageCtx, node.ID, shard, keyFn(shard)); err != nil {
			return fmt.Errorf("split GGUF shard %s: %w", shard, err)
		}
		r.stagingTracker.FileComplete(trackingKey, *fileIdx, totalFiles)
	}
	return nil
}
