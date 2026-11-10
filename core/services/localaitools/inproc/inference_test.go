package inproc

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	"gorm.io/gorm"
)

var _ = Describe("inproc.Client client tools", func() {
	It("chooses the first enabled installed model with the use case, by name", func() {
		dir := GinkgoT().TempDir()
		write := func(name, body string) {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
		}
		write("zeta", "name: zeta\nbackend: stablediffusion\nknown_usecases: [image]\n")
		write("beta", "name: beta\nbackend: stablediffusion\nknown_usecases: [image]\ndisabled: true\n")
		write("gamma", "name: gamma\nbackend: stablediffusion\nknown_usecases: [image]\n")
		write("voice", "name: voice\nbackend: piper\nknown_usecases: [tts]\n")
		cl := config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())

		c := &Client{ConfigLoader: cl}
		m, err := c.FirstModel(context.Background(), config.FLAG_IMAGE)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal("gamma"))
		m, _ = c.FirstModel(context.Background(), config.FLAG_TTS)
		Expect(m).To(Equal("voice"))
		m, _ = c.FirstModel(context.Background(), config.FLAG_RERANK)
		Expect(m).To(BeEmpty())
	})

	It("tells an installed model from a removed or disabled one, for the default of a tool", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "on.yaml"), []byte("name: on\nbackend: stablediffusion\nknown_usecases: [image]\n"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "off.yaml"), []byte("name: off\nbackend: stablediffusion\nknown_usecases: [image]\ndisabled: true\n"), 0o600)).To(Succeed())
		cl := config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
		c := &Client{ConfigLoader: cl}
		for name, want := range map[string]bool{"on": true, "off": false, "removed": false} {
			has, err := c.HasModel(context.Background(), name)
			Expect(err).ToNot(HaveOccurred())
			Expect(has).To(Equal(want), name)
		}
		// A chooser that cannot say is trusted.
		has, _ := (&Client{Models: fixedChooser("x")}).HasModel(context.Background(), "anything")
		Expect(has).To(BeTrue())
	})

	It("reads no default for a user it cannot tell, so a tool still works", func() {
		Expect((&Client{}).userDefault(context.Background(), "image").Kind).To(BeEmpty())
	})

	It("lets Models replace the default chooser", func() {
		c := &Client{Models: fixedChooser("custom")}
		m, err := c.FirstModel(context.Background(), config.FLAG_IMAGE)
		Expect(err).ToNot(HaveOccurred())
		Expect(m).To(Equal("custom"))
	})

	It("maps a workspace reference to a path in the home folder", func() {
		Expect(homeRel("/workspace/home/generated/a.png")).To(Equal("/generated/a.png"))
		Expect(homeRel("/workspace/home")).To(Equal("/"))
		Expect(homeRel("generated/a.png")).To(Equal("generated/a.png"))
	})

	It("reports that the user's files are unavailable when the chats are off", func() {
		fs := (&Client{}).clientFiles()
		_, err := fs.Read(context.Background(), "a.png", 10)
		Expect(err).To(MatchError(ContainSubstring("not available")))
		_, err = fs.Write(context.Background(), "a.png", []byte("x"), "image/png")
		Expect(err).To(MatchError(ContainSubstring("not available")))
	})

	Describe("the workspace file store", func() {
		var (
			ws   localaitools.FileStore
			ctx  = context.Background()
			user = "alice"
		)
		BeforeEach(func() {
			dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
			db, err := sqlitedb.Open(dsn, &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
			Expect(err).NotTo(HaveOccurred())
			store, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				if raw, err := db.DB(); err == nil {
					_ = raw.Close()
				}
			})
			svc := files.New(store)
			ws = workspaceFiles{lookup: func(context.Context) (*files.Service, string, error) { return svc, user, nil }}
		})

		It("writes a result into the home of the user and reads it back by the same reference", func() {
			ref, err := ws.Write(ctx, "image.png", []byte("pixels"), "image/png")
			Expect(err).ToNot(HaveOccurred())
			Expect(ref).To(HavePrefix("/workspace/home/generated/image-"))
			Expect(ref).To(HaveSuffix(".png"))
			f, err := ws.Read(ctx, ref, 100)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(f.Data)).To(Equal("pixels"))
			// The same file by a path relative to the home.
			f, err = ws.Read(ctx, strings.TrimPrefix(ref, "/workspace/home/"), 100)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(f.Data)).To(Equal("pixels"))
			_, err = ws.Read(ctx, ref, 3)
			Expect(err).To(MatchError(localaitools.ErrFileTooLarge))
		})

		It("does not show one user the files of another, and does not leave the home", func() {
			ref, err := ws.Write(ctx, "secret.png", []byte("pixels"), "image/png")
			Expect(err).ToNot(HaveOccurred())
			user = "bob"
			_, err = ws.Read(ctx, ref, 100)
			Expect(err).To(MatchError(localaitools.ErrFileNotFound))
			_, err = ws.Read(ctx, "/users/alice/generated/"+filepath.Base(ref), 100)
			Expect(err).To(MatchError(localaitools.ErrFileNotFound))
			_, err = ws.Read(ctx, "../alice/generated/"+filepath.Base(ref), 100)
			Expect(err).To(MatchError(localaitools.ErrFileNotFound))
		})
	})
})

type fixedChooser string

func (f fixedChooser) FirstModel(context.Context, config.ModelConfigUsecase) (string, error) {
	return string(f), nil
}
