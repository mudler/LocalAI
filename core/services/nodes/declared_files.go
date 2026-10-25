package nodes

import (
	"os"
	"path/filepath"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// declaredExtraFiles returns the files the model's install declared that the
// path fields of opts do not already stage: neither named by a field nor
// inside a directory a field names. It must run on the local paths, before
// staging rewrites the fields to remote ones.
func (r *SmartRouter) declaredExtraFiles(trackingKey string, opts *pb.ModelOptions) []string {
	if r.modelFiles == nil || opts == nil || trackingKey == "" {
		return nil
	}
	covered := append([]string{
		opts.ModelFile, opts.MMProj, opts.LoraAdapter, opts.DraftModel,
		opts.CLIPModel, opts.Tokenizer, opts.AudioPath, opts.LoraBase,
	}, opts.LoraAdapters...)

	seen := map[string]struct{}{}
	var extra []string
	for _, p := range r.modelFiles(trackingKey) {
		p = filepath.Clean(p)
		if _, dup := seen[p]; dup || coveredByField(p, covered) {
			continue
		}
		seen[p] = struct{}{}
		extra = append(extra, p)
	}
	return extra
}

func coveredByField(path string, fields []string) bool {
	for _, f := range fields {
		if f == "" {
			continue
		}
		f = filepath.Clean(f)
		if path == f || strings.HasPrefix(path, f+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// existingFiles drops declared files that are not on the frontend. An install
// can declare files that are gone by load time (an archive unpacked and then
// removed, say), so a missing one is not a reason to refuse the load; the
// backend reports it if it really needed it.
func existingFiles(paths []string, nodeName, trackingKey string) []string {
	out := paths[:0:0]
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			xlog.Warn("Skipping staging for declared model file that is not on the frontend", "path", p, "node", nodeName, "model", trackingKey, "error", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

// stagingPayloadBytes totals the on-disk size of everything staging uploads
// for a model: the path fields plus the declared files they do not cover. The
// first shard of a split GGUF can be a few MB of metadata while the weights
// sit in the others, so sizing the fields alone starves the load budget and
// the disk-headroom check.
func (r *SmartRouter) stagingPayloadBytes(trackingKey string, opts *pb.ModelOptions) int64 {
	total := modelPayloadBytes(opts)
	for _, p := range r.declaredExtraFiles(trackingKey, opts) {
		total += pathBytes(p)
	}
	return total
}
