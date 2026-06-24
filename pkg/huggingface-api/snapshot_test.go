package hfapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/downloader"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
)

var _ = Describe("immutable snapshot resolution", func() {
	It("pins the metadata sha and follows tree pagination", func() {
		const commit = "0123456789abcdef0123456789abcdef01234567"
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
			switch {
			case strings.Contains(r.URL.Path, "/revision/main"):
				_, _ = w.Write([]byte(`{"sha":"` + commit + `"}`))
			case strings.Contains(r.URL.Path, "/tree/"+commit) && r.URL.Query().Get("cursor") == "page-2":
				_, _ = w.Write([]byte(`[{"type":"file","path":"tokenizer/tokenizer.json","size":7,"oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`))
			case strings.Contains(r.URL.Path, "/tree/"+commit):
				Expect(r.URL.Query().Get("recursive")).To(Equal("true"))
				w.Header().Set("Link", fmt.Sprintf(`<%s%s?recursive=true&expand=true&cursor=page-2>; rel="next"`, server.URL, r.URL.Path))
				_, _ = w.Write([]byte(`[{"type":"file","path":"model.safetensors","size":11,"oid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","lfs":{"oid":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","size":11,"pointerSize":130}}]`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		DeferCleanup(server.Close)

		client := hfapi.NewClient()
		client.SetBaseURL(server.URL + "/api/models")
		snapshot, err := client.ResolveSnapshot(context.Background(), hfapi.SnapshotRequest{
			Repo:     "owner/repo",
			Revision: "main",
			Token:    "test-token",
			MaxFiles: 10,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Endpoint).To(Equal(server.URL))
		Expect(snapshot.ResolvedRevision).To(Equal(commit))
		Expect(snapshot.Files).To(HaveLen(2))
		Expect(snapshot.Files[0].Path).To(Equal("model.safetensors"))
		Expect(snapshot.Files[0].LFSOID).To(HaveLen(64))
		Expect(snapshot.Files[1].Path).To(Equal("tokenizer/tokenizer.json"))
		Expect(snapshot.Files[0].URL).To(ContainSubstring("/resolve/" + commit + "/model.safetensors"))
	})

	It("applies deterministic allow and ignore filters", func() {
		files := []hfapi.SnapshotFile{
			{Path: "config.json"},
			{Path: "nested/tokenizer.json"},
			{Path: "weights.bin"},
			{Path: "weights.safetensors"},
		}
		filtered, err := hfapi.FilterSnapshotFiles(files, []string{"*.json", "*.safetensors"}, []string{"nested/*"})
		Expect(err).NotTo(HaveOccurred())
		Expect(filtered).To(ConsistOf(
			hfapi.SnapshotFile{Path: "config.json"},
			hfapi.SnapshotFile{Path: "weights.safetensors"},
		))
	})

	It("rejects a manifest larger than MaxFiles", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/revision/") {
				_, _ = w.Write([]byte(`{"sha":"0123456789abcdef0123456789abcdef01234567"}`))
				return
			}
			_, _ = w.Write([]byte(`[{"type":"file","path":"a","size":1},{"type":"file","path":"b","size":1}]`))
		}))
		DeferCleanup(server.Close)
		client := hfapi.NewClient()
		client.SetBaseURL(server.URL + "/api/models")
		_, err := client.ResolveSnapshot(context.Background(), hfapi.SnapshotRequest{Repo: "owner/repo", Revision: "main", MaxFiles: 1})
		Expect(err).To(MatchError(ContainSubstring("maximum file count")))
	})

	It("honors a cancelled context before metadata access", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := hfapi.NewClient().ResolveSnapshot(ctx, hfapi.SnapshotRequest{Repo: "owner/repo", Revision: "main"})
		Expect(err).To(MatchError(context.Canceled))
	})

	It("rejects a cross-origin pagination link before sending credentials", func() {
		const commit = "0123456789abcdef0123456789abcdef01234567"
		requestedCrossOrigin := false
		evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requestedCrossOrigin = true
			w.WriteHeader(http.StatusNoContent)
		}))
		DeferCleanup(evil.Close)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/revision/") {
				_, _ = w.Write([]byte(`{"sha":"` + commit + `"}`))
				return
			}
			w.Header().Set("Link", `<`+evil.URL+`/steal>; rel="next"`)
			_, _ = w.Write([]byte(`[]`))
		}))
		DeferCleanup(origin.Close)
		client := hfapi.NewClient()
		client.SetBaseURL(origin.URL + "/api/models")
		_, err := client.ResolveSnapshot(context.Background(), hfapi.SnapshotRequest{
			Repo: "owner/repo", Revision: "main", Token: "test-token",
		})
		Expect(err).To(MatchError(ContainSubstring("cross-origin pagination")))
		Expect(requestedCrossOrigin).To(BeFalse())
	})

	It("downloads a pinned public Xet fixture through the ordinary resolve URL", Label("network"), func() {
		if os.Getenv("LOCALAI_HF_XET_SMOKE") != "1" {
			Skip("set LOCALAI_HF_XET_SMOKE=1 to run the public Hub compatibility gate")
		}
		client := hfapi.NewClient()
		snapshot, err := client.ResolveSnapshot(context.Background(), hfapi.SnapshotRequest{
			Repo:          "hf-internal-testing/tiny-random-t5-v1.1",
			Revision:      "main",
			AllowPatterns: []string{"model.safetensors"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.ResolvedRevision).To(MatchRegexp(`^[0-9a-f]{40}$`))
		Expect(snapshot.Files).To(HaveLen(1))
		file := snapshot.Files[0]
		Expect(file.Path).To(Equal("model.safetensors"))
		Expect(file.XetHash).NotTo(BeEmpty())
		target := filepath.Join(GinkgoT().TempDir(), file.Path)
		var final downloader.TransferProgress
		err = downloader.URI(file.URL).DownloadFileWithContext(
			context.Background(), target, file.LFSOID, 0, 1, nil,
			downloader.WithTransferProgress(func(event downloader.TransferProgress) { final = event }),
		)
		Expect(err).NotTo(HaveOccurred())
		info, err := os.Stat(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Size()).To(Equal(file.Size))
		Expect(final.Written).To(Equal(file.Size))
	})
})
