package localaitools

import (
	"encoding/json"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// clientBudgetBytes bounds the tools/list entries of the client group (name,
// description and input schema of every tool, as JSON).
//
// Every tool of an always-sent group is paid on every turn of every chat, by
// every user, whether or not the turn needs it. On a small context window
// that cost decides whether the chat fits at all. The value is the measured
// size after the merges and trims (6,948 bytes, about 1,750 tokens) plus
// about 1%: the voice_profile tool took 613 bytes of the 5,910 before it. Raise
// it only with a reason in the commit message: a new tool must justify its place first, and
// an operation that shares state with an existing tool belongs in that tool
// as an action (see .agents/localai-assistant-mcp.md).
//
// 6,600 to 7,000: list_models (345 bytes). Without it a user who is not an
// administrator cannot learn the names of the models the other tools take,
// and guesses them. It is the one listing tool for every user, so it is not
// a candidate for a merge: it shares no state with a generation tool.
//
// 7,000 to 7,300: generate_image takes seed, an init image and reference
// images (about 240 bytes). They are arguments of the one image route the API
// already has, so they cost no new tool.
//
// 7,300 to 8,000: generate_video reads what the /video route reads and the
// tool did not send: end_image, audio, ref_images, ref_videos, ref_audios,
// seed, cfg_scale, steps (about 700 bytes). ref_videos is a list of frame
// lists because that is how the route takes a video.
//
// 8,000 to 8,550: generate_sound takes the ten fields of the route that a
// song model reads: caption, bpm, keyscale, timesignature, language,
// vocal_language, think, do_sample, prompt_influence, expansion_model (about
// 480 bytes).
//
// 8,550 to 8,650: speak takes sample_rate, which the TTS route reads (about
// 60 bytes).
//
// 8,650 to 8,900: voice_profile create takes description and extra_clips,
// because the API takes up to ten clips and a description (about 210 bytes).
//
// 8,900 to 9,250: detect takes points and boxes (segmentation prompts), and
// voice and face take anti_spoofing for verify (about 340 bytes together).
// All three are fields their routes read.
const clientBudgetBytes = 9250

var _ = Describe("Client tool surface", func() {
	It("stays inside the byte budget of an always-sent group", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
		DeferCleanup(done)
		res, err := sess.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		total := 0
		for _, t := range res.Tools {
			if ToolGroupOf(t.Name) != GroupClient {
				continue
			}
			raw, err := json.Marshal(t)
			Expect(err).ToNot(HaveOccurred())
			total += len(raw)
		}
		GinkgoWriter.Printf("client group: %d bytes (budget %d)\n", total, clientBudgetBytes)
		Expect(total).To(BeNumerically("<=", clientBudgetBytes),
			"the client group grew past its budget; merge or trim before raising it")
	})

	It("keeps the action tools flat, with an action enum and no oneOf or anyOf", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
		DeferCleanup(done)
		res, err := sess.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		seen := 0
		for _, t := range res.Tools {
			if t.Name != ToolVoice && t.Name != ToolFace && t.Name != ToolVoiceProfile {
				continue
			}
			seen++
			raw, _ := json.Marshal(t.InputSchema)
			var m map[string]any
			Expect(json.Unmarshal(raw, &m)).To(Succeed())
			Expect(m).ToNot(HaveKey("oneOf"), t.Name)
			Expect(m).ToNot(HaveKey("anyOf"), t.Name)
			Expect(m["type"]).To(Equal("object"))
			action := m["properties"].(map[string]any)["action"].(map[string]any)
			if t.Name == ToolVoiceProfile {
				Expect(action["enum"]).To(ConsistOf("create", "list", "delete"))
			} else {
				Expect(action["enum"]).To(ConsistOf("verify", "identify"))
			}
			Expect(m["required"]).To(ContainElement("action"))
		}
		Expect(seen).To(Equal(3))
	})

	It("has no edit_image, verify_* or identify_* tool any more", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
		DeferCleanup(done)
		names := listToolNames(ctx, sess)
		for _, gone := range []string{"edit_image", "verify_voice", "identify_voice", "verify_face", "identify_face"} {
			Expect(slices.Contains(names, gone)).To(BeFalse(), gone)
		}
	})

	Describe("generate_image", func() {
		It("makes a new image without image and mask", func() {
			fc := &fakeClient{}
			ctx, sess, done := connectInMemory(fc, Options{})
			DeferCleanup(done)
			res := callTool(ctx, sess, ToolGenerateImage, map[string]any{"prompt": "a fox", "n": 2})
			Expect(res.IsError).To(BeFalse(), resultText(res))
			calls := fc.recorded()
			Expect(calls).To(HaveLen(1))
			Expect(calls[0].method).To(Equal("GenerateImage"))
			Expect(calls[0].args.(GenerateImageRequest).N).To(Equal(2))
		})

		It("repaints with image and mask", func() {
			fc := &fakeClient{}
			ctx, sess, done := connectInMemory(fc, Options{})
			DeferCleanup(done)
			res := callTool(ctx, sess, ToolGenerateImage, map[string]any{"prompt": "a hat", "image": "a.png", "mask": "m.png", "steps": 5})
			Expect(res.IsError).To(BeFalse(), resultText(res))
			calls := fc.recorded()
			Expect(calls).To(HaveLen(1))
			Expect(calls[0].method).To(Equal("EditImage"))
			Expect(calls[0].args).To(Equal(EditImageRequest{Prompt: "a hat", Image: "a.png", Mask: "m.png", Steps: 5}))
		})

		It("starts from an image without a mask (img2img) and passes seed and references", func() {
			fc := &fakeClient{}
			ctx, sess, done := connectInMemory(fc, Options{})
			DeferCleanup(done)
			res := callTool(ctx, sess, ToolGenerateImage, map[string]any{"prompt": "x", "image": "a.png", "seed": 7, "ref_images": []string{"r1.png", "r2.png"}})
			Expect(res.IsError).To(BeFalse(), resultText(res))
			calls := fc.recorded()
			Expect(calls).To(HaveLen(1))
			Expect(calls[0].method).To(Equal("GenerateImage"))
			seed := 7
			Expect(calls[0].args).To(Equal(GenerateImageRequest{Prompt: "x", Image: "a.png", Seed: &seed, RefImages: []string{"r1.png", "r2.png"}}))
		})

		It("refuses a mask without an image, and a seed or references with a mask, and calls nothing", func() {
			fc := &fakeClient{}
			ctx, sess, done := connectInMemory(fc, Options{})
			DeferCleanup(done)
			res := callTool(ctx, sess, ToolGenerateImage, map[string]any{"prompt": "x", "image": "a.png", "mask": "m.png", "seed": 1})
			Expect(res.IsError).To(BeTrue())
			Expect(resultText(res)).To(ContainSubstring("do not work with mask"))
			res = callTool(ctx, sess, ToolGenerateImage, map[string]any{"prompt": "x", "mask": "m.png"})
			Expect(res.IsError).To(BeTrue())
			Expect(resultText(res)).To(ContainSubstring("mask needs image"))
			Expect(fc.recorded()).To(BeEmpty())
		})
	})

	DescribeTable("the action tools check the fields of each action",
		func(tool string, args map[string]any, method string, wantErr string) {
			fc := &fakeClient{}
			ctx, sess, done := connectInMemory(fc, Options{})
			DeferCleanup(done)
			res := callTool(ctx, sess, tool, args)
			if wantErr != "" {
				Expect(res.IsError).To(BeTrue(), resultText(res))
				Expect(resultText(res)).To(ContainSubstring(wantErr))
				Expect(fc.recorded()).To(BeEmpty())
				return
			}
			Expect(res.IsError).To(BeFalse(), resultText(res))
			Expect(fc.recorded()).To(HaveLen(1))
			Expect(fc.recorded()[0].method).To(Equal(method))
		},
		Entry("voice verify", ToolVoice, map[string]any{"action": "verify", "audio": "a.wav", "audio2": "b.wav"}, "VerifyVoice", ""),
		Entry("voice identify", ToolVoice, map[string]any{"action": "identify", "audio": "a.wav", "top_k": 3}, "IdentifyVoice", ""),
		Entry("voice verify without audio2", ToolVoice, map[string]any{"action": "verify", "audio": "a.wav"}, "", `voice: action "verify" needs audio2`),
		Entry("voice identify without audio", ToolVoice, map[string]any{"action": "identify", "audio": ""}, "", `voice: action "identify" needs audio`),
		Entry("voice with no action", ToolVoice, map[string]any{"audio": "a.wav"}, "", "action"),
		Entry("voice with a bad action", ToolVoice, map[string]any{"action": "clone", "audio": "a.wav"}, "", "does not equal any of"),
		Entry("face verify", ToolFace, map[string]any{"action": "verify", "image": "a.png", "image2": "b.png"}, "VerifyFace", ""),
		Entry("face identify", ToolFace, map[string]any{"action": "identify", "image": "a.png"}, "IdentifyFace", ""),
		Entry("face verify without image2", ToolFace, map[string]any{"action": "verify", "image": "a.png"}, "", `face: action "verify" needs image2`),
		Entry("voice_profile create without a name", ToolVoiceProfile, map[string]any{"action": "create", "audio": "a.wav", "transcript": "x"}, "", `voice_profile: action "create" needs name`),
		Entry("voice_profile create without audio", ToolVoiceProfile, map[string]any{"action": "create", "name": "n", "transcript": "x"}, "", `voice_profile: action "create" needs audio`),
		Entry("voice_profile create without a transcript", ToolVoiceProfile, map[string]any{"action": "create", "name": "n", "audio": "a.wav"}, "", `voice_profile: action "create" needs transcript`),
		Entry("voice_profile create", ToolVoiceProfile, map[string]any{"action": "create", "name": "n", "audio": "a.wav", "transcript": "x"}, "CreateVoiceProfile", ""),
		Entry("voice_profile list", ToolVoiceProfile, map[string]any{"action": "list"}, "ListVoiceProfiles", ""),
		Entry("voice_profile delete without id", ToolVoiceProfile, map[string]any{"action": "delete"}, "", `voice_profile: action "delete" needs id`),
		Entry("voice_profile delete", ToolVoiceProfile, map[string]any{"action": "delete", "id": "abc"}, "DeleteVoiceProfile", ""),
		Entry("voice_profile with a bad action", ToolVoiceProfile, map[string]any{"action": "update"}, "", "does not equal any of"),
		Entry("face with a bad action", ToolFace, map[string]any{"action": "x", "image": "a.png"}, "", "does not equal any of"),
	)

	It("tells the model that a profile of an administrator is visible to all", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
		DeferCleanup(done)
		res := callTool(ctx, sess, ToolVoiceProfile, map[string]any{"action": "list"})
		Expect(res.IsError).To(BeFalse(), resultText(res))
		Expect(resultText(res)).To(ContainSubstring("created by an administrator are visible to all users"))
	})
})
