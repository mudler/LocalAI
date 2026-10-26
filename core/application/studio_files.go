package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
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
// The tree lives in the auth database when there is one, and otherwise in a
// private SQLite file under the data path. The bytes live in the object store
// of a cluster, and otherwise in a folder under the data path. With no data
// path there is nowhere to keep files, so they are off.
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

	db := a.authDB
	if db == nil {
		var err error
		if err = os.MkdirAll(root, 0o700); err == nil {
			db, err = auth.OpenSQLiteFile(filepath.Join(root, "workspace.db"))
		}
		if err != nil {
			xlog.Warn("Files disabled: cannot open the workspace database", "error", err)
			a.studioFiles = files.Disabled("Files are off: the workspace database could not be opened.")
			return
		}
		a.studioFilesDB = db
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
		a.closeStudioFilesDB(db)
		return
	}
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal, MaxFileSize: studioMaxFileSize})
	if err != nil {
		xlog.Warn("Files disabled: cannot start the workspace", "error", err)
		a.studioFiles = files.Disabled("Files are off: the workspace could not be started.")
		a.closeStudioFilesDB(db)
		return
	}
	f := files.New(ws)
	if err := f.EnsureShared(context.Background()); err != nil {
		xlog.Warn("Files disabled: cannot create the shared folders", "error", err)
		a.studioFiles = files.Disabled("Files are off: the workspace could not be started.")
		a.closeStudioFilesDB(db)
		return
	}
	a.studioFiles = f
}

// closeStudioFilesDB closes the private database of the workspace, never the
// shared auth database.
func (a *Application) closeStudioFilesDB(db *gorm.DB) {
	if db == nil || db != a.studioFilesDB {
		return
	}
	if raw, err := db.DB(); err == nil {
		_ = raw.Close()
	}
	a.studioFilesDB = nil
}
