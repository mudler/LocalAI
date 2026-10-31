package e2echats_test

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// photo is the start of a PNG file: enough for the type sniffer.
var photo = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"

// An attached photo and a cloud model, through the real server and the real
// cloud-proxy backend. Only the upstream model is faked.
var _ = Describe("Chats images and a cloud model", Ordered, func() {
	var (
		in *instance
		c  *client
	)
	BeforeAll(func() {
		in = newInstance()
		// A passthrough cloud model that declares it can look at images.
		vl := proxyModel("fake-vl", llm.URL(), map[string]any{"known_usecases": []string{"chat", "vision"}})
		vl["proxy"].(map[string]any)["mode"] = "passthrough"
		in.writeModel("fake-vl", vl)
		in.Start()
		c = &client{base: in.URL()}
	})
	AfterAll(func() { in.Cleanup() })
	BeforeEach(func() { llm.Reset() })

	attach := func(id, text, name, body string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("text", text)
		fw, err := mw.CreateFormFile("files", name)
		Expect(err).ToNot(HaveOccurred())
		_, _ = fw.Write([]byte(body))
		Expect(mw.Close()).To(Succeed())
		resp, err := http.DefaultClient.Do(c.req(http.MethodPost, "/api/chats/"+id+"/messages", &buf, mw.FormDataContentType()))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(raw))
	}
	requestWith := func(marker string) fakeReq {
		var found fakeReq
		for _, r := range llm.Requests() {
			if strings.Contains(r.LastUser(), marker) {
				found = r
			}
		}
		Expect(found.Raw).ToNot(BeEmpty(), "no model request for %s", marker)
		return found
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(photo))

	It("sends the photo to a cloud model that can look at images, once", func() {
		ch := c.create("fake-vl")
		attach(ch.ID, "VISION-1 whats in this photo", "photo.png", photo)
		Expect(c.settled(ch.ID)).To(Equal("done"))
		first := requestWith("VISION-1")
		Expect(first.Raw).To(ContainSubstring(`"type":"image_url"`))
		Expect(first.Raw).To(ContainSubstring("data:image/png;base64," + b64))
		Expect(first.Prompt()).To(ContainSubstring("Images: you see the images attached to the user's message"))

		Expect(c.say(ch.ID, "VISION-2 thanks")).To(Equal("done"))
		second := requestWith("VISION-2")
		Expect(second.Raw).ToNot(ContainSubstring(b64), "the photo of the first message went again")
		Expect(second.Prompt()).To(ContainSubstring("It is not sent again"))
	})

	It("describes the photo for a text-only chat with the model that can look at images", func() {
		var list map[string]any
		Expect(c.get("/api/chats", &list)).To(Equal(http.StatusOK))
		Expect(list).To(HaveKeyWithValue("image_model", "fake-vl"))

		llm.Handle(describeScript("VISION-3", "a red square"))
		ch := c.create("fake")
		es := c.events(ch.ID)
		defer es.Close()
		attach(ch.ID, "VISION-3 whats in this photo", "photo.png", photo)
		Expect(c.settled(ch.ID)).To(Equal("done"))
		es.Wait("turn_end")
		var described fakeReq
		for _, r := range llm.Requests() {
			if r.Model == "fake-vl" {
				described = r
			}
			if strings.Contains(r.Prompt(), "VISION-3") {
				Expect(r.Raw).ToNot(ContainSubstring(`"type":"image_url"`), "a text-only model got the image")
				Expect(r.Prompt()).To(ContainSubstring("Images: you cannot see images. To look at an image"))
			}
		}
		Expect(described.Raw).To(ContainSubstring("data:image/png;base64,"+b64), "describe_image did not send the photo to fake-vl")
		Expect(toolOutput(es, "describe_image")).To(Equal("a red square"))
	})
})

// describeScript calls describe_image on the photo attached to the message
// with marker, then answers. A request without the marker is the describing
// model's: it answers with answer.
func describeScript(marker, answer string) func(fakeReq) fakeStep {
	path := regexp.MustCompile(`(?m)^- (/workspace/.*photo\.png)$`)
	return func(r fakeReq) fakeStep {
		p := r.Prompt()
		if !strings.Contains(p, marker) {
			return fakeStep{Text: answer}
		}
		if m := path.FindStringSubmatch(p); m != nil && llm.CountWith(marker) == 1 {
			return fakeStep{Calls: []fakeCall{{Name: "describe_image", Args: `{"path":"` + m[1] + `"}`}}}
		}
		return fakeStep{Text: "seen"}
	}
}

// toolOutput is the output of the first run of a tool in the events.
func toolOutput(es *eventStream, name string) string {
	for _, ev := range es.All() {
		if ev.Kind == "tool_end" && ev.Tool != nil && ev.Tool.Name == name {
			return ev.Tool.Output
		}
	}
	Fail("no " + name + " in the events")
	return ""
}

var _ = Describe("Chats images with no model that can look at them", Ordered, func() {
	var (
		in *instance
		c  *client
	)
	BeforeAll(func() {
		in = newInstance()
		in.Start()
		c = &client{base: in.URL()}
	})
	AfterAll(func() { in.Cleanup() })
	BeforeEach(func() { llm.Reset() })

	It("tells the model and the person what to do", func() {
		var list map[string]any
		Expect(c.get("/api/chats", &list)).To(Equal(http.StatusOK))
		Expect(list).ToNot(HaveKey("image_model"))

		llm.Handle(describeScript("VISION-4", "unused"))
		ch := c.create("fake")
		es := c.events(ch.ID)
		defer es.Close()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("text", "VISION-4 whats in this photo")
		fw, err := mw.CreateFormFile("files", "photo.png")
		Expect(err).ToNot(HaveOccurred())
		_, _ = fw.Write([]byte(photo))
		Expect(mw.Close()).To(Succeed())
		resp, err := http.DefaultClient.Do(c.req(http.MethodPost, "/api/chats/"+ch.ID+"/messages", &buf, mw.FormDataContentType()))
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(c.settled(ch.ID)).To(Equal("done"))
		es.Wait("turn_end")

		Expect(toolOutput(es, "describe_image")).To(ContainSubstring(
			"No vision model: choose a model that can look at images for this chat, " +
				"or an administrator can install one from the model gallery and choose it in Settings under Chat image model."))
		Expect(llm.Requests()[0].Prompt()).To(ContainSubstring("Images: you cannot see images, and no model here can describe them."))
	})
})
