// SPDX-License-Identifier: MIT
package transport_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte{1}, 60)...)
	oggBytes  = append([]byte("OggS\x00\x02"), bytes.Repeat([]byte{2}, 60)...)
)

// photoUpdate is an update with a photo in a private chat, in an album when
// group is set.
func photoUpdate(id int, fileID, caption, group string) string {
	extra := ""
	if caption != "" {
		extra += fmt.Sprintf(`,"caption":%q`, caption)
	}
	if group != "" {
		extra += fmt.Sprintf(`,"media_group_id":%q`, group)
	}
	return fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"chat":{"id":55,"type":"private"},"from":{"id":55,"first_name":"Ada"},"photo":[{"file_id":"%s_s","file_size":900,"width":90,"height":90},{"file_id":"%s","file_size":90000,"width":800,"height":800}]%s}}`, id, id, fileID, fileID, extra)
}

func updates(items ...string) string {
	return `{"ok":true,"result":[` + strings.Join(items, ",") + `]}`
}

var _ = Describe("the files of messages", func() {
	ctx := context.Background()

	It("hands over a photo, a voice message, an audio file, a video note and a document, with the caption as the text", func() {
		f := newFakeBot()
		f.reply["getUpdates"] = func(map[string]any) (int, string) {
			return 200, updates(
				`{"update_id":1,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"caption":"what is this?","photo":[{"file_id":"small","file_size":900,"width":90,"height":90},{"file_id":"big","file_size":200000,"width":1280,"height":1280},{"file_id":"huge","file_size":3000000,"width":2560,"height":2560}]}}`,
				`{"update_id":2,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"voice":{"file_id":"v1","file_size":5000,"mime_type":"audio/ogg"}}}`,
				`{"update_id":3,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"audio":{"file_id":"a1"}}}`,
				`{"update_id":4,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"video_note":{"file_id":"n1"}}}`,
				`{"update_id":5,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"document":{"file_id":"d1","file_name":"../../etc/passwd"}}}`,
				`{"update_id":6,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"sticker":{"file_id":"s1"}}}`,
				`{"update_id":7,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"photo":[{"file_id":"bad id/../x"}]}}`)
		}
		ups, err := f.client().Updates(ctx, secretToken, 1, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ups).To(HaveLen(7))
		Expect(ups[0].Media).To(Equal([]string{"photo:big"}), "the largest size that fits")
		Expect(ups[0].Text).To(Equal("what is this?"))
		Expect(ups[0].Unsupported).To(BeFalse())
		Expect(ups[1].Media).To(Equal([]string{"voice:v1"}))
		Expect(ups[1].Unsupported).To(BeFalse())
		Expect(ups[2].Media).To(Equal([]string{"audio:a1"}))
		Expect(ups[3].Media).To(Equal([]string{"video_note:n1"}))
		Expect(ups[4].Media).To(Equal([]string{"document:d1"}), "the name of the sender goes nowhere")
		Expect(ups[5].Media).To(BeEmpty())
		Expect(ups[5].Unsupported).To(BeTrue(), "a sticker")
		Expect(ups[6].Media).To(BeEmpty(), "a file ID that is not one")
		Expect(ups[6].Unsupported).To(BeTrue())
	})

	It("finds a mention of the bot in the caption of a photo in a group", func() {
		f := newFakeBot()
		f.reply["getMe"] = func(map[string]any) (int, string) { return 200, `{"ok":true,"result":{"id":123456,"username":"helper_bot"}}` }
		f.reply["getUpdates"] = func(map[string]any) (int, string) {
			return 200, updates(`{"update_id":1,"message":{"chat":{"id":-300,"type":"supergroup"},"from":{"id":7},"caption":"@helper_bot read this","caption_entities":[{"type":"mention","offset":0,"length":11}],"photo":[{"file_id":"p1","file_size":10,"width":1,"height":1}]}}`)
		}
		ups, err := f.client().Updates(ctx, secretToken, 1, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ups[0].Mention).To(BeTrue())
		Expect(ups[0].Text).To(Equal("read this"))
		Expect(ups[0].Media).To(Equal([]string{"photo:p1"}))
	})

	It("makes one message of an album, and starts another past the limit of files", func() {
		f := newFakeBot()
		var items []string
		items = append(items, photoUpdate(10, "a0", "the trip", "G1"))
		for i := 1; i < contract.MaxConnectorMedia+2; i++ {
			items = append(items, photoUpdate(10+i, fmt.Sprintf("a%d", i), "", "G1"))
		}
		items = append(items, `{"update_id":30,"message":{"chat":{"id":55,"type":"private"},"from":{"id":55},"text":"and this"}}`)
		f.reply["getUpdates"] = func(map[string]any) (int, string) { return 200, updates(items...) }
		out, err := transport.Runtime{API: f.client()}.Connector(ctx, contract.ConnectorRequest{Op: "poll", Kind: "telegram", Token: secretToken, Offset: 10})
		Expect(err).NotTo(HaveOccurred())
		ups := out.Updates
		Expect(ups).To(HaveLen(3))
		Expect(ups[0].Media).To(HaveLen(contract.MaxConnectorMedia))
		Expect(ups[0].Media[0]).To(Equal("photo:a0"))
		Expect(ups[0].Text).To(Equal("the trip"))
		Expect(ups[0].MessageID).To(Equal("10"))
		Expect(ups[0].Cursor).To(Equal(int64(10 + contract.MaxConnectorMedia - 1)))
		Expect(ups[1].Media).To(HaveLen(2), "the rest of the album")
		Expect(ups[2].Text).To(Equal("and this"))
		Expect(out.NextOffset).To(Equal(int64(31)))
		Expect(len(f.called("getUpdates"))).To(BeNumerically("<=", 1), "the album ends with a text: no wait")
	})

	It("waits for the rest of an album and asks from the same offset", func() {
		f := newFakeBot()
		var n atomic.Int32
		f.reply["getUpdates"] = func(map[string]any) (int, string) {
			if n.Add(1) == 1 {
				return 200, updates(photoUpdate(40, "b0", "", "G2"))
			}
			return 200, updates(photoUpdate(40, "b0", "", "G2"), photoUpdate(41, "b1", "two photos", "G2"))
		}
		ups, err := f.client().Updates(ctx, secretToken, 40, 25)
		Expect(err).NotTo(HaveOccurred())
		Expect(ups).To(HaveLen(1))
		Expect(ups[0].Media).To(Equal([]string{"photo:b0", "photo:b1"}))
		Expect(ups[0].Text).To(Equal("two photos"))
		Expect(ups[0].Cursor).To(Equal(int64(41)))
		calls := f.called("getUpdates")
		Expect(len(calls)).To(BeNumerically(">=", 2))
		for _, c := range calls[1:] {
			Expect(c.Body).To(And(HaveKeyWithValue("offset", float64(40)), HaveKeyWithValue("timeout", float64(0))), "a higher offset would confirm the updates")
		}
	})

	It("downloads a photo and a voice message with names that it makes", func() {
		f := newFakeBot()
		f.files["photos/file_1.jpg"] = jpegBytes
		f.files["voice/file_2.oga"] = oggBytes
		f.reply["getFile"] = func(body map[string]any) (int, string) {
			if body["file_id"] == "p1" {
				return 200, `{"ok":true,"result":{"file_id":"p1","file_unique_id":"AQAD_u1","file_size":64,"file_path":"photos/file_1.jpg"}}`
			}
			return 200, `{"ok":true,"result":{"file_id":"v1","file_unique_id":"AgAD-v1","file_size":66,"file_path":"voice/file_2.oga"}}`
		}
		r := transport.Runtime{API: f.client()}
		out, err := r.Connector(ctx, contract.ConnectorRequest{Op: "media", Kind: "telegram", Token: secretToken, Media: "photo:p1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(out.File.Name).To(Equal("photo-AQAD-u1.jpg"))
		Expect(out.File.MIME).To(Equal("image/jpeg"))
		Expect(out.File.Data).To(Equal(jpegBytes))
		out, err = r.Connector(ctx, contract.ConnectorRequest{Op: "media", Kind: "telegram", Token: secretToken, Media: "voice:v1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Error).To(BeNil())
		Expect(out.File.Name).To(Equal("voice-AgAD-v1.ogg"))
		Expect(out.File.MIME).To(Equal("application/ogg"))
		Expect(f.called("file_1.jpg")[0].Path).To(Equal("/file/bot" + secretToken + "/photos/file_1.jpg"))
	})

	DescribeTable("refuses a file that is too large, of another type or not of the bot, and keeps the token out",
		func(ref, getFile string, data []byte, code string, downloads int) {
			f := newFakeBot()
			f.files["photos/file_1.jpg"] = data
			f.reply["getFile"] = func(map[string]any) (int, string) {
				if strings.Contains(getFile, `"ok":false`) {
					return 400, getFile
				}
				return 200, getFile
			}
			out, err := transport.Runtime{API: f.client()}.Connector(ctx, contract.ConnectorRequest{Op: "media", Kind: "telegram", Token: secretToken, Media: ref})
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Error).NotTo(BeNil())
			Expect(out.Error.Code).To(Equal(code))
			Expect(out.Error.Message).NotTo(ContainSubstring("SECRET"))
			Expect(out.File).To(BeNil())
			Expect(f.called("file_1.jpg")).To(HaveLen(downloads))
		},
		Entry("larger than the add-on hands over, by its size", "photo:p1", `{"ok":true,"result":{"file_size":2000000,"file_path":"photos/file_1.jpg"}}`, jpegBytes, contract.ConnectorTooLarge, 0),
		Entry("larger than Telegram lets a bot download", "voice:v1", `{"ok":false,"error_code":400,"description":"Bad Request: file is too big"}`, jpegBytes, contract.ConnectorTooLarge, 0),
		Entry("larger than it said", "photo:p1", `{"ok":true,"result":{"file_size":10,"file_path":"photos/file_1.jpg"}}`, bytes.Repeat([]byte{0xff}, contract.MaxConnectorFileBytes+1), contract.ConnectorTooLarge, 1),
		Entry("a photo that is not an image", "photo:p1", `{"ok":true,"result":{"file_size":10,"file_path":"photos/file_1.jpg"}}`, []byte("#!/bin/sh\necho hi"), contract.ConnectorFailed, 1),
		Entry("a voice message that is not audio", "voice:v1", `{"ok":true,"result":{"file_size":10,"file_path":"photos/file_1.jpg"}}`, jpegBytes, contract.ConnectorFailed, 1),
		Entry("a path out of the files of the bot", "photo:p1", `{"ok":true,"result":{"file_size":10,"file_path":"../../bot1/photos/file_1.jpg"}}`, jpegBytes, contract.ConnectorFailed, 0),
		Entry("a reference of another kind", "secret:p1", `{"ok":true,"result":{}}`, jpegBytes, contract.ConnectorFailed, 0),
		Entry("a reference with a path in it", "photo:../p1", `{"ok":true,"result":{}}`, jpegBytes, contract.ConnectorFailed, 0),
	)

	It("keeps the token out of the error of a failed download", func() {
		f := newFakeBot()
		f.reply["getFile"] = func(map[string]any) (int, string) {
			return 200, `{"ok":true,"result":{"file_size":10,"file_path":"photos/missing.jpg"}}`
		}
		_, err := f.client().Media(ctx, secretToken, "photo:p1")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
		Expect(errors.Is(err, transport.ErrTooLarge)).To(BeFalse())
	})
})
