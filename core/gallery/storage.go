package gallery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/pkg/system"
)

// StorageFile is one referenced path under the models directory with the
// model configs referencing it. Missing marks a reference whose file is
// not on disk (a download that never finished, or removed by hand);
// len(Models) > 1 is what the UI renders as "shared".
type StorageFile struct {
	// Path is relative to the models directory.
	Path      string   `json:"path"`
	SizeBytes int64    `json:"size_bytes"`
	Missing   bool     `json:"missing,omitempty"`
	Models    []string `json:"models"`
}

// ModelStorage is the disk footprint of one model config. SharedBytes is
// the portion of SizeBytes in files other configs reference too: deleting
// this model frees SizeBytes minus SharedBytes. Missing lists referenced
// paths with no file behind them — surfaced as a warning in the UI.
type ModelStorage struct {
	Name        string   `json:"name"`
	SizeBytes   int64    `json:"size_bytes"`
	SharedBytes int64    `json:"shared_bytes"`
	Files       []string `json:"files"`
	Missing     []string `json:"missing,omitempty"`
}

// StorageIndex is the storage report for the models directory. TotalBytes
// deduplicates shared files, so it is the actual on-disk payload of the
// installed configs rather than the sum of the per-model sizes.
type StorageIndex struct {
	Models     []ModelStorage `json:"models"`
	Files      []StorageFile  `json:"files"`
	TotalBytes int64          `json:"total_bytes"`
	// Errors carries per-config failures (malformed YAML, path escapes),
	// reported rather than fatal so one broken config cannot blank the
	// whole report.
	Errors []string `json:"errors,omitempty"`
}

// BuildStorageIndex attributes every referenced file to the model configs
// referencing it, using the same resolution the delete path trusts
// (listModelFiles: the config's model/mmproj references plus the gallery
// sidecar's file list, including the sidecar itself). Model references
// are confined to the models path by construction — resolution always
// joins onto it — so the index is complete for installed configs.
func BuildStorageIndex(systemState *system.SystemState) (*StorageIndex, error) {
	base := systemState.Model.ModelsPath
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}

	type fileInfo struct {
		size    int64
		missing bool
		models  []string
	}
	files := map[string]*fileInfo{}
	index := &StorageIndex{Models: []ModelStorage{}, Files: []StorageFile{}}

	var modelNames []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "._gallery_") {
			continue
		}
		if !strings.HasSuffix(n, ".yaml") && !strings.HasSuffix(n, ".yml") {
			continue
		}
		modelNames = append(modelNames, strings.TrimSuffix(strings.TrimSuffix(n, ".yaml"), ".yml"))
	}
	sort.Strings(modelNames)

	for _, name := range modelNames {
		refs, err := listModelFiles(systemState, name)
		if err != nil {
			index.Errors = append(index.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		ms := ModelStorage{Name: name, Files: []string{}}
		seen := map[string]bool{}
		for _, f := range refs {
			if seen[f] {
				continue
			}
			seen[f] = true
			// listModelFiles names the ._gallery_ sidecar unconditionally
			// (deletion must remove it when present). An ABSENT sidecar is
			// the normal state of a hand-written config, not a broken
			// reference — only a present one counts as payload.
			if strings.HasPrefix(filepath.Base(f), "._gallery_") {
				if _, err := os.Stat(f); err != nil {
					continue
				}
			}
			fi, ok := files[f]
			if !ok {
				fi = &fileInfo{}
				if size, err := pathSize(f); err != nil {
					fi.missing = true
				} else {
					fi.size = size
				}
				files[f] = fi
			}
			fi.models = append(fi.models, name)
			rel, err := filepath.Rel(base, f)
			if err != nil {
				rel = f
			}
			if fi.missing {
				ms.Missing = append(ms.Missing, rel)
				continue
			}
			ms.Files = append(ms.Files, rel)
			ms.SizeBytes += fi.size
		}
		sort.Strings(ms.Files)
		sort.Strings(ms.Missing)
		index.Models = append(index.Models, ms)
	}

	// Shared bytes need the final refcounts, hence the second pass.
	for i := range index.Models {
		m := &index.Models[i]
		for _, rel := range m.Files {
			if fi := files[filepath.Join(base, rel)]; fi != nil && len(fi.models) > 1 {
				m.SharedBytes += fi.size
			}
		}
	}

	for p, fi := range files {
		rel, err := filepath.Rel(base, p)
		if err != nil {
			rel = p
		}
		sort.Strings(fi.models)
		index.Files = append(index.Files, StorageFile{
			Path: rel, SizeBytes: fi.size, Missing: fi.missing, Models: fi.models,
		})
		index.TotalBytes += fi.size
	}

	sort.Slice(index.Models, func(i, j int) bool {
		if index.Models[i].SizeBytes != index.Models[j].SizeBytes {
			return index.Models[i].SizeBytes > index.Models[j].SizeBytes
		}
		return index.Models[i].Name < index.Models[j].Name
	})
	sort.Slice(index.Files, func(i, j int) bool {
		if index.Files[i].SizeBytes != index.Files[j].SizeBytes {
			return index.Files[i].SizeBytes > index.Files[j].SizeBytes
		}
		return index.Files[i].Path < index.Files[j].Path
	})

	return index, nil
}

// pathSize sizes a referenced path: a plain stat for files, a walk for
// artifact snapshot directories (multi-file models resolve to a directory).
func pathSize(p string) (int64, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	if !st.IsDir() {
		return st.Size(), nil
	}
	var total int64
	err = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
