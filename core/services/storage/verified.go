package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/atomicfile"
	"github.com/mudler/xlog"
)

// verifiedDir holds, beside the cached files, the sha256 last computed for
// each one with the size and modification time it had, so a cached copy is
// hashed once rather than at every load. It lives apart from the cached files
// so a directory model never sees the records among its own files.
const verifiedDir = ".sha256"

// SHA256MismatchText is in the error of a download whose object does not have
// the expected sha256. The error crosses the control channel as text, so the
// sender of the object finds it with strings.Contains and replaces a stale
// object.
const SHA256MismatchText = "does not have the expected sha256"

// DownloadVerified is Download for a file whose sha256 the caller knows. A
// cached copy with another sha256 (a model upgrade changed the file under the
// same key while this node did not run it) is dropped and downloaded again,
// and the new copy is checked too. With an empty sha256 it is Download.
func (fm *FileManager) DownloadVerified(ctx context.Context, key, sha string) (string, error) {
	if sha == "" {
		return fm.Download(ctx, key)
	}
	localPath, err := fm.cachePath(key)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(localPath); err == nil {
		ok, err := fm.cachedMatches(key, localPath, sha)
		if err != nil {
			return "", err
		}
		if ok {
			return localPath, nil
		}
		xlog.Info("Cached file has another sha256 than expected; downloading it again", "key", key)
		fm.evictVerified(key, localPath)
	}
	localPath, err = fm.Download(ctx, key)
	if err != nil {
		return "", err
	}
	ok, err := fm.cachedMatches(key, localPath, sha)
	if err != nil {
		return "", err
	}
	if !ok {
		fm.evictVerified(key, localPath)
		return "", fmt.Errorf("the object %s "+SHA256MismatchText+" %s", key, sha)
	}
	return localPath, nil
}

// RecordCacheSHA256 records the sha256 of a file already in the cache under
// key (a file a worker downloaded and verified for itself), so the next
// DownloadVerified does not hash it again.
func RecordCacheSHA256(cacheDir, key, sha string) error {
	fm := &FileManager{cacheDir: cacheDir}
	localPath, err := fm.cachePath(key)
	if err != nil {
		return err
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}
	return fm.writeRecord(key, info, strings.ToLower(sha))
}

func (fm *FileManager) recordPath(key string) (string, error) {
	return fm.cachePath(verifiedDir + "/" + key)
}

// cachedMatches reports whether the cached file has the sha256 sha. It trusts
// the record when the file still has the size and modification time recorded
// with it, and hashes the file (and records the result) otherwise.
func (fm *FileManager) cachedMatches(key, localPath, sha string) (bool, error) {
	sha = strings.ToLower(sha)
	info, err := os.Stat(localPath)
	if err != nil {
		return false, err
	}
	if rec, err := fm.recordPath(key); err == nil {
		// #nosec G304 -- rec is the record path of recordPath, which cachePath
		// confines to the cache directory (a key that escapes it is an error).
		if data, err := os.ReadFile(rec); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) == 3 && fields[1] == strconv.FormatInt(info.Size(), 10) && fields[2] == strconv.FormatInt(info.ModTime().UnixNano(), 10) {
				return fields[0] == sha, nil
			}
		}
	}
	sum, err := fileSHA256(localPath)
	if err != nil {
		return false, err
	}
	if err := fm.writeRecord(key, info, sum); err != nil {
		xlog.Warn("Could not record the sha256 of a cached file", "key", key, "error", err)
	}
	return sum == sha, nil
}

// fileSHA256 returns the hex sha256 of the file at path. It is here, not a call
// to pkg/downloader, because that package needs the BitTorrent build tags when
// cgo is on, and every program that imports this one would need them too (the
// add-on fixtures are built without them).
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- path is a cached file the caller found under the cache directory
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (fm *FileManager) writeRecord(key string, info os.FileInfo, sum string) error {
	rec, err := fm.recordPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rec), 0750); err != nil {
		return err
	}
	line := sum + " " + strconv.FormatInt(info.Size(), 10) + " " + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "\n"
	return atomicfile.Write(rec, []byte(line), 0600)
}

func (fm *FileManager) evictVerified(key, localPath string) {
	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		xlog.Warn("Could not drop a cached file", "key", key, "error", err)
	}
	if rec, err := fm.recordPath(key); err == nil {
		_ = os.Remove(rec)
	}
}
