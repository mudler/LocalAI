package application

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

// studioMaxFileSize limits one file of a workspace.
const studioMaxFileSize int64 = 256 << 20

// studioUploadLimit is the largest file an upload of the Files page can
// send: the workspace limit, or the operator's --upload-limit when that is
// set and lower. Without it the general 15 MB limit does not apply here.
func (a *Application) studioUploadLimit() int64 {
	limit := studioMaxFileSize
	if cfg := a.ApplicationConfig(); cfg.UploadLimitSet() && int64(cfg.UploadLimitMB)<<20 < limit {
		limit = int64(cfg.UploadLimitMB) << 20
	}
	return limit
}

// StudioFiles returns the files service of the chats. It is never nil: when
// files are off, it says why.
func (a *Application) StudioFiles() *files.Service {
	if a.studioFiles == nil {
		return files.Disabled("Files are not set up on this server.")
	}
	return a.studioFiles
}

// initStudioFiles builds the workspace of the chats.
//
// The choice of the index is explicit. With a database (the auth database: a
// SQLite file or PostgreSQL) the tree lives there, in the GORM store. With no
// database it is a folder under the data path with a JSON index, in the JSON
// store; a build without cgo and without any build tag has it too. The bytes
// live in the object store of a cluster, and otherwise in a folder under the
// data path. With no data path there is nowhere to keep files, so they are
// off. Moving files from one index to the other is not supported.
func (a *Application) initStudioFiles() {
	dp := a.applicationConfig.DataPath
	dist := a.Distributed()
	if reason := clusterFilesBlocker(a.applicationConfig, dist != nil && dist.Store != nil); reason != "" {
		a.studioFiles = files.Disabled(reason)
		return
	}
	if dp == "" && (a.authDB == nil || dist == nil || dist.Store == nil) {
		a.studioFiles = files.Disabled("There is no data path, so files are off. Start LocalAI with a data path to keep files.")
		return
	}
	var tmpCache string
	if dp == "" { // a cluster without a data path: the blob cache is a temporary folder
		dir, err := os.MkdirTemp("", "localai-files-*")
		if err != nil {
			a.studioFiles = files.Disabled("Files are off: there is no folder for their cache.")
			return
		}
		a.studioFilesTemp, tmpCache = dir, dir
	}
	root := filepath.Join(dp, "chats")
	if dp == "" {
		root = tmpCache
	}

	var blobs blob.Store
	var err error
	if dist != nil && dist.Store != nil {
		blobs = blob.NewObjectStore(dist.Store, filepath.Join(root, "blobcache"))
	} else {
		blobs, err = blob.NewFS(filepath.Join(root, "blobs"))
	}
	if err != nil {
		xlog.Warn("Files disabled: cannot open the blob store", "error", err)
		a.studioFiles = files.Disabled("Files are off: their storage could not be opened.")
		return
	}
	ws, closer, err := a.openWorkspaceIndex(root, blobs)
	if err != nil {
		xlog.Warn("Files disabled: cannot start the workspace", "error", err)
		a.studioFiles = files.Disabled("Files are off: the workspace could not be started.")
		return
	}
	f := files.New(ws, files.WithBlobs(blobs), files.WithMaxFileSize(a.studioUploadLimit()))
	if err := f.EnsureShared(context.Background()); err != nil {
		xlog.Warn("Files disabled: cannot create the shared folders", "error", err)
		a.studioFiles = files.Disabled("Files are off: the workspace could not be started.")
		if closer != nil {
			_ = closer.Close()
		}
		return
	}
	// The office skills ship with the server. A copy that fails leaves the
	// chats as they are: the skills are help, not a need.
	if err := f.SyncBuiltin(context.Background(), builtinskills.FS()); err != nil {
		xlog.Warn("Could not write the built-in skills", "error", err)
	}
	a.studioFiles, a.studioFilesIndex = f, closer
	a.initStudioTextIndex(f, root)
	a.syncAddonSkills(f)
}

// initStudioTextIndex builds the text index of the documents in the files and
// starts its indexer. The text lives with the workspace index: in the auth
// database when there is one, otherwise in a folder under root, never among
// the users' files. In a cluster each pass of the indexer runs under an
// advisory lock, so one frontend reads the files and every frontend searches
// the shared text. A chat worker searches it but never runs the indexer.
func (a *Application) initStudioTextIndex(f *files.Service, root string) {
	var st textindex.Store
	var err error
	if a.authDB != nil {
		st, err = textindex.NewGORM(a.authDB)
	} else {
		st, err = textindex.NewJSON(filepath.Join(root, "textindex"))
	}
	if err != nil {
		xlog.Warn("File text index disabled: cannot open its store; documents are found by name only", "error", err)
		return
	}
	cfg := a.applicationConfig
	ix := textindex.New(textindex.Options{
		Store: st, Workspace: f.Store(), Admin: workspace.Actor{Principal: files.AdminPrincipal}, DB: a.authDB,
		Enabled:      func() bool { return !cfg.DisableChatsIndexFiles },
		MaxTextBytes: cfg.EffectiveChatsIndexMaxTextBytes,
	})
	f.SetTextIndex(ix)
	a.studioTextIndex = ix
	if cfg.Distributed.ChatWorker {
		return
	}
	go ix.Run(a.backgroundContext())
}

// syncAddonSkills lets the add-on owner keep the add-on skill layer in step
// with the installed packages: it copies the skills of every installed add-on
// now and after each install, upgrade and uninstall, and removes the folders of
// add-ons that are gone. It does nothing on a frontend: only the owner holds
// the verified packages, and the layer is in the workspace store every
// frontend reads.
func (a *Application) syncAddonSkills(f *files.Service) {
	m := a.addonManager
	if m == nil {
		return
	}
	ctx := a.applicationConfig.Context
	m.SetSkillSync(ctx, addonSkillSync(f))
	installed, err := m.InstalledSkillIDs(ctx)
	if err != nil {
		return // not the owner
	}
	present, err := f.AddonSkillIDs(ctx)
	if err != nil {
		xlog.Warn("Could not list the add-on skills", "error", err)
		return
	}
	for _, id := range present {
		if !slices.Contains(installed, id) {
			if err := f.RemoveAddonSkills(ctx, id); err != nil {
				xlog.Warn("Could not remove the skills of an add-on", "addon", id, "error", err)
			}
		}
	}
}

// addonSkillSync copies the skills of a package into the add-on layer, each
// file checked against the receipt, or removes them.
func addonSkillSync(f *files.Service) addons.SkillSync {
	return func(ctx context.Context, id string, r *packages.PackageReceipt) error {
		if r == nil || len(r.Skills) == 0 {
			return f.RemoveAddonSkills(ctx, id)
		}
		fsys, err := packages.SkillFS(*r)
		if err != nil {
			return err
		}
		return f.SyncAddonSkills(ctx, id, fsys)
	}
}

// clusterFilesBlocker returns why files cannot work in this cluster, or "" when
// they can. The bytes of a file go to the cluster's object store so that every
// frontend reads what another wrote. Without S3 the object store is a folder
// on the frontend itself, and each frontend would keep its own copy of every
// file, so files are off instead of silently wrong.
func clusterFilesBlocker(cfg *config.ApplicationConfig, clustered bool) string {
	if clustered && cfg.Distributed.StorageURL == "" {
		return "Files are off: in a cluster they need S3 object storage (the storage URL setting), because without it each frontend would keep its own copy of every file."
	}
	return ""
}

// openWorkspaceIndex opens the index of the workspace: the GORM store on the
// auth database when there is one, and the JSON store in a folder under root
// otherwise. The returned closer is nil for the shared database, which the
// application closes itself.
func (a *Application) openWorkspaceIndex(root string, blobs blob.Store) (workspace.Store, io.Closer, error) {
	if a.authDB != nil {
		ws, err := gormstore.New(a.authDB, blobs, gormstore.Options{Admin: files.AdminPrincipal, MaxFileSize: studioMaxFileSize})
		return ws, nil, err
	}
	ws, err := jsonstore.New(filepath.Join(root, "workspace"), blobs, jsonstore.Options{Admin: files.AdminPrincipal, MaxFileSize: studioMaxFileSize})
	if err != nil {
		return nil, nil, err
	}
	return ws, ws, nil
}
