package studio

import (
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/providerguard"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The text that the prompt builders and the Tools menu reasons produce names
// the add-on that runs commands by the title it declares, never by a name that
// core knows. The names come from addons/*/manifest.json at run time.
var _ = Describe("the prompts that talk about the add-on that runs commands", func() {
	It("name no shipped provider, with a declared title or without one", func() {
		names, err := providerguard.Names()
		Expect(err).NotTo(HaveOccurred())
		var words []string
		for _, n := range names {
			// A short name can be an ordinary word (web), so it is checked as a title only.
			if len(n) >= 6 || strings.ContainsAny(n, "- ") {
				words = append(words, regexp.QuoteMeta(strings.ToLower(n)))
			}
		}
		re := regexp.MustCompile(`(^|[^\w-])(` + strings.Join(words, "|") + `)($|[^\w-])`)

		var texts []string
		for _, base := range []Commands{{Title: "Zeta Runner"}, {}} {
			for _, st := range []CommandState{CommandsAvailable, CommandsOff, CommandsUnavailable, CommandsNone, CommandsSwitchedOff} {
				for _, iso := range []string{"isolated", "host", ""} {
					c := base
					c.State, c.Isolation, c.Tool, c.Reason, c.Consent = st, iso, "bash", "stopped", "ask"
					texts = append(texts, shellReason(c, ""), commandsBlock(c, true, true, true, session.Place{}))
				}
			}
		}
		for _, p := range []egress.Preset{egress.PresetOff, egress.PresetRegistries, egress.PresetAll} {
			for _, flag := range []bool{true, false} {
				reg := egress.BuiltinRegistries()
				texts = append(texts, egress.Describe(p, reg, flag, "Zeta Runner"), egress.DescribeLimit(p, egress.PresetOff, reg, flag, "Zeta Runner"), egress.DescribeLimit(p, egress.PresetRegistries, reg, flag, "Zeta Runner"), egress.Hint(p, flag, "Zeta Runner"))
			}
		}
		texts = append(texts, scopedHostBashReason, hostBashDescription)
		// "GitHub releases" is the service that a package registry preset
		// allows, not the add-on of the same name.
		service := strings.NewReplacer("github releases and archives", "releases and archives")
		for _, text := range texts {
			Expect(re.FindStringSubmatch(service.Replace(strings.ToLower(text)))).To(BeNil(), "a prompt names a shipped add-on: %q", text)
		}
	})

	It("carries the declared title, or says generically what runs commands", func() {
		Expect(shellReason(Commands{State: CommandsOff, Title: "Zeta Runner"}, "")).To(ContainSubstring("the Zeta Runner add-on"))
		Expect(shellReason(Commands{State: CommandsUnavailable, Title: "Zeta Runner", Reason: "no vm"}, "")).To(ContainSubstring("the Zeta Runner add-on cannot run commands here: no vm"))
		Expect(shellReason(Commands{State: CommandsOff}, "")).To(ContainSubstring("the add-on that runs commands"))
	})
})
