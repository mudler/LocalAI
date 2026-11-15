package downloader_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	. "github.com/onsi/gomega"
)

// testTorrent is a small multi-file torrent built on disk, so a fake web seed
// and a fake peer can serve it without any network access.
type testTorrent struct {
	Root     string // directory that holds the torrent's top-level folder
	Name     string // the top-level folder
	Files    map[string][]byte
	Meta     []byte // the .torrent file
	InfoHash string
}

const testPieceLength = 32 << 10

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	Expect(err).ToNot(HaveOccurred())
	return b
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// newTestTorrent writes files below <root>/<name> and builds the metainfo.
func newTestTorrent(root, name string, files map[string][]byte, webseed string) *testTorrent {
	top := filepath.Join(root, name)
	for p, data := range files {
		full := filepath.Join(top, filepath.FromSlash(p))
		Expect(os.MkdirAll(filepath.Dir(full), 0o750)).To(Succeed())
		Expect(os.WriteFile(full, data, 0o600)).To(Succeed())
	}
	info := metainfo.Info{PieceLength: testPieceLength}
	Expect(info.BuildFromFilePath(top)).To(Succeed())
	infoBytes, err := bencode.Marshal(info)
	Expect(err).ToNot(HaveOccurred())
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	if webseed != "" {
		mi.UrlList = []string{webseed}
	}
	var sb strings.Builder
	Expect(mi.Write(&sb)).To(Succeed())
	return &testTorrent{
		Root: root, Name: name, Files: files, Meta: []byte(sb.String()),
		InfoHash: mi.HashInfoBytes().HexString(),
	}
}

// webseedServer serves a torrent's files the way a BEP 19 web seed does:
// <base>/<name>/<path>, with Range support. It counts the requests it gets.
func webseedServer(t *testTorrent, hits *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		rel := strings.TrimPrefix(r.URL.Path, "/"+t.Name+"/")
		data, ok := t.Files[rel]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, rel, timeZero, bytesReader(data))
	}))
}

var timeZero = time.Time{}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
