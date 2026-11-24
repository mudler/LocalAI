package modeladmin

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreateConfig and DeleteConfig", func() {
	const body = "name: remote-one\nbackend: localai-proxy\nknown_usecases: [chat]\nproxy:\n  upstream_url: https://remote.example.com\n  remote: c1\n"

	It("creates a model that the loader sees at once and tells the lifecycle", func() {
		svc, dir := newTestService()
		lc := &fakeRevisionLifecycle{}
		svc.Lifecycle = lc
		res, err := svc.CreateConfig(context.Background(), []byte(body))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.NewName).To(Equal("remote-one"))
		Expect(res.ConfigRevision).NotTo(BeEmpty())
		Expect(filepath.Join(dir, "remote-one.yaml")).To(BeARegularFile())
		cfg, ok := svc.Loader.GetModelConfig("remote-one")
		Expect(ok).To(BeTrue())
		Expect(cfg.Proxy.Remote).To(Equal("c1"))
		Expect(lc.calls).To(HaveLen(1))
	})
	It("refuses a name that exists", func() {
		svc, _ := newTestService()
		_, err := svc.CreateConfig(context.Background(), []byte(body))
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.CreateConfig(context.Background(), []byte(body))
		Expect(errors.Is(err, ErrConflict)).To(BeTrue())
	})
	It("refuses an invalid config and leaves no file", func() {
		svc, dir := newTestService()
		bad := "name: x\nbackend: localai-proxy\nproxy:\n  remote: c\n  api_key: k\n"
		_, err := svc.CreateConfig(context.Background(), []byte(bad))
		Expect(errors.Is(err, ErrInvalidConfig)).To(BeTrue())
		Expect(filepath.Join(dir, "x.yaml")).NotTo(BeAnExistingFile())
	})
	It("refuses a name with a path separator", func() {
		svc, _ := newTestService()
		_, err := svc.CreateConfig(context.Background(), []byte("name: a/b\nbackend: localai-proxy\n"))
		Expect(errors.Is(err, ErrPathSeparator)).To(BeTrue())
	})
	It("deletes a config, drops it from the loader and publishes a tombstone", func() {
		svc, dir := newTestService()
		lc := &fakeRevisionLifecycle{}
		svc.Lifecycle = lc
		_, err := svc.CreateConfig(context.Background(), []byte(body))
		Expect(err).NotTo(HaveOccurred())
		res, err := svc.DeleteConfig(context.Background(), "remote-one")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.ConfigRevision).To(Equal(DeletedModelConfigRevision("remote-one")))
		Expect(filepath.Join(dir, "remote-one.yaml")).NotTo(BeAnExistingFile())
		_, ok := svc.Loader.GetModelConfig("remote-one")
		Expect(ok).To(BeFalse())
		Expect(lc.calls[len(lc.calls)-1].disabled).To(BeTrue())
	})
	It("restores the file when the lifecycle fails during delete", func() {
		svc, dir := newTestService()
		_, err := svc.CreateConfig(context.Background(), []byte(body))
		Expect(err).NotTo(HaveOccurred())
		svc.Lifecycle = &fakeRevisionLifecycle{err: errors.New("boom")}
		_, err = svc.DeleteConfig(context.Background(), "remote-one")
		Expect(err).To(HaveOccurred())
		data, rerr := os.ReadFile(filepath.Join(dir, "remote-one.yaml"))
		Expect(rerr).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(body))
		_, ok := svc.Loader.GetModelConfig("remote-one")
		Expect(ok).To(BeTrue())
	})
	It("reports an unknown model", func() {
		svc, _ := newTestService()
		_, err := svc.DeleteConfig(context.Background(), "nope")
		Expect(errors.Is(err, ErrNotFound)).To(BeTrue())
	})
})
