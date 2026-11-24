// SPDX-License-Identifier: MIT
package transport_test

import (
	"bytes"
	"strings"

	"github.com/mudler/LocalAI/addons/discord/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	msgFile = "800000000000000001"
	attID   = "810000000000000001"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 100)...)

var _ = Describe("the files of messages", func() {
	var (
		f *fakeDiscord
		h *memHost
		r *transport.Runtime
	)
	ref := dmID + "/" + msgFile + "/" + attID
	BeforeEach(func() {
		f = newFakeDiscord()
		h = &memHost{}
		r = f.runtime(h)
		DeferCleanup(func() { r.Close(); f.Close() })
		f.files["files/pic.png"] = pngBytes
		f.messages[dmID+"/"+msgFile] = map[string]any{"id": msgFile, "channel_id": dmID, "content": "", "author": author(userID, "ada", false),
			"attachments": []any{map[string]any{"id": attID, "filename": "../../pic.png", "content_type": "image/png", "size": len(pngBytes),
				"url": f.srv.URL + "/cdn/files/pic.png?ex=1&is=2&hm=3"}}}
	})
	handle := &contract.ConnectorTransfer{Handle: "h", MaxBytes: 1 << 20}

	It("streams a file of a message to the host by handle, with a safe name, from a fresh URL", func() {
		out := call(r, contract.ConnectorRequest{Op: "media", Media: ref, Transfer: handle})
		Expect(out.Error).To(BeNil())
		Expect(out.Received.Name).To(Equal("pic.png"))
		Expect(out.Received.MIME).To(Equal("image/png"))
		Expect(h.got.Bytes()).To(Equal(pngBytes))
		Expect(f.called("GET /channels/:id/messages/:id")).To(HaveLen(1), "the message is read for the current URL")
		cdn := f.called("GET /cdn/files/pic.png")
		Expect(cdn).To(HaveLen(1))
		Expect(cdn[0].Header.Get("Authorization")).To(BeEmpty(), "the CDN never gets the token")
	})

	DescribeTable("refuses a file it must not fetch",
		func(ref string, max int64, mutate func(), code string, downloads int) {
			mutate()
			out := call(r, contract.ConnectorRequest{Op: "media", Media: ref, Transfer: &contract.ConnectorTransfer{Handle: "h", MaxBytes: max}})
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(code))
			Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
			Expect(out.Received).To(BeNil())
			Expect(f.called("GET /cdn/files/pic.png")).To(HaveLen(downloads))
		},
		Entry("larger than the bound of the handle", ref, int64(50), func() {}, contract.ConnectorTooLarge, 0),
		Entry("larger than the add-on hands over", ref, int64(1<<30), func() {
			f.messages[dmID+"/"+msgFile]["attachments"].([]any)[0].(map[string]any)["size"] = transport.MaxFileBytes + 1
		}, contract.ConnectorTooLarge, 0),
		Entry("a reference with a path in it", dmID+"/../"+attID, int64(1<<20), func() {}, contract.ConnectorFailed, 0),
		Entry("a reference of the wrong shape", "pic.png", int64(1<<20), func() {}, contract.ConnectorFailed, 0),
		Entry("a file that the message no longer has", dmID+"/"+msgFile+"/819999999999999999", int64(1<<20), func() {}, contract.ConnectorFailed, 0),
		Entry("a URL outside the Discord CDN", ref, int64(1<<20), func() {
			f.messages[dmID+"/"+msgFile]["attachments"].([]any)[0].(map[string]any)["url"] = "http://evil.example/x"
		}, contract.ConnectorFailed, 0),
		Entry("a download that fails", ref, int64(1<<20), func() { delete(f.files, "files/pic.png") }, contract.ConnectorFailed, 1),
	)

	It("keeps the token out of the error of a failed message read", func() {
		f.on["GET /channels/:id/messages/:id"] = func(restCall) reply {
			return reply{Status: 403, Body: `{"code":50001,"message":"Missing Access ` + secretToken + `"}`}
		}
		out := call(r, contract.ConnectorRequest{Op: "media", Media: ref, Transfer: handle})
		Expect(out.Error).NotTo(BeNil())
		Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
	})

	It("uploads a file from the host as one attachment in a message with no text and no pings", func() {
		h.src = pngBytes
		out := call(r, contract.ConnectorRequest{Op: "send_file", Chat: chanID, Thread: threadI,
			Transfer: &contract.ConnectorTransfer{Handle: "h", Name: "pic.png", Size: int64(len(pngBytes))}})
		Expect(out.Error).To(BeNil())
		Expect(out.MessageID).NotTo(BeEmpty())
		c := f.called("POST /channels/:id/messages")
		Expect(c).To(HaveLen(1))
		Expect(c[0].Path).To(Equal("/channels/" + threadI + "/messages"))
		Expect(c[0].File.Name).To(Equal("pic.png"))
		Expect(c[0].File.Data).To(Equal(pngBytes))
		Expect(c[0].Body["allowed_mentions"]).To(Equal(map[string]any{"parse": []any{}}))
		Expect(c[0].Header.Get("Content-Type")).To(HavePrefix("multipart/form-data"))
	})

	It("refuses to upload a file over the limit before it reads a byte", func() {
		out := call(r, contract.ConnectorRequest{Op: "send_file", Chat: chanID,
			Transfer: &contract.ConnectorTransfer{Handle: "h", Name: "big.bin", Size: transport.MaxFileBytes + 1}})
		Expect(out.Error).NotTo(BeNil())
		Expect(out.Error.Code).To(Equal(contract.ConnectorTooLarge))
		Expect(f.called("POST /channels/:id/messages")).To(BeEmpty())
	})

	It("fails an upload whose file is not the size it said", func() {
		h.src = []byte(strings.Repeat("a", 50))
		out := call(r, contract.ConnectorRequest{Op: "send_file", Chat: chanID,
			Transfer: &contract.ConnectorTransfer{Handle: "h", Name: "x.txt", Size: 10}})
		Expect(out.Error).NotTo(BeNil())
	})
})
