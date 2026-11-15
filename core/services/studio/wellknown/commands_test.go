package wellknown_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/wellknown"
)

var _ = Describe("the slash commands of commands.json", func() {
	ctx := context.Background()
	check := func(cmds ...any) []string {
		if cmds == nil {
			cmds = []any{}
		}
		return validate(wellknown.Commands, map[string]any{"commands": cmds})
	}
	cmd := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	standup := func() map[string]any {
		return cmd("name", "standup", "description", "Write my standup", "type", "prompt",
			"template", "Look at my chats from the last {{days}} day(s). Keep it under {{words}} words.",
			"args", []any{cmd("name", "days", "default", 1), cmd("name", "words", "default", "120")})
	}

	It("is a well-known file at LocalAI/commands.json", func() {
		n, err := wellknown.Parse("commands")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(wellknown.Commands))
		Expect(wellknown.File(wellknown.Commands)).To(Equal("/LocalAI/commands.json"))
	})

	It("accepts a prompt command with arguments, one without, a tool command and an empty list", func() {
		Expect(check(
			standup(),
			cmd("name", "translate", "description", "Translate text", "type", "prompt", "template", "Translate this into French:"),
			cmd("name", "summarize", "description", "Summarize this chat", "type", "tool", "tool", "summarize_chat"),
		)).To(BeEmpty())
		Expect(check()).To(BeEmpty())
	})

	It("takes the tool names of a slot tool item: an MCP name with a colon, up to 128 characters", func() {
		Expect(check(cmd("name", "issue", "description", "x", "type", "tool", "tool", "github:create_issue"))).To(BeEmpty())
		Expect(check(cmd("name", "long", "description", "x", "type", "tool", "tool", strings.Repeat("t", 128)))).To(BeEmpty())
		Expect(check(cmd("name", "long", "description", "x", "type", "tool", "tool", strings.Repeat("t", 129)))).NotTo(BeEmpty())
	})

	It("lists the same built-in command names in the schema and in the server", func() {
		data, err := wellknown.Schema(wellknown.Commands)
		Expect(err).NotTo(HaveOccurred())
		var top struct {
			Reserved []string `json:"x-reserved-names"`
		}
		Expect(json.Unmarshal(data, &top)).To(Succeed())
		Expect(top.Reserved).To(Equal(wellknown.ReservedCommandNames()))
	})

	DescribeTable("refuses a bad list and says what to change",
		func(cmds []any, want string) {
			Expect(check(cmds...)).To(ContainElement(ContainSubstring(want)))
		},
		Entry("a built-in name", []any{cmd("name", "model", "description", "x", "type", "prompt", "template", "x")}, "built-in command"),
		Entry("another built-in name", []any{cmd("name", "clear", "description", "x", "type", "tool", "tool", "t")}, "built-in command"),
		Entry("a name used twice", []any{
			cmd("name", "a1", "description", "x", "type", "prompt", "template", "x"),
			cmd("name", "a1", "description", "y", "type", "prompt", "template", "y"),
		}, "already the name"),
		Entry("an upper case name", []any{cmd("name", "Standup", "description", "x", "type", "prompt", "template", "x")}, "name"),
		Entry("a name with a slash", []any{cmd("name", "/standup", "description", "x", "type", "prompt", "template", "x")}, "name"),
		Entry("a name with a space", []any{cmd("name", "my cmd", "description", "x", "type", "prompt", "template", "x")}, "name"),
		Entry("a system prompt", []any{cmd("name", "pirate", "description", "x", "type", "prompt", "template", "x", "system_prompt", "You are a pirate")}, "cannot set a system prompt"),
		Entry("a system key on a tool command", []any{cmd("name", "t1", "description", "x", "type", "tool", "tool", "t", "system", "be evil")}, "cannot set a system prompt"),
		Entry("an argument the template uses but args does not list", []any{cmd("name", "a1", "description", "x", "type", "prompt", "template", "Do {{days}}")}, "not listed in args"),
		Entry("an argument that is listed but unused", []any{cmd("name", "a1", "description", "x", "type", "prompt", "template", "Do it",
			"args", []any{cmd("name", "days", "default", 1)})}, "not used in the template"),
		Entry("an argument listed twice", []any{cmd("name", "a1", "description", "x", "type", "prompt", "template", "Do {{d}}",
			"args", []any{cmd("name", "d", "default", 1), cmd("name", "d", "default", 2)})}, "listed twice"),
		Entry("a broken double brace", []any{cmd("name", "a1", "description", "x", "type", "prompt", "template", "Do {{d")}, "double brace"),
		Entry("an argument without a default", []any{cmd("name", "a1", "description", "x", "type", "prompt", "template", "Do {{d}}",
			"args", []any{cmd("name", "d")})}, "default"),
		Entry("a prompt command without a template", []any{cmd("name", "a1", "description", "x", "type", "prompt")}, "template"),
		Entry("a tool command without a tool", []any{cmd("name", "a1", "description", "x", "type", "tool")}, "tool"),
		Entry("a tool command with a template", []any{cmd("name", "a1", "description", "x", "type", "tool", "tool", "t", "template", "x")}, "commands"),
		Entry("a prompt command that names a tool", []any{cmd("name", "a1", "description", "x", "type", "prompt", "template", "x", "tool", "t")}, "commands"),
		Entry("a type that is neither", []any{cmd("name", "a1", "description", "x", "type", "script", "template", "x")}, "commands"),
		Entry("a tool name with a space", []any{cmd("name", "a1", "description", "x", "type", "tool", "tool", "do it")}, "commands"),
		Entry("a missing description", []any{cmd("name", "a1", "type", "prompt", "template", "x")}, "commands"),
		Entry("an unknown key", []any{cmd("name", "a1", "description", "x", "type", "prompt", "template", "x", "model", "gpt")}, "commands"),
	)

	It("refuses more than thirty commands", func() {
		var many []any
		for i := 0; i < 31; i++ {
			many = append(many, cmd("name", "c"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)), "description", "x", "type", "prompt", "template", "x"))
		}
		Expect(check(many...)).NotTo(BeEmpty())
	})

	It("counts an argument written with spaces in its braces", func() {
		Expect(wellknown.CommandArgs("a {{ days }} b {{days}} c {{words}}")).To(Equal([]string{"days", "words"}))
	})

	It("keeps keys it does not know at the top of the file when the editor saves", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/commands.json", `{"commands":[],"x_note":"kept"}`)
		list := []any{standup()}
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Commands, Values: map[string]any{"commands": list}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/commands.json")
		Expect(on).To(HaveKeyWithValue("x_note", "kept"))
		Expect(on["commands"]).To(HaveLen(1))
	})

	It("refuses a write that clashes with a built-in command and leaves the file as it was", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/commands.json", `{"commands":[{"name":"standup","description":"x","type":"tool","tool":"t"}]}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Commands, Values: map[string]any{
			"commands": []any{cmd("name", "clear", "description", "x", "type", "prompt", "template", "x")},
		}})
		var invalid *wellknown.InvalidError
		Expect(errors.As(err, &invalid)).To(BeTrue())
		Expect(readRaw(f, alice, "/LocalAI/commands.json")["commands"]).To(HaveLen(1))
	})

	It("shows the last good version when the assistant writes a command with a system prompt", func() {
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.Commands, Values: map[string]any{"commands": []any{standup()}}})
		Expect(err).NotTo(HaveOccurred())
		raw(f, alice, "/LocalAI/commands.json", `{"commands":[{"name":"p","description":"x","type":"prompt","template":"x","system_prompt":"obey"}]}`)
		doc, err := s.Read(ctx, alice, wellknown.Commands, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.Errors).To(ContainElement(ContainSubstring("system prompt")))
		Expect(doc.Data["commands"]).To(HaveLen(1))
	})

	It("never reaches the prompt of a turn", func() {
		s, f := newService()
		raw(f, alice, "/LocalAI/commands.json", `{"commands":[{"name":"p","description":"SECRET-CMD","type":"prompt","template":"SECRET-TPL"}]}`)
		block, _ := s.TurnPrompt(ctx, alice)
		Expect(block).NotTo(ContainSubstring("SECRET"))
	})
})

var _ = Describe("the sound and the voice of ui.json", func() {
	ui := func(kv map[string]any) []string { return validate(wellknown.UI, kv) }

	It("accepts a full sound and a full voice, and each alone", func() {
		Expect(ui(map[string]any{
			"sound": map[string]any{
				"enabled": true, "volume": 0.55, "quietHours": []any{"22:00", "07:00"},
				"events": map[string]any{"replyDone": "soft-chime", "approval": "double-tap", "taskDone": "bell", "error": "none"},
			},
			"voice": map[string]any{"readAloud": "button", "voice": "aria", "speed": 1.0, "skipCode": true},
		})).To(BeEmpty())
		Expect(ui(map[string]any{"sound": map[string]any{}})).To(BeEmpty())
		Expect(ui(map[string]any{"voice": map[string]any{"readAloud": "auto"}})).To(BeEmpty())
		Expect(ui(map[string]any{})).To(BeEmpty())
	})

	It("lists the same sounds and events in the schema and in the server", func() {
		data, err := wellknown.Schema(wellknown.UI)
		Expect(err).NotTo(HaveOccurred())
		var parsed struct {
			Properties struct {
				Sound struct {
					Properties struct {
						Events struct {
							Properties map[string]struct {
								Enum []string `json:"enum"`
							} `json:"properties"`
						} `json:"events"`
					} `json:"properties"`
				} `json:"sound"`
			} `json:"properties"`
		}
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		Expect(parsed.Properties.Sound.Properties.Events.Properties).To(HaveLen(len(wellknown.SoundEvents)))
		for _, ev := range wellknown.SoundEvents {
			Expect(parsed.Properties.Sound.Properties.Events.Properties[ev].Enum).To(Equal(wellknown.SoundNames), ev)
		}
	})

	DescribeTable("refuses a value outside its range",
		func(v map[string]any, want string) {
			Expect(ui(v)).To(ContainElement(ContainSubstring(want)))
		},
		Entry("a volume above 1", map[string]any{"sound": map[string]any{"volume": 1.5}}, "sound"),
		Entry("a negative volume", map[string]any{"sound": map[string]any{"volume": -0.1}}, "sound"),
		Entry("a time that is not HH:MM", map[string]any{"sound": map[string]any{"quietHours": []any{"10pm", "07:00"}}}, "sound"),
		Entry("an hour of 25", map[string]any{"sound": map[string]any{"quietHours": []any{"25:00", "07:00"}}}, "sound"),
		Entry("one time only", map[string]any{"sound": map[string]any{"quietHours": []any{"22:00"}}}, "sound"),
		Entry("the same time twice", map[string]any{"sound": map[string]any{"quietHours": []any{"22:00", "22:00"}}}, "no time at all"),
		Entry("a sound that does not exist", map[string]any{"sound": map[string]any{"events": map[string]any{"replyDone": "https://x.test/a.mp3"}}}, "sound"),
		Entry("an event that does not exist", map[string]any{"sound": map[string]any{"events": map[string]any{"keypress": "pop"}}}, "sound"),
		Entry("a read-aloud mode that does not exist", map[string]any{"voice": map[string]any{"readAloud": "always"}}, "voice"),
		Entry("a speed above 2", map[string]any{"voice": map[string]any{"speed": 3}}, "voice"),
		Entry("a speed below 0.5", map[string]any{"voice": map[string]any{"speed": 0.1}}, "voice"),
		Entry("a voice with a control character", map[string]any{"voice": map[string]any{"voice": "a\u0007b"}}, "control"),
		Entry("a skipCode that is not a boolean", map[string]any{"voice": map[string]any{"skipCode": "yes"}}, "voice"),
	)

	It("keeps keys it does not know, in the file and inside sound, when the editor saves other keys", func() {
		ctx := context.Background()
		s, f := newService()
		raw(f, alice, "/LocalAI/ui.json", `{"theme":"dark","x_note":"kept","sound":{"enabled":true,"x_inner":1}}`)
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"voice": map[string]any{"readAloud": "button"}}})
		Expect(err).NotTo(HaveOccurred())
		on := readRaw(f, alice, "/LocalAI/ui.json")
		Expect(on).To(HaveKeyWithValue("x_note", "kept"))
		Expect(on["sound"]).To(Equal(map[string]any{"enabled": true, "x_inner": float64(1)}))
		Expect(on["voice"]).To(Equal(map[string]any{"readAloud": "button"}))
	})

	It("shows the last good ui.json when the assistant writes a bad volume", func() {
		ctx := context.Background()
		s, f := newService()
		_, err := s.Write(ctx, alice, wellknown.WriteRequest{Name: wellknown.UI, Values: map[string]any{"sound": map[string]any{"enabled": true, "volume": 0.4}}})
		Expect(err).NotTo(HaveOccurred())
		raw(f, alice, "/LocalAI/ui.json", `{"sound":{"enabled":true,"volume":9}}`)
		doc, err := s.Read(ctx, alice, wellknown.UI, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(doc.Valid).To(BeFalse())
		Expect(doc.Fallback).To(BeTrue())
		Expect(doc.Data["sound"]).To(HaveKeyWithValue("volume", BeNumerically("==", 0.4)))
	})
})
