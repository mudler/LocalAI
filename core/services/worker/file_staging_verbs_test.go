package worker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// newFakeS3 answers every upload with 200 and every other object request
// with 404, so the stage verb can succeed and the ensure verb can fail
// without any real object storage.
func newFakeS3() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func registerFileVerbsForTest(cfg *Config, bus *recordingBus) error {
	return cfg.registerFileStagingVerbs(newNATSControlServer(bus, "n1"), nil)
}

var _ = Describe("Worker file staging verbs over NATS", func() {
	var (
		bus      *recordingBus
		cfg      *Config
		cacheDir string
		s3       *httptest.Server
	)

	BeforeEach(func() {
		s3 = newFakeS3()
		DeferCleanup(s3.Close)
		root := canonicalWorkerTempDir()
		cfg = &Config{
			ModelsPath:       filepath.Join(root, "models"),
			StorageURL:       s3.URL,
			StorageBucket:    "bucket",
			StorageAccessKey: "key",
			StorageSecretKey: "secret",
		}
		Expect(os.MkdirAll(cfg.ModelsPath, 0750)).To(Succeed())
		cacheDir = filepath.Join(root, "cache")
		bus = newRecordingBus()
		Expect(registerFileVerbsForTest(cfg, bus)).To(Succeed())
	})

	reply := func(subject func(string) string, body string) string {
		GinkgoHelper()
		var got string
		Eventually(bus.deliver(subject("n1"), []byte(body))).Should(Receive(&got))
		return got
	}

	It("subscribes the five file subjects of the node, release first", func() {
		Expect(bus.subscribed()).To(Equal([]string{
			messaging.SubjectNodeFilesRelease("n1"),
			messaging.SubjectNodeFilesEnsure("n1"),
			messaging.SubjectNodeFilesStage("n1"),
			messaging.SubjectNodeFilesTemp("n1"),
			messaging.SubjectNodeFilesListDir("n1"),
		}))
	})

	DescribeTable("answers a malformed body with the invalid request refusal",
		func(subject func(string) string) {
			Expect(reply(subject, malformedBody)).To(Equal(`{"error":"invalid request"}`))
		},
		Entry("files.release", messaging.SubjectNodeFilesRelease),
		Entry("files.ensure", messaging.SubjectNodeFilesEnsure),
		Entry("files.stage", messaging.SubjectNodeFilesStage),
		Entry("files.listdir", messaging.SubjectNodeFilesListDir),
	)

	It("allocates a temp file even when the body is malformed", func() {
		got := reply(messaging.SubjectNodeFilesTemp, malformedBody)
		Expect(got).To(MatchRegexp(`^\{"local_path":"` + filepath.Join(cacheDir, "staging-tmp") + `/localai-staging-[0-9]+\.tmp"\}$`))
	})

	It("answers a temp dir failure with its error", func() {
		tmpDir := filepath.Join(cacheDir, "staging-tmp")
		Expect(os.WriteFile(tmpDir, []byte("x"), 0640)).To(Succeed())
		Expect(reply(messaging.SubjectNodeFilesTemp, `{}`)).To(Equal(
			fmt.Sprintf(`{"error":"creating temp dir: mkdir %s: not a directory"}`, tmpDir)))
	})

	It("ensures a cached key and answers its local path", func() {
		path := filepath.Join(cacheDir, "models", "m.gguf")
		Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
		Expect(os.WriteFile(path, []byte("x"), 0640)).To(Succeed())
		Expect(reply(messaging.SubjectNodeFilesEnsure, `{"key":"models/m.gguf"}`)).To(Equal(
			fmt.Sprintf(`{"local_path":%q}`, path)))
	})

	It("answers an ensure failure with only an error", func() {
		Expect(reply(messaging.SubjectNodeFilesEnsure, `{"key":"models/missing.gguf"}`)).To(
			MatchRegexp(`^\{"error":"downloading models/missing.gguf: .+"\}$`))
	})

	It("stages an allowed path and answers its key", func() {
		path := filepath.Join(cfg.ModelsPath, "m.gguf")
		Expect(os.WriteFile(path, []byte("x"), 0640)).To(Succeed())
		Expect(reply(messaging.SubjectNodeFilesStage,
			fmt.Sprintf(`{"local_path":%q,"key":"models/m.gguf"}`, path))).To(Equal(`{"key":"models/m.gguf"}`))
	})

	It("refuses to stage a path outside the allowed directories", func() {
		Expect(reply(messaging.SubjectNodeFilesStage, `{"local_path":"/etc/passwd","key":"k"}`)).To(Equal(
			`{"error":"path outside allowed directories"}`))
	})

	It("lists the files under a key prefix", func() {
		dir := filepath.Join(cacheDir, "listing")
		Expect(os.MkdirAll(filepath.Join(dir, "sub"), 0750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0640)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("x"), 0640)).To(Succeed())
		Expect(reply(messaging.SubjectNodeFilesListDir, `{"key_prefix":"listing"}`)).To(Equal(
			`{"files":["a.txt","sub/b.txt"]}`))
	})

	// Before the typed reply this was {"files":null}; the frontend decodes both
	// to a nil Files slice.
	It("answers an empty listing with an empty object", func() {
		Expect(os.MkdirAll(filepath.Join(cacheDir, "empty"), 0750)).To(Succeed())
		Expect(reply(messaging.SubjectNodeFilesListDir, `{"key_prefix":"empty"}`)).To(Equal(`{}`))
	})

	It("refuses a key prefix that escapes the staging directories", func() {
		Expect(reply(messaging.SubjectNodeFilesListDir, `{"key_prefix":"../../../etc"}`)).To(Equal(
			`{"error":"invalid key prefix"}`))
	})

	It("answers a listing failure with its error", func() {
		missing := filepath.Join(cacheDir, "missing")
		Expect(reply(messaging.SubjectNodeFilesListDir, `{"key_prefix":"missing"}`)).To(Equal(
			fmt.Sprintf(`{"error":"lstat %s: no such file or directory"}`, missing)))
	})

	It("answers a successful release with an empty object", func() {
		Expect(reply(messaging.SubjectNodeFilesRelease, `{"request_id":"req-1"}`)).To(Equal(`{}`))
	})

	It("answers a refused release with its error", func() {
		Expect(reply(messaging.SubjectNodeFilesRelease, `{"key":"models/model.gguf"}`)).To(Equal(
			`{"error":"release key \"models/model.gguf\" must identify one file below ephemeral/"}`))
	})
})
