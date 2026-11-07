package studio

import (
	"fmt"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The named cases say the rules in words; the sweep below checks every
// combination against a second, plainer statement of the same rules.
func TestEffectiveSaveLevelCases(t *testing.T) {
	conn := session.OriginConnector
	cases := []struct {
		name string
		in   SaveInputs
		want string
	}{
		// The person's own chat reads the setting as it is.
		{"chat, unset", SaveInputs{}, SaveAsk},
		{"chat, ask", SaveInputs{User: SaveAsk}, SaveAsk},
		{"chat, auto", SaveInputs{User: SaveAuto}, SaveAuto},
		{"chat, always", SaveInputs{User: SaveAlways}, SaveAlways},
		{"chat, never", SaveInputs{User: SaveNever}, SaveNever},
		{"chat, a value nobody declared reads as ask", SaveInputs{User: "yes"}, SaveAsk},
		// The agent can only be stricter.
		{"agent always over auto", SaveInputs{User: SaveAuto, Agent: agents.MemorySaveAlways}, SaveAlways},
		{"agent always over ask", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAlways}, SaveAlways},
		{"agent never over auto", SaveInputs{User: SaveAuto, Agent: agents.MemorySaveNever}, SaveNever},
		{"agent always does not loosen never", SaveInputs{User: SaveNever, Agent: agents.MemorySaveAlways}, SaveNever},
		{"agent never over always", SaveInputs{User: SaveAlways, Agent: agents.MemorySaveNever}, SaveNever},
		{"an agent value that is not offered fails closed", SaveInputs{User: SaveAuto, Agent: SaveAsk}, SaveAlways},
		// An explicit agent auto is the one loosening, for that agent only.
		{"agent auto over ask", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto}, SaveAuto},
		{"agent auto, person unset", SaveInputs{Agent: agents.MemorySaveAuto}, SaveAuto},
		{"agent auto does not loosen always", SaveInputs{User: SaveAlways, Agent: agents.MemorySaveAuto}, SaveAlways},
		{"agent auto does not loosen never", SaveInputs{User: SaveNever, Agent: agents.MemorySaveAuto}, SaveNever},
		{"agent auto, sub-agent", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, SubAgent: true}, SaveAsk},
		{"agent auto, trigger", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: session.OriginTrigger}, SaveAsk},
		{"agent auto, job", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: session.OriginJob}, SaveAsk},
		{"agent auto lifts the messaging row ask for a paired private chat", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: conn, Messaging: MessagingAsk, Direct: true, Paired: true}, SaveAuto},
		{"agent auto lifts the unset row for a paired private chat", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: conn, Direct: true, Paired: true}, SaveAuto},
		{"agent auto, unpaired sender", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: conn, Direct: true}, SaveAsk},
		{"agent auto, group chat", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: conn, Paired: true}, SaveAsk},
		{"agent auto, sub-agent on a messaging chat", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: conn, SubAgent: true, Direct: true, Paired: true}, SaveAsk},
		{"agent auto does not lift the row never", SaveInputs{User: SaveAsk, Agent: agents.MemorySaveAuto, Origin: conn, Messaging: MessagingNever, Direct: true, Paired: true}, SaveNever},
		{"agent auto does not loosen always on a messaging chat", SaveInputs{User: SaveAlways, Agent: agents.MemorySaveAuto, Origin: conn, Direct: true, Paired: true}, SaveAlways},
		// A messaging chat asks unless the row and the facts say otherwise.
		{"connector, auto, row ask", SaveInputs{User: SaveAuto, Origin: conn}, SaveAsk},
		{"connector, ask, row ask", SaveInputs{User: SaveAsk, Origin: conn, Messaging: MessagingAsk}, SaveAsk},
		{"connector, ask, paired private chat", SaveInputs{User: SaveAsk, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveAuto},
		{"connector, auto, paired private chat", SaveInputs{User: SaveAuto, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveAuto},
		{"connector, paired but a group", SaveInputs{User: SaveAuto, Origin: conn, Messaging: MessagingPairedPrivate, Paired: true}, SaveAsk},
		{"connector, private but not paired", SaveInputs{User: SaveAuto, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true}, SaveAsk},
		{"connector, row never", SaveInputs{User: SaveAuto, Origin: conn, Messaging: MessagingNever, Direct: true, Paired: true}, SaveNever},
		{"connector, never", SaveInputs{User: SaveNever, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveNever},
		{"always is not loosened by the row", SaveInputs{User: SaveAlways, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveAlways},
		{"always, row ask", SaveInputs{User: SaveAlways, Origin: conn, Messaging: MessagingAsk}, SaveAlways},
		{"always, row never", SaveInputs{User: SaveAlways, Origin: conn, Messaging: MessagingNever}, SaveNever},
		{"agent always beats the paired private row", SaveInputs{User: SaveAuto, Agent: agents.MemorySaveAlways, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveAlways},
		{"agent never beats the paired private row", SaveInputs{User: SaveAuto, Agent: agents.MemorySaveNever, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveNever},
		{"an unknown row reads as ask", SaveInputs{User: SaveAuto, Origin: conn, Messaging: "anyone", Direct: true, Paired: true}, SaveAsk},
		// An unattended run has no tool; the rule still holds if one ever had.
		{"trigger, auto", SaveInputs{User: SaveAuto, Origin: session.OriginTrigger}, SaveAsk},
		{"trigger, auto, row paired private", SaveInputs{User: SaveAuto, Origin: session.OriginTrigger, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveAsk},
		{"job, always", SaveInputs{User: SaveAlways, Origin: session.OriginJob}, SaveAlways},
		{"job, never", SaveInputs{User: SaveNever, Origin: session.OriginJob}, SaveNever},
		// A sub-agent is not the person.
		{"sub-agent, auto", SaveInputs{User: SaveAuto, SubAgent: true}, SaveAsk},
		{"sub-agent, never", SaveInputs{User: SaveNever, SubAgent: true}, SaveNever},
		{"sub-agent on a messaging chat, paired private", SaveInputs{User: SaveAuto, SubAgent: true, Origin: conn, Messaging: MessagingPairedPrivate, Direct: true, Paired: true}, SaveAsk},
	}
	for _, c := range cases {
		if got := effectiveSaveLevel(c.in); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// oracle restates the rules without the rank helpers: it lists every rule that
// has a say and keeps the strictest, in the order never, always, ask, auto.
func oracle(in SaveInputs) string {
	user := in.User
	if user != SaveAuto && user != SaveAlways && user != SaveNever {
		user = SaveAsk
	}
	var say []string
	agentAuto := in.Agent == agents.MemorySaveAuto
	switch in.Agent {
	case "", agents.MemorySaveAuto:
	case agents.MemorySaveNever:
		say = append(say, SaveNever)
	default:
		say = append(say, SaveAlways)
	}
	switch in.Origin {
	case "":
		if agentAuto && user != SaveAlways && user != SaveNever {
			user = SaveAuto
		}
		say = append(say, user)
		if in.SubAgent {
			say = append(say, SaveAsk)
		}
	case session.OriginConnector:
		if user == SaveAlways || user == SaveNever {
			say = append(say, user)
		}
		switch {
		case in.Messaging == MessagingNever:
			say = append(say, SaveNever)
		case (in.Messaging == MessagingPairedPrivate || agentAuto) && in.Direct && in.Paired && !in.SubAgent:
			// the one case that saves at once
		default:
			say = append(say, SaveAsk)
		}
		if in.SubAgent {
			say = append(say, SaveAsk)
		}
	default:
		say = append(say, user, SaveAsk)
	}
	for _, level := range []string{SaveNever, SaveAlways, SaveAsk} {
		for _, s := range say {
			if s == level {
				return level
			}
		}
	}
	return SaveAuto
}

func TestEffectiveSaveLevelEveryCombination(t *testing.T) {
	n := 0
	for _, user := range []string{"", SaveAsk, SaveAuto, SaveAlways, SaveNever} {
		for _, agent := range []string{"", agents.MemorySaveAlways, agents.MemorySaveAuto, agents.MemorySaveNever} {
			for _, row := range []string{"", MessagingAsk, MessagingPairedPrivate, MessagingNever} {
				for _, origin := range []string{"", session.OriginConnector, session.OriginTrigger, session.OriginJob} {
					for _, sub := range []bool{false, true} {
						for _, direct := range []bool{false, true} {
							for _, paired := range []bool{false, true} {
								in := SaveInputs{User: user, Agent: agent, Messaging: row, Origin: origin, SubAgent: sub, Direct: direct, Paired: paired}
								got, want := effectiveSaveLevel(in), oracle(in)
								n++
								if got != want {
									t.Errorf("%+v: got %s, want %s", in, got, want)
								}
								// Never looser than the person's own level when
								// that is always or never, whatever else is set.
								if user == SaveAlways && got == SaveAuto || user == SaveAlways && got == SaveAsk {
									t.Errorf("%+v: always was loosened to %s", in, got)
								}
								if user == SaveNever && got != SaveNever {
									t.Errorf("%+v: never was loosened to %s", in, got)
								}
								// Saving at once needs the person's own chat or the
								// whole paired private chat condition.
								if got == SaveAuto && origin != "" && !(origin == session.OriginConnector && direct && paired && (row == MessagingPairedPrivate || agent == agents.MemorySaveAuto)) {
									t.Errorf("%+v: saved at once without the conditions", in)
								}
								// An agent is never looser than the person.
								if (agent == agents.MemorySaveAlways || agent == agents.MemorySaveNever) && levelRank(got) < levelRank(effectiveSaveLevel(SaveInputs{User: user, Messaging: row, Origin: origin, SubAgent: sub, Direct: direct, Paired: paired})) {
									t.Errorf("%+v: the agent loosened the level to %s", in, got)
								}
							}
						}
					}
				}
			}
		}
	}
	if n != 5*4*4*4*2*2*2 {
		t.Fatal(fmt.Sprint("swept ", n))
	}
}

func TestSaveLevelOrder(t *testing.T) {
	order := []string{SaveAuto, SaveAsk, SaveAlways, SaveNever}
	for i, a := range order {
		if levelRank(a) != i || levelName(i) != a {
			t.Errorf("%s has rank %d, want %d", a, levelRank(a), i)
		}
	}
}
