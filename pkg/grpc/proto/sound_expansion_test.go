// SPDX-License-Identifier: MIT
package proto_test

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestSoundExpansion(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sound Expansion protocol")
}

var _ = Describe("Sound Expansion protocol", func() {
	It("defaults omitted expanded to false and preserves explicit marker on the wire", func() {
		var request pb.SoundGenerationRequest
		Expect(request.GetExpanded()).To(BeFalse())
		Expect(request.Expanded).To(BeNil())
		marked := true
		request.Expanded = &marked
		data, err := proto.Marshal(&request)
		Expect(err).NotTo(HaveOccurred())
		var decoded pb.SoundGenerationRequest
		Expect(proto.Unmarshal(data, &decoded)).To(Succeed())
		Expect(decoded.Expanded).NotTo(BeNil())
		Expect(decoded.GetExpanded()).To(BeTrue())
	})
})
