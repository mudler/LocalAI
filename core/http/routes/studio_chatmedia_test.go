// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/go-pdf/fpdf"
	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/media"
)

// attach sends a message with one attached file and waits for the turn to end.
func (s *studioServer) attach(user, id, text, name string, body []byte) {
	GinkgoHelper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	Expect(mw.WriteField("text", text)).To(Succeed())
	fw, err := mw.CreateFormFile("files", name)
	Expect(err).NotTo(HaveOccurred())
	_, err = fw.Write(body)
	Expect(err).NotTo(HaveOccurred())
	Expect(mw.Close()).To(Succeed())
	req := httptest.NewRequest(http.MethodPost, "/api/chats/"+id+"/messages", &buf)
	req.Header.Set(echo.HeaderContentType, mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	s.waitIdle(user, id)
}

// lastUserParts returns the content parts of the last user message of a
// request, or nil when its content is a plain string.
func lastUserParts(req map[string]any) []map[string]any {
	msgs := req["messages"].([]any)
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i].(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		parts, ok := msg["content"].([]any)
		if !ok {
			return nil
		}
		var out []map[string]any
		for _, p := range parts {
			out = append(out, p.(map[string]any))
		}
		return out
	}
	return nil
}

func partTypes(parts []map[string]any) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p["type"].(string))
	}
	return out
}

func tinyPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	Expect(png.Encode(&buf, img)).To(Succeed())
	return buf.Bytes()
}

func tinyPDF(text string) []byte {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Helvetica", "", 14)
	pdf.Cell(40, 10, text)
	var buf bytes.Buffer
	Expect(pdf.Output(&buf)).To(Succeed())
	return buf.Bytes()
}

var _ = Describe("Chats media input", func() {
	withMedia := func(vision, video bool) func(*studio.Deps) {
		return func(d *studio.Deps) {
			d.Modalities = func(string) studio.Modalities { return studio.Modalities{Vision: vision, Video: video} }
			d.Media = media.New(d.Provider, nil, func(media.Capability) string { return "" })
		}
	}

	It("sends an attached image to a vision model as an image part", func() {
		llm := &fakeLLM{}
		s := newLLMStudioServerWith(llm, withMedia(true, false))
		id := s.create("alice")
		s.attach("alice", id, "what is this?", "photo.png", tinyPNG())

		parts := lastUserParts(llm.last())
		Expect(partTypes(parts)).To(ConsistOf("text", "image_url"))
		for _, p := range parts {
			if p["type"] == "image_url" {
				Expect(p["image_url"].(map[string]any)["url"]).To(HavePrefix("data:image/png;base64,"))
			}
		}
		// The file is also in the chat folder, and the model is told its path.
		Expect(userTexts(llm.last())[0]).To(And(ContainSubstring("what is this?"), ContainSubstring("photo.png")))
	})

	It("leaves the image a file for a model without vision, which has describe_image", func() {
		llm := &fakeLLM{}
		s := newLLMStudioServerWith(llm, withMedia(false, false))
		id := s.create("alice")
		s.attach("alice", id, "what is this?", "photo.png", tinyPNG())

		Expect(lastUserParts(llm.last())).To(BeNil())
		Expect(userTexts(llm.last())[0]).To(ContainSubstring("photo.png"))
	})

	It("sends a video to a model that takes video, and not to one that does not", func() {
		llm := &fakeLLM{}
		s := newLLMStudioServerWith(llm, withMedia(false, true))
		id := s.create("alice")
		s.attach("alice", id, "summarize", "clip.mp4", []byte("not really a video, but named like one"))
		Expect(partTypes(lastUserParts(llm.last()))).To(ContainElement("video_url"))

		llm2 := &fakeLLM{}
		s2 := newLLMStudioServerWith(llm2, withMedia(true, false))
		id2 := s2.create("alice")
		s2.attach("alice", id2, "summarize", "clip.mp4", []byte("x"))
		Expect(lastUserParts(llm2.last())).To(BeNil())
	})

	It("reads an attached PDF into the message and keeps the file", func() {
		llm := &fakeLLM{}
		s := newLLMStudioServerWith(llm, withMedia(false, false))
		id := s.create("alice")
		s.attach("alice", id, "summarize it", "report.pdf", tinyPDF("Quarterly revenue grew twelve percent"))

		text := strings.Join(userTexts(llm.last()), "\n")
		Expect(text).To(And(ContainSubstring("Text of report.pdf"), ContainSubstring("Quarterly revenue grew twelve percent"), ContainSubstring("summarize it")))
		Expect(text).To(ContainSubstring("/Chats/")) // the file path is still given
		// The tool for reading the rest is offered.
		var names []string
		for _, t := range llm.last()["tools"].([]any) {
			names = append(names, t.(map[string]any)["function"].(map[string]any)["name"].(string))
		}
		Expect(names).To(ContainElement("read_document"))
	})
})
