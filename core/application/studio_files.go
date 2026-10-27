package application

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

// studioMaxFileSize limits one file of a workspace.
const studioMaxFileSize int64 = 256 << 20

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
	f := files.New(ws)
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
