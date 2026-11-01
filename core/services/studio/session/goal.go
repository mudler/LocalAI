package session

import "time"

// GoalState is where a chat's goal stands.
type GoalState string

// Goal states. A cleared goal is not stored: the chat has no goal.
const (
	// GoalActive: the chat works toward the goal, and a turn that ends is
	// followed by another one until the goal is done, blocked or paused.
	GoalActive GoalState = "active"
	// GoalPaused: nothing continues by itself until the person resumes.
	// Goal.PausedBy says why.
	GoalPaused GoalState = "paused"
	// GoalDone: the model said the goal is reached. Goal.Note is its summary.
	GoalDone GoalState = "done"
	// GoalBlocked: the model said it cannot go on. Goal.Note is the reason and
	// what it needs from the person.
	GoalBlocked GoalState = "blocked"
)

// Why a goal paused.
const (
	GoalPausedByUser        = "user"        // the person pressed Stop or Pause
	GoalPausedByTurnLimit   = "turn_limit"  // the automatic turns reached chats_goal_max_turns
	GoalPausedByStepLimit   = "step_limit"  // the model steps reached chats_goal_max_steps
	GoalPausedByError       = "error"       // a turn failed
	GoalPausedByInterrupted = "interrupted" // the server stopped during a turn
)

// MaxGoalLen is the longest goal text, in characters.
const MaxGoalLen = 2000

// Goal is the persistent objective of one chat. It lives with the chat record
// so that every frontend sees it. The store's UpdateGoal changes it;
// UpdateChat leaves it alone.
type Goal struct {
	Text  string    `json:"text"`
	State GoalState `json:"state"`
	// PausedBy is one of the GoalPausedBy* reasons while State is paused.
	PausedBy string `json:"paused_by,omitempty"`
	// Note is the model's one-line summary of a done goal, or the reason of
	// a blocked one.
	Note string `json:"note,omitempty"`
	// Turns counts the turns the server started by itself for the goal since
	// it was set or last resumed. Steps counts the model requests of every
	// turn that ran while the goal was active, in the same span.
	Turns int `json:"turns"`
	Steps int `json:"steps"`
	// Set is when the goal was set; Updated when it last changed.
	Set     time.Time `json:"set"`
	Updated time.Time `json:"updated"`
}

// Clone returns a copy of g that shares nothing with it.
func (g *Goal) Clone() *Goal {
	if g == nil {
		return nil
	}
	cp := *g
	return &cp
}

// The kinds of a goal line of the transcript (Entry.Note of a RoleGoal entry).
const (
	GoalLineSet      = "set"      // Text: the goal
	GoalLineReplaced = "replaced" // Text: the new goal; it replaced another one
	GoalLineContinue = "continue" // Text: what the model got; Rev: the turn of the goal
	GoalLineDone     = "done"     // Text: the model's summary
	GoalLineBlocked  = "blocked"  // Text: the model's reason
	GoalLinePaused   = "paused"   // Text: one of the GoalPausedBy* reasons
	GoalLineResumed  = "resumed"  // Text: the goal
	GoalLineCleared  = "cleared"  // Text: the goal that was removed
)
