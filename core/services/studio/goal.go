package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// A goal is a persistent objective of a chat. While it is active, every turn's
// prompt carries it, the model gets goal_done and goal_blocked, and when a turn
// ends by itself the frontend that ran it starts the next one, until the model
// says the goal is done or blocked, the person stops it, or a limit is reached.
//
// Distributed mode: the goal is stored with the chat record (the shared store),
// so every frontend reads the same one, and the goal event rides the chat's
// bus. Nothing is kept in memory. The next turn is started through the same
// turn claim as any other turn, so one turn runs per chat whoever tries, and a
// frontend that dies mid-turn leaves the goal paused (the reaper, or the JSON
// store at load) instead of continued twice.

// Defaults and bounds of chats_goal_max_turns and chats_goal_max_steps.
const (
	DefaultGoalMaxTurns = 20
	DefaultGoalMaxSteps = 400
)

// Goal errors.
var (
	// ErrGoalNotAllowed: a sub-agent run, or a chat that a trigger, a
	// connector, a job or a page started, has no goal.
	ErrGoalNotAllowed = errors.New("only a chat that you started can have a goal")
	// ErrNoGoal: the chat has no goal to resume.
	ErrNoGoal = errors.New("this chat has no goal")
	// ErrGoalText: the goal text is empty or too long.
	ErrGoalText = fmt.Errorf("the goal must be 1 to %d characters", session.MaxGoalLen)
	// ErrGoalReached: a done goal does not resume; set a new one.
	ErrGoalReached = errors.New("the goal is reached; set a new one")
)

// errGoalNoChange aborts an UpdateGoal that has nothing to write.
var errGoalNoChange = errors.New("studio: goal unchanged")

const (
	goalDoneName    = "goal_done"
	goalBlockedName = "goal_blocked"
	maxGoalNote     = 500
)

// The messages the server sends by itself for a goal.
func goalKickoff(text string) string {
	return fmt.Sprintf("Work toward this goal now: %q.", text)
}

func goalContinue(text string) string { return fmt.Sprintf("Continue toward the goal: %q.", text) }

func goalResume(text string) string { return fmt.Sprintf("Resume working toward the goal: %q.", text) }

// goalBlock is the block of the prompt of every turn while the goal is active.
func goalBlock(g *session.Goal) string {
	if g == nil || g.State != session.GoalActive {
		return ""
	}
	return "Active goal: " + g.Text + "\n" +
		"Work toward it. The server starts your next turn by itself until the goal is done, so do one useful step per turn and do not ask the person to say \"continue\". " +
		"When it is fully done, and you checked that it is, call the " + goalDoneName + " tool with a one-line summary. " +
		"If you are blocked, call " + goalBlockedName + " with the reason and what you need from the person. " +
		"A message from the person may change the direction: follow it."
}

// goalAllowed says whether a chat may have a goal: a conversation a person
// started, never a sub-agent run or a run that started by itself.
func goalAllowed(rec session.ChatRecord) error {
	if rec.Kind == session.KindSubAgent || rec.Origin != "" {
		return ErrGoalNotAllowed
	}
	return nil
}

// goalActive says whether the chat works toward a goal now.
func goalActive(rec session.ChatRecord) bool {
	return rec.Goal != nil && rec.Goal.State == session.GoalActive && goalAllowed(rec) == nil
}

// GoalLimits returns chats_goal_max_turns and chats_goal_max_steps as they
// apply now.
func (s *Service) GoalLimits() (turns, steps int) {
	turns, steps = DefaultGoalMaxTurns, DefaultGoalMaxSteps
	if s.d.GoalLimits != nil {
		t, st := s.d.GoalLimits()
		if t > 0 {
			turns = t
		}
		if st > 0 {
			steps = st
		}
	}
	return turns, steps
}

// Goal returns the goal of a chat, or nil.
func (s *Service) Goal(ctx context.Context, user, conv string) (*session.Goal, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return nil, err
	}
	return rec.Goal, nil
}

// cleanGoal checks and trims the text of a goal.
func cleanGoal(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > session.MaxGoalLen || !utf8.ValidString(text) {
		return "", ErrGoalText
	}
	return text, nil
}

// SetGoal sets the goal of a chat, replacing the one it had, and returns the
// replaced goal (nil when there was none) and the new one. With no turn running
// a turn starts on it at once; with one running, the goal applies when it ends.
func (s *Service) SetGoal(ctx context.Context, user, conv, text string) (prev, cur *session.Goal, err error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return nil, nil, err
	}
	if err := goalAllowed(rec); err != nil {
		return nil, nil, err
	}
	if text, err = cleanGoal(text); err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	err = s.d.Store.UpdateGoal(ctx, p, conv, func(old *session.Goal) (*session.Goal, error) {
		prev = old
		cur = &session.Goal{Text: text, State: session.GoalActive, Set: now, Updated: now}
		return cur, nil
	})
	if err != nil {
		return nil, nil, err
	}
	kind := session.GoalLineSet
	if prev != nil && (prev.State == session.GoalActive || prev.State == session.GoalPaused || prev.State == session.GoalBlocked) {
		kind = session.GoalLineReplaced
	}
	s.goalLine(ctx, p, conv, kind, text)
	s.publishGoal(p, conv, cur)
	s.kickGoal(ctx, p, conv, goalKickoff)
	return prev, cur.Clone(), nil
}

// ClearGoal removes the goal of a chat and returns it (nil when there was
// none). A running turn goes on, and nothing follows it.
func (s *Service) ClearGoal(ctx context.Context, user, conv string) (*session.Goal, error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return nil, err
	}
	var prev *session.Goal
	err := s.d.Store.UpdateGoal(ctx, p, conv, func(old *session.Goal) (*session.Goal, error) {
		if old == nil {
			return nil, errGoalNoChange
		}
		prev = old
		return nil, nil
	})
	if errors.Is(err, errGoalNoChange) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.goalLine(ctx, p, conv, session.GoalLineCleared, prev.Text)
	s.publishGoal(p, conv, nil)
	return prev, nil
}

// ResumeGoal makes a paused or blocked goal active again with fresh turn and
// step budgets, and starts a turn on it when none runs. An active goal with no
// turn running (its frontend stopped between two turns) starts one as well.
func (s *Service) ResumeGoal(ctx context.Context, user, conv string) (*session.Goal, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return nil, err
	}
	if err := goalAllowed(rec); err != nil {
		return nil, err
	}
	var cur *session.Goal
	resumed := false
	err = s.d.Store.UpdateGoal(ctx, p, conv, func(g *session.Goal) (*session.Goal, error) {
		switch {
		case g == nil:
			return nil, ErrNoGoal
		case g.State == session.GoalDone:
			return nil, ErrGoalReached
		case g.State == session.GoalActive:
			cur = g
			return g, errGoalNoChange
		}
		g.State, g.PausedBy, g.Note, g.Turns, g.Steps = session.GoalActive, "", "", 0, 0
		g.Updated = time.Now().UTC()
		cur, resumed = g, true
		return g, nil
	})
	if err != nil && !errors.Is(err, errGoalNoChange) {
		return nil, err
	}
	if resumed {
		s.goalLine(ctx, p, conv, session.GoalLineResumed, cur.Text)
		s.publishGoal(p, conv, cur)
	}
	s.kickGoal(ctx, p, conv, goalResume)
	if g, gerr := s.Goal(ctx, user, conv); gerr == nil && g != nil {
		cur = g
	}
	return cur, nil
}

// kickGoal starts a turn on the goal when no turn runs. A turn that runs (here
// or on another frontend) goes on with the goal when it ends.
func (s *Service) kickGoal(ctx context.Context, p, conv string, prompt func(string) string) {
	unlock := s.lock(key(p, conv))
	defer unlock()
	ctx = context.WithoutCancel(ctx)
	started, err := s.continueGoal(ctx, p, conv, prompt)
	if err != nil {
		s.log.Warn("studio: could not start a turn on the goal", "chat", conv, "error", err)
		return
	}
	if started {
		s.limitWorkerRun(ctx, p, conv)
	}
}

// continueGoal starts the next turn of an active goal, when no turn runs and
// the goal has turns and steps left; a goal at a limit is paused instead. The
// turn claim decides: whoever holds the chat runs the next turn, so two
// frontends, or a frontend and a person's message, never start two. The
// caller holds the lock of the chat.
func (s *Service) continueGoal(ctx context.Context, p, conv string, prompt func(string) string) (bool, error) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return false, err
	}
	if !goalActive(rec) {
		return false, nil
	}
	last := store.TurnDone
	if info, terr := s.d.Store.Turn(ctx, p, conv); terr == nil && info.Last != "" {
		last = info.Last
	}
	runID := newID()
	if err := s.d.Store.ClaimTurn(ctx, p, conv, runID); err != nil {
		if errors.Is(err, store.ErrTurnRunning) {
			return false, nil // that turn goes on with the goal when it ends
		}
		return false, err
	}
	maxTurns, maxSteps := s.GoalLimits()
	var (
		cur      *session.Goal
		pausedBy string
	)
	err = s.d.Store.UpdateGoal(ctx, p, conv, func(g *session.Goal) (*session.Goal, error) {
		if g == nil || g.State != session.GoalActive {
			return g, errGoalNoChange
		}
		switch {
		case g.Turns >= maxTurns:
			pausedBy = session.GoalPausedByTurnLimit
		case g.Steps >= maxSteps:
			pausedBy = session.GoalPausedByStepLimit
		}
		if pausedBy != "" {
			g.State, g.PausedBy = session.GoalPaused, pausedBy
		} else {
			g.Turns++
		}
		g.Updated = time.Now().UTC()
		cur = g
		return g, nil
	})
	if err != nil || pausedBy != "" {
		if rerr := s.d.Store.ReleaseTurn(ctx, p, conv, runID, last); rerr != nil {
			s.log.Warn("studio: could not release the claim of a goal turn", "chat", conv, "error", rerr)
		}
		if pausedBy != "" {
			s.goalLine(ctx, p, conv, session.GoalLinePaused, pausedBy)
			s.publishGoal(p, conv, cur)
		}
		if errors.Is(err, errGoalNoChange) {
			return false, nil
		}
		return false, err
	}
	s.publishGoal(p, conv, cur)
	msg := session.Message{Text: prompt(cur.Text), Goal: true, GoalTurn: cur.Turns}
	if err := s.startTurn(ctx, p, conv, runID, msg); err != nil {
		// startTurn gave the claim back as failed.
		s.pauseGoal(ctx, p, conv, session.GoalPausedByError)
		return false, err
	}
	return true, nil
}

// pauseGoal pauses the chat's goal when it is active.
func (s *Service) pauseGoal(ctx context.Context, p, conv, by string) {
	var cur *session.Goal
	err := s.d.Store.UpdateGoal(ctx, p, conv, func(g *session.Goal) (*session.Goal, error) {
		if g == nil || g.State != session.GoalActive {
			return g, errGoalNoChange
		}
		g.State, g.PausedBy, g.Updated = session.GoalPaused, by, time.Now().UTC()
		cur = g
		return g, nil
	})
	if errors.Is(err, errGoalNoChange) || errors.Is(err, session.ErrNotFound) {
		return
	}
	if err != nil {
		s.log.Warn("studio: could not pause the goal", "chat", conv, "error", err)
		return
	}
	s.goalLine(ctx, p, conv, session.GoalLinePaused, by)
	s.publishGoal(p, conv, cur)
}

// settleGoal records a turn that ended in the chat's goal: its model steps
// count, and a turn that was stopped, failed or cut short pauses the goal. A
// turn that ended by itself leaves it active, and the caller goes on.
func (s *Service) settleGoal(ctx context.Context, p, conv string, status store.TurnStatus, steps int) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || !goalActive(rec) {
		return
	}
	by := ""
	switch status {
	case store.TurnStopped:
		by = session.GoalPausedByUser
	case store.TurnFailed:
		by = session.GoalPausedByError
	case store.TurnInterrupted:
		by = session.GoalPausedByInterrupted
	}
	var cur *session.Goal
	err = s.d.Store.UpdateGoal(ctx, p, conv, func(g *session.Goal) (*session.Goal, error) {
		if g == nil || g.State != session.GoalActive || (steps == 0 && by == "") {
			return g, errGoalNoChange
		}
		g.Steps += steps
		if by != "" {
			g.State, g.PausedBy = session.GoalPaused, by
		}
		g.Updated = time.Now().UTC()
		cur = g
		return g, nil
	})
	if errors.Is(err, errGoalNoChange) {
		return
	}
	if err != nil {
		s.log.Warn("studio: could not record a turn in the goal", "chat", conv, "error", err)
		return
	}
	if by != "" {
		s.goalLine(ctx, p, conv, session.GoalLinePaused, by)
		s.publishGoal(p, conv, cur)
	}
}

// goalLine adds a goal line to the transcript. A failure costs the line only.
func (s *Service) goalLine(ctx context.Context, p, conv, kind, text string) {
	turn := 0
	if rec, err := s.d.Store.GetChat(ctx, p, conv); err == nil {
		turn = rec.Turns
	}
	if _, err := s.d.Store.AppendEntry(ctx, conv, session.Entry{Turn: turn, Role: session.RoleGoal, Note: kind, Text: text}); err != nil {
		s.log.Warn("studio: could not add a goal line to the transcript", "chat", conv, "error", err)
	}
}

// publishGoal tells the watchers of the chat that its goal changed.
func (s *Service) publishGoal(p, conv string, g *session.Goal) {
	state := ""
	if g != nil {
		state = string(g.State)
	}
	s.bus.Publish(key(p, conv), session.Event{Kind: session.KindGoal, Text: state})
}

// endGoal records the model's goal_done or goal_blocked. It refuses when the
// goal is no longer active: the person may have cleared or paused it.
func (s *Service) endGoal(ctx context.Context, p, conv string, state session.GoalState, note string) (string, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return "", errors.New("give a one-line text")
	}
	if utf8.RuneCountInString(note) > maxGoalNote {
		note = string([]rune(note)[:maxGoalNote])
	}
	var cur *session.Goal
	err := s.d.Store.UpdateGoal(ctx, p, conv, func(g *session.Goal) (*session.Goal, error) {
		if g == nil || g.State != session.GoalActive {
			return g, errGoalNoChange
		}
		g.State, g.Note, g.PausedBy, g.Updated = state, note, "", time.Now().UTC()
		cur = g
		return g, nil
	})
	if errors.Is(err, errGoalNoChange) {
		return "There is no active goal now: the person paused, cleared or replaced it. Do not call this tool again in this turn.", nil
	}
	if err != nil {
		return "", err
	}
	kind := session.GoalLineDone
	if state == session.GoalBlocked {
		kind = session.GoalLineBlocked
	}
	s.goalLine(ctx, p, conv, kind, note)
	s.publishGoal(p, conv, cur)
	if state == session.GoalDone {
		return "The goal is marked as reached. Tell the person in a few words what you did, then stop.", nil
	}
	return "The goal is marked as blocked and nothing continues by itself. Tell the person what you need, then stop.", nil
}

const goalDoneDescription = "Say that the chat's active goal is fully reached. Call it only after you checked that the whole goal is done, not one part of it. " +
	"Give a one-line summary of what was done. No turn follows by itself after it."

const goalDoneSchema = `{"type":"object","properties":{"summary":{"type":"string","minLength":1,"maxLength":500,"description":"One line: what was done."}},"required":["summary"]}`

const goalBlockedDescription = "Say that you cannot go on with the chat's active goal without the person: give the reason and what you need from them. " +
	"Nothing continues by itself until they answer and resume the goal."

const goalBlockedSchema = `{"type":"object","properties":{"reason":{"type":"string","minLength":1,"maxLength":500,"description":"Why you cannot go on, and what you need."}},"required":["reason"]}`

// registerGoalTools adds goal_done and goal_blocked to the registry of built-in
// tools. A chat has them only while its goal is active; a sub-agent and a run
// that started by itself never do. They have no runtime setting and no switch:
// they come and go with the goal.
func (s *Service) registerGoalTools() error {
	type def struct {
		id, label, desc, schema, field string
		state                          session.GoalState
	}
	for _, d := range []def{
		{goalDoneName, "Goal reached", goalDoneDescription, goalDoneSchema, "summary", session.GoalDone},
		{goalBlockedName, "Goal blocked", goalBlockedDescription, goalBlockedSchema, "reason", session.GoalBlocked},
	} {
		err := s.builtins.Register(builtins.Tool{
			ID: d.id, Label: d.label, Description: d.desc, Unlisted: true,
			Build: func(_ context.Context, c builtins.Chat) (session.Tool, bool, string) {
				switch {
				case c.SubAgent:
					return session.Tool{}, false, "a sub-agent has no goal"
				case c.Origin != "":
					return session.Tool{}, false, "a run that started by itself has no goal"
				case !c.GoalActive:
					return session.Tool{}, false, "the chat has no active goal"
				}
				a := c.Actor
				run := func(ctx context.Context, raw json.RawMessage) (string, error) {
					var args map[string]any
					if err := json.Unmarshal(raw, &args); err != nil {
						return "", fmt.Errorf("bad arguments: %w", err)
					}
					text, _ := args[d.field].(string)
					return s.endGoal(ctx, a.Principal, a.ChatID, d.state, text)
				}
				return session.Tool{Description: d.desc, Schema: json.RawMessage(d.schema), Run: run}, true, ""
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}
