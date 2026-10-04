// SPDX-License-Identifier: MIT
package systemone

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func pngURL(w, h int) string {
	var b bytes.Buffer
	Expect(png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)))).To(Succeed())
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

var _ = Describe("Canonical decision images", func() {
	It("validates headers, not just base64", func() {
		for _, u := range []string{"data:image/png;base64,AA==", "data:image/gif;base64,AA==", "https://example.org/x.png", strings.Replace(pngURL(1, 1), "image/png", "image/jpeg", 1)} {
			r := validRequest()
			r.Images, _ = json.Marshal([]string{u})
			Expect(ValidateRequestStructure(r)).NotTo(Succeed())
		}
	})
	It("accepts image-only structured state and preserves input", func() {
		r := validRequest()
		r.State = json.RawMessage(`{}`)
		r.Images, _ = json.Marshal([]string{pngURL(1, 1)})
		before, _ := json.Marshal(r)
		Expect(ValidateRequest(r)).To(Succeed())
		after, _ := json.Marshal(r)
		Expect(after).To(Equal(before))
	})
	It("rejects excessive dimensions and aggregate pixels", func() {
		for _, urls := range [][]string{{pngURL(4097, 1)}, {pngURL(3000, 3000), pngURL(3000, 3000)}} {
			r := validRequest()
			r.Images, _ = json.Marshal(urls)
			Expect(ValidateRequestStructure(r)).NotTo(Succeed())
		}
	})
	It("accepts image-bearing bodies above the text cap but not empty image arrays", func() {
		r := validRequest()
		r.State, _ = json.Marshal(map[string]string{"text": strings.Repeat("a", MaxBodyBytes)})
		r.Images = json.RawMessage(`[]`)
		Expect(ValidateRequest(r)).NotTo(Succeed())
		r.Images, _ = json.Marshal([]string{pngURL(1, 1)})
		Expect(ValidateRequest(r)).To(Succeed())
	})
})

var _ = Describe("Decision image boundaries", func() {
	It("collects both chat formats in order without interpreting domain objects", func() {
		u := pngURL(1, 1)
		data := strings.SplitN(u, ",", 2)[1]
		r := validRequest()
		r.Images, _ = json.Marshal([]string{u})
		r.State, _ = json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": u}}, map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": data}}}}}})
		urls, err := CollectImages(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(urls).To(Equal([]string{u, u, u}))
		Expect(ValidateRequest(r)).To(Succeed())
		r.Images = nil
		r.State = json.RawMessage(`{"type":"image","source":"domain data"}`)
		urls, err = CollectImages(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(urls).To(BeEmpty())
	})
	It("rejects malformed base64, headers and non-array images", func() {
		for _, u := range []string{"data:image/png;base64,AB==", "data:image/png;base64,AA\n=", "data:image/png;charset=utf8;base64,AA==", "data:image/png;base64,====", "file:///x", "data:image/png;base64,AAA"} {
			Expect(ValidateImages([]string{u})).NotTo(Succeed())
		}
		for _, raw := range []string{`""`, `{}`, `[null]`, `[1]`} {
			r := validRequest()
			r.Images = json.RawMessage(raw)
			Expect(ValidateRequestStructure(r)).NotTo(Succeed())
		}
	})
	It("bounds count, encoded and decoded aggregate before image decoding", func() {
		u := pngURL(1, 1)
		Expect(ValidateImages([]string{u, u, u, u, u, u, u, u})).To(Succeed())
		for _, urls := range [][]string{{u, u, u, u, u, u, u, u, u}, {strings.Repeat("x", MaxImageEncodedBytes+1)}, {"data:image/png;base64," + strings.Repeat("A", ((MaxImageDecodedBytes+3)/3)*4)}} {
			err := ValidateImages(urls)
			Expect(err).To(BeAssignableToTypeOf(&ValidationError{}))
			Expect(err.(*ValidationError).Kind).To(Equal(InputTooLarge))
		}
		Expect(ValidateImages([]string{pngURL(4000, 4000)})).To(Succeed())
	})
	It("keeps absent/null/empty image budgets and missing-state rejection", func() {
		for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`[]`)} {
			r := validRequest()
			r.Images = raw
			limit, err := RequestBodyLimit(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(limit).To(Equal(MaxBodyBytes))
		}
		for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`" "`)} {
			r := validRequest()
			r.State = raw
			r.Images, _ = json.Marshal([]string{pngURL(1, 1)})
			Expect(ValidateRequestStructure(r)).NotTo(Succeed())
		}
	})
})

var _ = Describe("Complete image validation", func() {
	It("rejects header-only PNG and JPEG while accepting valid JPEG", func() {
		var b bytes.Buffer
		Expect(jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 8, 8)), nil)).To(Succeed())
		jpegBytes := b.Bytes()
		Expect(ValidateImages([]string{"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes)})).To(Succeed())
		raw, err := base64.StdEncoding.DecodeString(strings.SplitN(pngURL(8, 8), ",", 2)[1])
		Expect(err).NotTo(HaveOccurred())
		for _, item := range []struct {
			mime string
			data []byte
		}{{"png", raw[:33]}, {"jpeg", jpegBytes[:len(jpegBytes)-10]}} {
			Expect(ValidateImages([]string{"data:image/" + item.mime + ";base64," + base64.StdEncoding.EncodeToString(item.data)})).NotTo(Succeed())
		}
	})
})

var _ = Describe("Exact decision byte boundaries", func() {
	It("accepts exactly the decoded budget and rejects the next byte", func() {
		raw, _ := base64.StdEncoding.DecodeString(strings.SplitN(pngURL(1, 1), ",", 2)[1])
		raw = append(raw, make([]byte, MaxImageDecodedBytes-len(raw))...)
		url := func(b []byte) string { return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b) }
		Expect(ValidateImages([]string{url(raw)})).To(Succeed())
		err := ValidateImages([]string{url(append(raw, 0))})
		Expect(err.(*ValidationError).Kind).To(Equal(InputTooLarge))
	})
	It("preserves distinct image ordering and bytes", func() {
		a, b, c := pngURL(1, 1), pngURL(2, 1), pngURL(3, 1)
		r := validRequest()
		r.Images, _ = json.Marshal([]string{a, b})
		r.State, _ = json.Marshal([]any{map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": c}}}})
		images, err := CollectImages(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(images).To(Equal([]string{a, b, c}))
	})
})
