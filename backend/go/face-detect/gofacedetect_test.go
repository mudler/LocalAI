package main

import (
	"encoding/base64"
	"os"
	"sync"
	"testing"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFaceDetect(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "face-detect Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive the C-API
// bridge without spinning up the gRPC server. Records the error (the smoke
// specs skip themselves) when libfacedetect.so is not loadable from cwd
// (LD_LIBRARY_PATH or a symlink in ./).
func ensureLibLoaded() error {
	libLoadOnce.Do(func() {
		libName := os.Getenv("FACEDETECT_LIBRARY")
		if libName == "" {
			libName = "libfacedetect.so"
		}
		lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			libLoadErr = err
			return
		}
		purego.RegisterLibFunc(&CppAbiVersion, lib, "facedetect_capi_abi_version")
		purego.RegisterLibFunc(&CppLoad, lib, "facedetect_capi_load")
		purego.RegisterLibFunc(&CppFree, lib, "facedetect_capi_free")
		purego.RegisterLibFunc(&CppLastError, lib, "facedetect_capi_last_error")
		purego.RegisterLibFunc(&CppFreeString, lib, "facedetect_capi_free_string")
		purego.RegisterLibFunc(&CppFreeVec, lib, "facedetect_capi_free_vec")
		purego.RegisterLibFunc(&CppEmbedPath, lib, "facedetect_capi_embed_path")
		purego.RegisterLibFunc(&CppEmbedRGB, lib, "facedetect_capi_embed_rgb")
		purego.RegisterLibFunc(&CppDetectJSON, lib, "facedetect_capi_detect_path_json")
		purego.RegisterLibFunc(&CppVerifyPaths, lib, "facedetect_capi_verify_paths")
		purego.RegisterLibFunc(&CppAnalyzeJSON, lib, "facedetect_capi_analyze_path_json")
	})
	return libLoadErr
}

var _ = Describe("parseOptions", func() {
	It("defaults verify_threshold to 0.35", func() {
		o := parseOptions(nil)
		Expect(o.verifyThreshold).To(Equal(float32(0.35)))
		Expect(o.modelName).To(Equal(""))
	})

	It("parses verify_threshold, threshold alias and model_name", func() {
		o := parseOptions([]string{"verify_threshold:0.4", "model_name:buffalo_l", "unknown:x"})
		Expect(o.verifyThreshold).To(Equal(float32(0.4)))
		Expect(o.modelName).To(Equal("buffalo_l"))

		o2 := parseOptions([]string{"threshold:0.3"})
		Expect(o2.verifyThreshold).To(Equal(float32(0.3)))
	})

	It("ignores non-positive thresholds and keeps the default", func() {
		o := parseOptions([]string{"verify_threshold:0", "threshold:-1"})
		Expect(o.verifyThreshold).To(Equal(float32(0.35)))
	})
})

var _ = Describe("normalizeGender", func() {
	It("maps M/F codes to Man/Woman", func() {
		Expect(normalizeGender("M")).To(Equal("Man"))
		Expect(normalizeGender("f")).To(Equal("Woman"))
		Expect(normalizeGender(" m ")).To(Equal("Man"))
	})

	It("passes empty and unknown codes through", func() {
		Expect(normalizeGender("")).To(Equal(""))
		Expect(normalizeGender("nonbinary")).To(Equal("nonbinary"))
	})
})

var _ = Describe("faceBox.xywh", func() {
	It("converts an [x1,y1,x2,y2] box to x/y/width/height", func() {
		b := faceBox{Box: []float32{10, 20, 50, 80}}
		x, y, w, h := b.xywh()
		Expect(x).To(Equal(float32(10)))
		Expect(y).To(Equal(float32(20)))
		Expect(w).To(Equal(float32(40)))
		Expect(h).To(Equal(float32(60)))
	})

	It("returns zeros for a short box", func() {
		x, y, w, h := faceBox{Box: []float32{1, 2}}.xywh()
		Expect([]float32{x, y, w, h}).To(Equal([]float32{0, 0, 0, 0}))
	})
})

var _ = Describe("parseAnalyzeJSON", func() {
	It("maps region, age and gender for each face", func() {
		doc := `{"faces":[
			{"score":0.997,"box":[10,20,50,80],"age":31,"gender":"M"},
			{"score":0.81,"box":[0,0,40,40],"age":24,"gender":"F"}]}`
		faces, err := parseAnalyzeJSON(doc)
		Expect(err).ToNot(HaveOccurred())
		Expect(faces).To(HaveLen(2))

		Expect(faces[0].FaceConfidence).To(BeNumerically("~", 0.997, 1e-4))
		Expect(faces[0].Age).To(BeNumerically("~", 31, 1e-4))
		Expect(faces[0].DominantGender).To(Equal("Man"))
		Expect(faces[0].Gender).To(HaveKeyWithValue("Man", float32(1.0)))
		Expect(faces[0].Region.W).To(Equal(float32(40)))
		Expect(faces[0].Region.H).To(Equal(float32(60)))

		Expect(faces[1].DominantGender).To(Equal("Woman"))
	})

	It("tolerates a missing gender field", func() {
		faces, err := parseAnalyzeJSON(`{"faces":[{"score":0.5,"box":[0,0,10,10],"age":40}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(faces).To(HaveLen(1))
		Expect(faces[0].DominantGender).To(Equal(""))
		Expect(faces[0].Gender).To(BeEmpty())
	})

	It("returns no faces for an empty document", func() {
		faces, err := parseAnalyzeJSON(`{"faces":[]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(faces).To(BeEmpty())
	})

	It("returns an error on malformed JSON", func() {
		_, err := parseAnalyzeJSON(`{not-json`)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("materializeImage", func() {
	It("decodes a base64 payload to a temp file", func() {
		payload := base64.StdEncoding.EncodeToString([]byte("\xff\xd8\xff\xe0fake-jpeg"))
		path, cleanup, err := materializeImage(payload)
		Expect(err).ToNot(HaveOccurred())
		defer cleanup()
		data, rerr := os.ReadFile(path)
		Expect(rerr).ToNot(HaveOccurred())
		Expect(data).To(Equal([]byte("\xff\xd8\xff\xe0fake-jpeg")))
	})

	It("strips a data: URI prefix before decoding", func() {
		payload := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("hello"))
		path, cleanup, err := materializeImage(payload)
		Expect(err).ToNot(HaveOccurred())
		defer cleanup()
		data, rerr := os.ReadFile(path)
		Expect(rerr).ToNot(HaveOccurred())
		Expect(data).To(Equal([]byte("hello")))
	})

	It("uses an existing path as-is", func() {
		tmp, err := os.CreateTemp("", "face-detect-fixture-*.bin")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = os.Remove(tmp.Name()) }()
		Expect(tmp.Close()).To(Succeed())

		path, cleanup, err := materializeImage(tmp.Name())
		Expect(err).ToNot(HaveOccurred())
		defer cleanup()
		Expect(path).To(Equal(tmp.Name()))
	})

	It("errors on input that is neither a path nor base64", func() {
		_, _, err := materializeImage("not base64!!!")
		Expect(err).To(HaveOccurred())
	})
})

// The specs below exercise the real C-API end to end. They run only when both a
// model GGUF and a test image are provided, and skip cleanly otherwise so the
// suite stays green without large assets.
var _ = Describe("FaceDetect end-to-end", Ordered, func() {
	var (
		f         *FaceDetect
		modelPath = os.Getenv("FACEDETECT_BACKEND_TEST_MODEL")
		imagePath = os.Getenv("FACEDETECT_BACKEND_TEST_IMAGE")
	)

	BeforeAll(func() {
		if modelPath == "" || imagePath == "" {
			Skip("set FACEDETECT_BACKEND_TEST_MODEL and FACEDETECT_BACKEND_TEST_IMAGE to run the e2e specs")
		}
		if err := ensureLibLoaded(); err != nil {
			Skip("libfacedetect.so not loadable: " + err.Error())
		}
		f = &FaceDetect{}
		Expect(f.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
	})

	It("embeds the primary face in an image", func() {
		emb, err := f.Embeddings(&pb.PredictOptions{Images: []string{imagePath}})
		Expect(err).ToNot(HaveOccurred())
		Expect(emb).ToNot(BeEmpty())
	})

	It("detects at least one face", func() {
		resp, err := f.Detect(&pb.DetectOptions{Src: imagePath})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Detections).ToNot(BeEmpty())
		Expect(resp.Detections[0].ClassName).To(Equal("face"))
	})

	It("verifies an image against itself as the same identity", func() {
		resp, err := f.FaceVerify(&pb.FaceVerifyRequest{Img1: imagePath, Img2: imagePath})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Verified).To(BeTrue())
		Expect(resp.Distance).To(BeNumerically("<=", resp.Threshold))
	})

	It("analyzes age/gender for each face", func() {
		resp, err := f.FaceAnalyze(&pb.FaceAnalyzeRequest{Img: imagePath})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Faces).ToNot(BeEmpty())
	})
})
