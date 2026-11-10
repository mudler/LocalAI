package studio

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
)

const typeLoopsOff = "loops_off"

// loopRules maps the errors of the loop host; the rest go to chatRules.
var loopRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "loop not found", loops.ErrNotFound),
	fixed(http.StatusConflict, typeConflict, "this chat has as many loops as it may; stop one first", loops.ErrTooManyInChat),
	fixed(http.StatusConflict, typeConflict, "you have as many loops as you may; stop one first", loops.ErrTooManyForOwner),
	sentinelText(http.StatusBadRequest, typeInvalid, loops.ErrInvalid),
	verbatim(http.StatusBadRequest, typeInvalid, "", studiosvc.ErrLoopCommand, studiosvc.ErrLoopChat, studiosvc.ErrLoopState),
	fixed(http.StatusNotFound, typeLoopsOff, "loops are turned off on this server", studiosvc.ErrLoopsOff),
	fixed(http.StatusServiceUnavailable, typeServerError, "the server is shutting down", studiosvc.ErrLoopsClosed),
}

func failLoop(c echo.Context, err error) error { return mapErr(c, err, loopRules, failFor) }

func (h *Handlers) loopHost(c echo.Context) (*studiosvc.Loops, bool) {
	return feature(c, h.svc.Loops(), func(c echo.Context) error { return failLoop(c, studiosvc.ErrLoopsOff) })
}

// loopJSON is a loop as the API gives it.
func loopJSON(l loops.Loop) schema.ChatsLoop {
	out := schema.ChatsLoop{ID: l.ID, Prompt: l.Prompt, IntervalSeconds: int64(l.Interval.Seconds()), State: string(l.State),
		Reason: l.Reason, Runs: l.Runs, Failures: l.Failures, Next: utcPtr(l.Next), Running: l.Running, Wait: l.Wait,
		Created: l.Created.UTC(), LastRun: utcPtr(l.LastRun)}
	if l.Interval > 0 {
		out.Interval = loops.ShortDuration(l.Interval)
	}
	if l.Watch != nil {
		out.Kind = "wait"
		out.Watch = waitJSON(l)
	}
	return out
}

// waitJSON is the state of a wait as the API gives it.
func waitJSON(l loops.Loop) *schema.ChatsWait {
	w := l.Watch
	out := &schema.ChatsWait{Reason: w.Note, Mode: w.Mode, Condition: waitCondition(w), Checks: w.Checks,
		LastCheck: utcPtr(w.LastCheck), Outcome: w.Outcome, LastError: redactURLs(w.LastErr),
		Deadline: w.Deadline.UTC(), Errors: w.Errors, MaxErrors: w.MaxErrors}
	switch {
	case w.Script != "":
		// The command is not shown: this list reaches people who may not
		// read it. It stays in the store of its owner.
		out.SourceKind, out.Source = "script", "a script"
	case w.Agent:
		// A wait on a background sub-agent: Job holds its id, not a job's.
		out.SourceKind, out.Source = "agent", w.Job
	case w.Job != "":
		// The id and the label the model gave; the command is never here.
		out.SourceKind, out.Source = "job", w.Job
		if w.JobLabel != "" {
			out.Source = fmt.Sprintf("%s %q", w.Job, w.JobLabel)
		}
	case w.Tool != "":
		out.SourceKind, out.Source = "tool", w.Tool
	default:
		out.SourceKind, out.Source = "url", urlLabel(w.URL)
	}
	switch {
	case l.State == loops.StatePending:
		out.Phase = "pending"
	case l.State == loops.StatePaused:
		out.Phase = "paused"
	case l.State == loops.StateEnded:
		out.Phase = "ended"
		out.End = w.End
		if out.End == "" {
			out.End = loops.EndStopped
		}
	case w.Wake != "":
		// The wait is settled and says how; the model has still to be asked.
		out.Phase, out.End = "waking", w.End
	default:
		out.Phase = "waiting"
		out.NextCheck = utcPtr(l.Next)
	}
	return out
}

// waitCondition says what a wait waits for, in words. The pattern and the
// value are cut so a long one does not fill the line.
func waitCondition(w *loops.Watch) string {
	short := func(s string) string {
		if r := []rune(s); len(r) > 80 {
			return string(r[:79]) + "…"
		}
		return s
	}
	if w.Agent {
		if w.Mode == loops.UntilStall {
			return fmt.Sprintf("the sub-agent makes no progress for %s", loops.ShortDuration(w.Idle))
		}
		return "the sub-agent finishes"
	}
	switch w.Mode {
	case loops.UntilExitZero:
		return "the script exits with status 0"
	case loops.UntilExit:
		return "the job exits"
	case loops.UntilStall:
		return fmt.Sprintf("the job writes no output for %s", loops.ShortDuration(w.Idle))
	case loops.UntilMatch:
		return fmt.Sprintf("the output matches %q", short(w.Pattern))
	case loops.UntilJSONEquals:
		return fmt.Sprintf("the field %s equals %q", short(w.Field), short(w.Equals))
	}
	return "the output changes"
}

// urlLabel is the host and path of a URL: no scheme, user, query or fragment,
// which can carry a secret.
func urlLabel(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "a URL"
	}
	return u.Hostname() + strings.TrimRight(u.EscapedPath(), "/")
}

var urlInText = regexp.MustCompile(`https?://[^\s"'<>)]+`)

// redactURLs cuts every URL in a message to host and path, and the message to
// a short line.
func redactURLs(s string) string {
	s = urlInText.ReplaceAllStringFunc(s, urlLabel)
	if r := []rune(s); len(r) > 160 {
		s = string(r[:159]) + "…"
	}
	return s
}

func loopsJSON(list []loops.Loop) []schema.ChatsLoop {
	out := make([]schema.ChatsLoop, 0, len(list))
	for _, l := range list {
		out = append(out, loopJSON(l))
	}
	return out
}

// ListLoops lists the loops of a chat.
// @Summary List the loops of a chat.
// @Description A loop sends a prompt to its chat again and again, as a new message, at an interval or, when it has none, whenever the model asks for the next run. Ended loops stay listed with the reason until you remove them.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsLoopList "The loops of the chat, oldest first"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or loops are off"
// @Router /api/chats/{id}/loops [get]
func (h *Handlers) ListLoops(c echo.Context) error {
	host, ok := h.loopHost(c)
	if !ok {
		return nil
	}
	list, err := host.List(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failLoop(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsLoopList{Loops: loopsJSON(list), Jobs: h.jobsOf(c, userID(c), c.Param("id"))})
}

// StartLoop starts a loop in a chat.
// @Summary Start a loop in a chat.
// @Description With an interval (30s, 5m, 2h; at least chats_loop_min_interval, at most 24h) the prompt is sent every interval. Without one the loop is self-paced: after each run the model calls schedule_next to say when to run again, or loop_done to stop, and a run that does neither ends the loop. The prompt is plain text or a skill (/skill-name and its words). The first loop of a chat is pending until you confirm it with PUT .../loops/{loopId} {"state":"active"}; later ones run at once. A run starts only when the chat is idle.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsLoopRequest true "the interval and the prompt"
// @Success 201 {object} schema.ChatsLoop "The new loop"
// @Failure 400 {object} schema.ErrorResponse "The interval or the prompt is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or loops are off"
// @Failure 409 {object} schema.ErrorResponse "The chat or you have as many loops as allowed"
// @Router /api/chats/{id}/loops [post]
func (h *Handlers) StartLoop(c echo.Context) error {
	host, ok := h.loopHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsLoopRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	l, err := host.Start(c.Request().Context(), userID(c), c.Param("id"), studiosvc.LoopInput{Interval: req.Interval, Prompt: req.Prompt})
	if err != nil {
		return failLoop(c, err)
	}
	return c.JSON(http.StatusCreated, loopJSON(l))
}

// SetLoopState pauses a loop or makes it active.
// @Summary Pause, resume or confirm a loop.
// @Description state paused stops the runs until you make it active again; a run that goes on finishes. state active resumes a paused loop (it runs at once) or confirms a pending one. An ended loop cannot go on.
// @Tags chats
// @Param id path string true "conversation id"
// @Param loopId path string true "loop id"
// @Param request body schema.ChatsLoopStateRequest true "the new state"
// @Success 200 {object} schema.ChatsLoop "The loop"
// @Failure 400 {object} schema.ErrorResponse "The state is not paused or active, or the loop has ended"
// @Failure 404 {object} schema.ErrorResponse "No such conversation or loop"
// @Router /api/chats/{id}/loops/{loopId} [put]
func (h *Handlers) SetLoopState(c echo.Context) error {
	host, ok := h.loopHost(c)
	if !ok {
		return nil
	}
	var req schema.ChatsLoopStateRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, msgNotJSON)
	}
	l, err := host.SetState(c.Request().Context(), userID(c), c.Param("id"), c.Param("loopId"), loops.State(req.State))
	if err != nil {
		return failLoop(c, err)
	}
	return c.JSON(http.StatusOK, loopJSON(l))
}

// StopLoop stops one loop of a chat for good.
// @Summary Stop a loop.
// @Description The loop is removed and runs no more. A run that goes on is not stopped: stop the chat's turn for that.
// @Tags chats
// @Param id path string true "conversation id"
// @Param loopId path string true "loop id"
// @Success 200 {object} map[string]bool "stopped"
// @Failure 404 {object} schema.ErrorResponse "No such conversation or loop"
// @Router /api/chats/{id}/loops/{loopId} [delete]
func (h *Handlers) StopLoop(c echo.Context) error {
	host, ok := h.loopHost(c)
	if !ok {
		return nil
	}
	if err := host.Remove(c.Request().Context(), userID(c), c.Param("id"), c.Param("loopId")); err != nil {
		return failLoop(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"stopped": true})
}

// StopLoops stops every loop of a chat.
// @Summary Stop every loop of a chat.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} map[string]int "the number of loops stopped"
// @Failure 404 {object} schema.ErrorResponse "No such conversation"
// @Router /api/chats/{id}/loops [delete]
func (h *Handlers) StopLoops(c echo.Context) error {
	host, ok := h.loopHost(c)
	if !ok {
		return nil
	}
	n, err := host.RemoveAll(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failLoop(c, err)
	}
	return c.JSON(http.StatusOK, map[string]int{"stopped": n})
}

// jobJSON is a job as the API gives it: no command, no output, no node.
func jobJSON(j studiosvc.JobView) schema.ChatsJob {
	out := schema.ChatsJob{ID: j.ID, Label: j.Label, Chat: j.Chat, Provider: j.Provider, State: string(j.State), End: string(j.End),
		Exit: j.Exit, Started: j.Started.UTC(), Ended: utcPtr(j.Ended), LastOutput: utcPtr(j.LastOutput), Bytes: j.Total}
	if out.Provider == "" {
		out.Provider = studiosvc.BuiltinProvider
	}
	return out
}

// jobsOf lists the jobs of a chat for the list and the chat detail. A failure
// to read them is an empty list: the jobs are a view, not the chat.
func (h *Handlers) jobsOf(c echo.Context, user, chat string) []schema.ChatsJob {
	out := []schema.ChatsJob{}
	list, err := h.svc.BgJobs(c.Request().Context(), user, chat)
	if err != nil {
		return out
	}
	for _, j := range list {
		job := jobJSON(j)
		job.Agent = j.Chat != chat
		out = append(out, job)
	}
	return out
}

// StopJob stops a background job of a chat.
// @Summary Stop a background job.
// @Description Stops a job that the model started with the job tool (a build, a server): the process gets term, and kill after 10 seconds. The job ends with the reason "stopped". Only the owner of the chat may stop it. The job may belong to a sub-agent of the chat.
// @Tags chats
// @Param id path string true "conversation id"
// @Param job path string true "job id"
// @Success 200 {object} schema.ChatsJob "The job after the signal"
// @Failure 404 {object} schema.ErrorResponse "No such conversation or job"
// @Failure 409 {object} schema.ErrorResponse "The job has ended already"
// @Router /api/chats/{id}/jobs/{job}/kill [post]
func (h *Handlers) StopJob(c echo.Context) error {
	v, err := h.svc.StopJob(c.Request().Context(), userID(c), c.Param("id"), c.Param("job"))
	if err != nil {
		return mapErr(c, err, jobRules, failFor)
	}
	job := jobJSON(v)
	job.Agent = v.Chat != c.Param("id")
	return c.JSON(http.StatusOK, job)
}

var jobRules = []errRule{
	fixed(http.StatusNotFound, typeNotFound, "job not found", studiosvc.ErrBgJobNotFound),
	fixed(http.StatusConflict, typeConflict, "the job has ended already", studiosvc.ErrBgJobEnded),
}
