package projects_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

const (
	leakPath = "/var/blobs/secret/abc"
	leakURL  = "s3://host/bucket/key"
)

// failingManifestWS makes the read of a project.json fail the way an object
// store does: an error that carries a local path and a bucket URL.
type failingManifestWS struct {
	workspace.Store
	onOpen, onRead bool
}

type brokenReader struct{ io.ReadSeekCloser }

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read " + leakURL + ": boom") }

func (w *failingManifestWS) Open(ctx context.Context, a workspace.Actor, p string) (io.ReadSeekCloser, workspace.Node, error) {
	if strings.HasSuffix(p, "/project.json") {
		if w.onOpen {
			return nil, workspace.Node{}, errors.New("open " + leakPath + ": permission denied")
		}
		if w.onRead {
			rc, n, err := w.Store.Open(ctx, a, p)
			if err != nil {
				return nil, n, err
			}
			return brokenReader{rc}, n, nil
		}
	}
	return w.Store.Open(ctx, a, p)
}

type failingHost struct {
	*files.Service
	st workspace.Store
}

func (h failingHost) Store() workspace.Store { return h.st }

var _ = Describe("projects shared with a person when the store fails", func() {
	ctx := context.Background()
	var (
		ws    *jsonstore.Store
		f     *files.Service
		fw    *failingManifestWS
		svc   *projects.Service
		plain *projects.Service
	)
	BeforeEach(func() {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err = jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		f = files.New(ws)
		inner, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		fw = &failingManifestWS{Store: f.Store()}
		svc = projects.NewService(projects.Options{Store: inner, Files: failingHost{Service: f, st: fw}})
		plain = projects.NewService(projects.Options{Store: inner, Files: f})
	})
	share := func(name string) projects.Project {
		p, err := plain.Create(ctx, "alice", projects.CreateInput{Name: name})
		Expect(err).NotTo(HaveOccurred())
		abs, err := f.Abs("alice", "/Projects/"+p.Slug)
		Expect(err).NotTo(HaveOccurred())
		Expect(ws.Grant(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "bob", abs, workspace.ModeRead)).To(Succeed())
		return p
	}

	DescribeTable("skips the project, and the node resolver gives a generic error without the text",
		func(setup func()) {
			p := share("Acme")
			share("Other")
			setup()
			list, err := svc.SharedWith(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			for _, s := range list {
				Expect(s.Status).NotTo(Equal(projects.StatusBroken))
				Expect(s.Error).NotTo(ContainSubstring("secret"))
				Expect(s.Name).NotTo(Equal("Acme"), "a project that cannot be read is skipped")
			}
			_, err = svc.ResolveSharedNode(ctx, "bob", p.Node)
			Expect(err).To(HaveOccurred())
			Expect(err).NotTo(MatchError(projects.ErrInvalid))
			Expect(err).NotTo(MatchError(projects.ErrNotFound))
			Expect(err.Error()).NotTo(Or(ContainSubstring(leakPath), ContainSubstring(leakURL), ContainSubstring("s3://")))
		},
		Entry("open fails", func() { fw.onOpen = true }),
		Entry("read fails", func() { fw.onRead = true }),
	)

	It("still lists a manifest with invalid JSON as broken, with the parse error and no path", func() {
		p := share("Acme")
		_, err := f.WriteHome(ctx, "alice", "Projects/"+p.Slug+"/project.json", "{not json", "t", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		list, err := svc.SharedWith(ctx, "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Status).To(Equal(projects.StatusBroken))
		Expect(list[0].Error).To(ContainSubstring("invalid"))
		Expect(list[0].Error).NotTo(ContainSubstring("/"))
		_, err = svc.ResolveSharedNode(ctx, "bob", p.Node)
		Expect(err).To(MatchError(projects.ErrInvalid))
	})
})
