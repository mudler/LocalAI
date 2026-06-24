// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type failedCompanionStager struct{ FileStager }

func (failedCompanionStager) EnsureRemote(context.Context, string, string, string) (string, error) {
	return "", errors.New("worker unavailable")
}

var _ = Describe("staging virtual model companions", func() {
	DescribeTable("anchors relative assets on the worker",
		func(options, overrides []string, files []string) {
			modelsDir := GinkgoT().TempDir()
			for _, name := range files {
				path := filepath.Join(modelsDir, name)
				Expect(os.MkdirAll(filepath.Dir(path), 0750)).To(Succeed())
				Expect(os.WriteFile(path, []byte("weights"), 0600)).To(Succeed())
			}
			stager := &fakeFileStager{}
			router := &SmartRouter{fileStager: stager, stagingTracker: NewStagingTracker()}
			input := &pb.ModelOptions{Model: "insightface-buffalo-m", ModelFile: filepath.Join(modelsDir, "insightface-buffalo-m"), ModelPath: modelsDir, Options: options, Overrides: overrides}
			staged, err := router.stageModelFiles(context.Background(), &BackendNode{ID: "worker"}, input, "insightface-buffalo-m")
			Expect(err).NotTo(HaveOccurred())
			Expect(staged.ModelPath).To(Equal("/remote/models/insightface-buffalo-m"))
			Expect(staged.Options).To(Equal(options))
			Expect(staged.Overrides).To(Equal(overrides))
			Expect(input.ModelPath).To(Equal(modelsDir))
			Expect(input.ModelFile).To(Equal(filepath.Join(modelsDir, "insightface-buffalo-m")))
			Expect(stager.ensureCalls).To(HaveLen(len(files)))
			for _, call := range stager.ensureCalls {
				rel, err := filepath.Rel(modelsDir, call.localPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(filepath.Join(staged.ModelPath, rel)).To(Equal("/remote/" + call.key))
			}
		},
		Entry("Buffalo pack and MiniFASNet files", []string{"model_pack:buffalo_m", "antispoof_v2_onnx:MiniFASNetV2.onnx", "antispoof_v1se_onnx:MiniFASNetV1SE.onnx"}, nil, []string{"buffalo_m/det_2.5g.onnx", "buffalo_m/w600k_r50.onnx", "MiniFASNetV2.onnx", "MiniFASNetV1SE.onnx"}),
		Entry("only a nested companion directory", []string{"model_pack:packs/buffalo_m"}, nil, []string{"packs/buffalo_m/det_2.5g.onnx"}),
		Entry("only a companion file", []string{"antispoof_v2_onnx:MiniFASNetV2.onnx"}, nil, []string{"MiniFASNetV2.onnx"}),
		Entry("only override assets", nil, []string{"antispoof_v2_onnx:MiniFASNetV2.onnx"}, []string{"MiniFASNetV2.onnx"}),
	)
	It("keeps the original path when no assets are staged", func() {
		modelsDir := GinkgoT().TempDir()
		router := &SmartRouter{fileStager: &fakeFileStager{}, stagingTracker: NewStagingTracker()}
		input := &pb.ModelOptions{Model: "virtual", ModelFile: filepath.Join(modelsDir, "virtual"), ModelPath: modelsDir, Options: []string{"engine:insightface"}}
		staged, err := router.stageModelFiles(context.Background(), &BackendNode{ID: "worker"}, input, "virtual")
		Expect(err).NotTo(HaveOccurred())
		Expect(staged.ModelPath).To(Equal(modelsDir))
	})
	DescribeTable("keeps the original root when companion staging fails",
		func(directory bool) {
			modelsDir := GinkgoT().TempDir()
			relative := "MiniFASNetV2.onnx"
			if directory {
				relative = "buffalo_m/det_2.5g.onnx"
			}
			local := filepath.Join(modelsDir, relative)
			Expect(os.MkdirAll(filepath.Dir(local), 0750)).To(Succeed())
			Expect(os.WriteFile(local, []byte("weights"), 0600)).To(Succeed())
			value := relative
			if directory {
				value = "buffalo_m"
			}
			router := &SmartRouter{fileStager: failedCompanionStager{}, stagingTracker: NewStagingTracker()}
			input := &pb.ModelOptions{Model: "virtual", ModelFile: filepath.Join(modelsDir, "virtual"), ModelPath: modelsDir, Options: []string{"asset:" + value}}
			staged, err := router.stageModelFiles(context.Background(), &BackendNode{ID: "worker"}, input, "virtual")
			Expect(err).NotTo(HaveOccurred())
			Expect(staged.ModelPath).To(Equal(modelsDir))
		}, Entry("file", false), Entry("directory", true),
	)

})
