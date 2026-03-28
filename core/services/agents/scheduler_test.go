package agents

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// mockPublisher records all Publish calls for assertions.
type mockPublisher struct {
	calls []publishCall
}

type publishCall struct {
	subject string
	data    any
}

func (m *mockPublisher) Publish(subject string, data any) error {
	m.calls = append(m.calls, publishCall{subject: subject, data: data})
	return nil
}

// mockSchedulerStore implements SchedulerStore for testing.
type mockSchedulerStore struct {
	configs    []AgentConfigRecord
	lastRunErr error
	updated    []lastRunUpdate
}

type lastRunUpdate struct {
	userID string
	name   string
}

func (m *mockSchedulerStore) ListConfigs(userID string) ([]AgentConfigRecord, error) {
	if userID == "" {
		return m.configs, nil
	}
	var filtered []AgentConfigRecord
	for _, c := range m.configs {
		if c.UserID == userID {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}

func (m *mockSchedulerStore) UpdateLastRun(userID, name string) error {
	m.updated = append(m.updated, lastRunUpdate{userID: userID, name: name})
	return m.lastRunErr
}

var _ = Describe("AgentScheduler", func() {

	// -----------------------------------------------------------------------
	// parseInterval
	// -----------------------------------------------------------------------
	Describe("parseInterval", func() {
		It("returns 10m for empty string", func() {
			Expect(parseInterval("")).To(Equal(10 * time.Minute))
		})

		It("parses valid duration strings", func() {
			Expect(parseInterval("5m")).To(Equal(5 * time.Minute))
			Expect(parseInterval("1h")).To(Equal(1 * time.Hour))
			Expect(parseInterval("30s")).To(Equal(30 * time.Second))
			Expect(parseInterval("2h30m")).To(Equal(2*time.Hour + 30*time.Minute))
		})

		It("returns 10m for invalid strings", func() {
			Expect(parseInterval("not-a-duration")).To(Equal(10 * time.Minute))
			Expect(parseInterval("abc")).To(Equal(10 * time.Minute))
		})

		It("returns 10m for zero duration", func() {
			Expect(parseInterval("0s")).To(Equal(10 * time.Minute))
		})

		It("returns 10m for negative duration", func() {
			Expect(parseInterval("-5m")).To(Equal(10 * time.Minute))
		})
	})

	// -----------------------------------------------------------------------
	// isDue (via IsDueExported)
	// -----------------------------------------------------------------------
	Describe("isDue", func() {
		It("returns true when lastRun is nil", func() {
			Expect(IsDueExported(nil, 10*time.Minute)).To(BeTrue())
		})

		It("returns true when enough time has elapsed", func() {
			past := time.Now().Add(-15 * time.Minute)
			Expect(IsDueExported(&past, 10*time.Minute)).To(BeTrue())
		})

		It("returns false when not enough time has elapsed", func() {
			recent := time.Now().Add(-3 * time.Minute)
			Expect(IsDueExported(&recent, 10*time.Minute)).To(BeFalse())
		})

		It("returns true when exactly at the interval boundary", func() {
			exactly := time.Now().Add(-10 * time.Minute)
			Expect(IsDueExported(&exactly, 10*time.Minute)).To(BeTrue())
		})

		It("returns true with zero interval (always due)", func() {
			recent := time.Now().Add(-1 * time.Second)
			Expect(IsDueExported(&recent, 0)).To(BeTrue())
		})
	})

	// -----------------------------------------------------------------------
	// runDueAgents — test the scheduling logic with mocks
	// -----------------------------------------------------------------------
	Describe("runDueAgents", func() {
		var (
			pub   *mockPublisher
			mStore *mockSchedulerStore
			sched *AgentScheduler
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			pub = &mockPublisher{}
			mStore = &mockSchedulerStore{}
			sched = NewAgentScheduler(db, pub, mStore, "agent.execute")
		})

		It("publishes event for a due standalone agent", func() {
			past := time.Now().Add(-15 * time.Minute)
			cfg := AgentConfig{
				StandaloneJob: true,
				PeriodicRuns:  "10m",
				Model:         "gpt-4",
			}
			cfgJSON, err := json.Marshal(cfg)
			Expect(err).ToNot(HaveOccurred())

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-1",
					UserID:    "user-1",
					Name:      "background-agent",
					ConfigJSON: string(cfgJSON),
					Status:    StatusActive,
					LastRunAt: &past,
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(HaveLen(1))
			Expect(pub.calls[0].subject).To(Equal("agent.execute"))

			evt, ok := pub.calls[0].data.(AgentChatEvent)
			Expect(ok).To(BeTrue())
			Expect(evt.AgentName).To(Equal("background-agent"))
			Expect(evt.UserID).To(Equal("user-1"))
			Expect(evt.Role).To(Equal(RoleSystem))
			Expect(evt.Config).ToNot(BeNil())
			Expect(evt.Config.Model).To(Equal("gpt-4"))
		})

		It("skips agents that are not due yet", func() {
			recent := time.Now().Add(-2 * time.Minute)
			cfg := AgentConfig{
				StandaloneJob: true,
				PeriodicRuns:  "10m",
			}
			cfgJSON, _ := json.Marshal(cfg)

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-2",
					UserID:    "user-1",
					Name:      "not-yet-agent",
					ConfigJSON: string(cfgJSON),
					Status:    StatusActive,
					LastRunAt: &recent,
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(BeEmpty())
		})

		It("skips non-standalone agents", func() {
			past := time.Now().Add(-15 * time.Minute)
			cfg := AgentConfig{
				StandaloneJob: false,
				PeriodicRuns:  "10m",
			}
			cfgJSON, _ := json.Marshal(cfg)

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-3",
					UserID:    "user-1",
					Name:      "interactive-agent",
					ConfigJSON: string(cfgJSON),
					Status:    StatusActive,
					LastRunAt: &past,
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(BeEmpty())
		})

		It("skips paused agents", func() {
			past := time.Now().Add(-15 * time.Minute)
			cfg := AgentConfig{
				StandaloneJob: true,
				PeriodicRuns:  "10m",
			}
			cfgJSON, _ := json.Marshal(cfg)

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-4",
					UserID:    "user-1",
					Name:      "paused-agent",
					ConfigJSON: string(cfgJSON),
					Status:    StatusPaused,
					LastRunAt: &past,
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(BeEmpty())
		})

		It("skips agents with invalid config JSON", func() {
			past := time.Now().Add(-15 * time.Minute)

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-5",
					UserID:    "user-1",
					Name:      "broken-agent",
					ConfigJSON: `{invalid json`,
					Status:    StatusActive,
					LastRunAt: &past,
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(BeEmpty())
		})

		It("updates last run timestamp after publishing", func() {
			cfg := AgentConfig{
				StandaloneJob: true,
				PeriodicRuns:  "10m",
			}
			cfgJSON, _ := json.Marshal(cfg)

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-6",
					UserID:    "user-1",
					Name:      "track-agent",
					ConfigJSON: string(cfgJSON),
					Status:    StatusActive,
					LastRunAt: nil, // never run — due immediately
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(HaveLen(1))
			Expect(mStore.updated).To(HaveLen(1))
			Expect(mStore.updated[0].userID).To(Equal("user-1"))
			Expect(mStore.updated[0].name).To(Equal("track-agent"))
		})

		It("enriches event with skills when skill provider is set", func() {
			cfg := AgentConfig{
				StandaloneJob: true,
				PeriodicRuns:  "10m",
				EnableSkills:  true,
			}
			cfgJSON, _ := json.Marshal(cfg)

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-7",
					UserID:    "user-1",
					Name:      "skilled-agent",
					ConfigJSON: string(cfgJSON),
					Status:    StatusActive,
					LastRunAt: nil,
				},
			}

			skills := []SkillInfo{
				{Name: "search", Description: "Search the web"},
				{Name: "code", Description: "Write code"},
			}
			provider := func(userID string) ([]SkillInfo, error) {
				return skills, nil
			}
			sched.skillProvider = provider

			sched.runDueAgents()

			Expect(pub.calls).To(HaveLen(1))
			evt, ok := pub.calls[0].data.(AgentChatEvent)
			Expect(ok).To(BeTrue())
			Expect(evt.Skills).To(HaveLen(2))
			Expect(evt.Skills[0].Name).To(Equal("search"))
			Expect(evt.Skills[1].Name).To(Equal("code"))
		})

		It("handles multiple agents in a single run", func() {
			past := time.Now().Add(-15 * time.Minute)
			cfg := AgentConfig{
				StandaloneJob: true,
				PeriodicRuns:  "10m",
			}
			cfgJSON, _ := json.Marshal(cfg)

			mStore.configs = []AgentConfigRecord{
				{
					ID:         "rec-a",
					UserID:     "user-1",
					Name:       "agent-a",
					ConfigJSON: string(cfgJSON),
					Status:     StatusActive,
					LastRunAt:  &past,
				},
				{
					ID:         "rec-b",
					UserID:     "user-2",
					Name:       "agent-b",
					ConfigJSON: string(cfgJSON),
					Status:     StatusActive,
					LastRunAt:  nil, // never run
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(HaveLen(2))
			names := []string{
				pub.calls[0].data.(AgentChatEvent).AgentName,
				pub.calls[1].data.(AgentChatEvent).AgentName,
			}
			Expect(names).To(ConsistOf("agent-a", "agent-b"))
		})

		It("uses default interval when PeriodicRuns is empty", func() {
			// Default is 10m; set last run to 11 minutes ago => should be due
			past := time.Now().Add(-11 * time.Minute)
			cfg := AgentConfig{
				StandaloneJob: true,
				PeriodicRuns:  "", // empty => 10m default
			}
			cfgJSON, _ := json.Marshal(cfg)

			mStore.configs = []AgentConfigRecord{
				{
					ID:        "rec-8",
					UserID:    "user-1",
					Name:      "default-interval-agent",
					ConfigJSON: string(cfgJSON),
					Status:    StatusActive,
					LastRunAt: &past,
				},
			}

			sched.runDueAgents()

			Expect(pub.calls).To(HaveLen(1))
		})
	})
})
