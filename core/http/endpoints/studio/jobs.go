package studio

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/jobs"
)

// The agent tasks and jobs API keeps the paths and the JSON of the one the old
// agent pool had, so a script that called it keeps working. A task is a prompt
// template with the model or saved agent that runs it; a job is a chat that runs
// in the background, as the task's owner, with the same loop as every chat.
// Errors use the old {"error": "..."} body.

func jobFail(c echo.Context, status int, msg string) error {
	return c.JSON(status, map[string]string{"error": msg})
}

func (h *Handlers) jobHost(c echo.Context) (*studiosvc.Jobs, bool) {
	host := h.svc.Jobs()
	if host == nil {
		_ = jobFail(c, http.StatusConflict, studiosvc.ErrJobsOff.Error())
		return nil, false
	}
	return host, true
}

// failJob maps the errors of the job host.
func failJob(c echo.Context, err error) error {
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		return jobFail(c, http.StatusNotFound, "not found")
	case errors.Is(err, agents.ErrNotFound):
		return jobFail(c, http.StatusBadRequest, "agent not found")
	case errors.Is(err, jobs.ErrInvalid):
		return jobFail(c, http.StatusBadRequest, strings.TrimPrefix(err.Error(), jobs.ErrInvalid.Error()+": "))
	case errors.Is(err, studiosvc.ErrBadJobInput):
		return jobFail(c, http.StatusBadRequest, strings.TrimPrefix(err.Error(), studiosvc.ErrBadJobInput.Error()+": "))
	case errors.Is(err, studiosvc.ErrTaskDisabled):
		return jobFail(c, http.StatusBadRequest, "the task is switched off")
	case errors.Is(err, jobs.ErrNameTaken), errors.Is(err, studiosvc.ErrTooManyTasks), errors.Is(err, studiosvc.ErrJobEnded):
		return jobFail(c, http.StatusConflict, strings.TrimPrefix(err.Error(), "studio: "))
	case errors.Is(err, studiosvc.ErrTooManyJobs):
		return jobFail(c, http.StatusTooManyRequests, strings.TrimPrefix(err.Error(), "studio: "))
	case errors.Is(err, files.ErrUnavailable):
		return jobFail(c, http.StatusConflict, err.Error())
	case errors.Is(err, studiosvc.ErrNoModel):
		return jobFail(c, http.StatusBadRequest, err.Error())
	}
	xlog.Error("studio: agent job request failed", "path", c.Request().URL.Path, "error", err)
	return jobFail(c, http.StatusInternalServerError, "the request failed")
}

func taskOut(t jobs.Task) schema.Task {
	enabled := t.Enabled
	out := schema.Task{ID: t.ID, Name: t.Name, Description: t.Description, Model: t.Model, AgentID: t.AgentID, Prompt: t.Prompt,
		CreatedAt: t.Created, UpdatedAt: t.Updated, Enabled: &enabled}
	for _, w := range t.Webhooks {
		hidden := make(map[string]string, len(w.Headers))
		for k := range w.Headers {
			hidden[k] = studiosvc.HiddenHeader
		}
		if len(hidden) == 0 {
			hidden = nil
		}
		out.Webhooks = append(out.Webhooks, schema.WebhookConfig{URL: w.URL, Method: w.Method, Headers: hidden, PayloadTemplate: w.PayloadTemplate})
	}
	return out
}

func taskIn(t schema.Task) (studiosvc.TaskInput, error) {
	if t.Cron != "" || len(t.CronParameters) > 0 || len(t.MultimediaSources) > 0 {
		return studiosvc.TaskInput{}, fmt.Errorf("%w: cron, cron_parameters and multimedia_sources are not supported: schedule a saved agent with a trigger (/api/chats/triggers)", jobs.ErrInvalid)
	}
	in := studiosvc.TaskInput{Name: t.Name, Description: t.Description, Model: t.Model, AgentID: t.AgentID, Prompt: t.Prompt, Enabled: t.Enabled}
	for _, w := range t.Webhooks {
		in.Webhooks = append(in.Webhooks, jobs.Webhook{URL: w.URL, Method: strings.ToUpper(w.Method), Headers: w.Headers, PayloadTemplate: w.PayloadTemplate})
	}
	return in, nil
}

func jobOut(i studiosvc.JobInfo) schema.Job {
	out := schema.Job{ID: i.ID, TaskID: i.TaskID, Status: schema.JobStatus(i.Status), Parameters: i.Parameters, Result: i.Result, Error: i.Error,
		CreatedAt: i.Created.UTC(), TriggeredBy: i.TriggeredBy, WebhookSent: i.WebhookSent, WebhookError: i.WebhookError, Attachments: i.Attachments}
	if !i.StartedAt.IsZero() {
		t := i.StartedAt.UTC()
		out.StartedAt = &t
	}
	if !i.CompletedAt.IsZero() {
		t := i.CompletedAt.UTC()
		out.CompletedAt = &t
	}
	if !i.WebhookSentAt.IsZero() {
		t := i.WebhookSentAt.UTC()
		out.WebhookSentAt = &t
	}
	if out.Parameters == nil {
		out.Parameters = map[string]string{}
	}
	return out
}

// refuseTaskModel answers the request and returns true when the model does not
// exist or the user may not use it.
func (h *Handlers) refuseTaskModel(c echo.Context, model string) bool {
	if h.models == nil || model == "" {
		return false
	}
	switch err := h.models.Check(c.Request().Context(), auth.GetUser(c), model); {
	case errors.Is(err, ErrUnknownModel):
		_ = jobFail(c, http.StatusBadRequest, "unknown model")
	case errors.Is(err, ErrModelNotAllowed):
		_ = jobFail(c, http.StatusForbidden, "you may not use this model")
	case err != nil:
		_ = failJob(c, err)
	default:
		return false
	}
	return true
}

// CreateTask creates an agent task.
// @Summary Create an agent task.
// @Description A task is a prompt template (Go template syntax, each parameter written as .name between double curly braces) run by a model, or by a saved agent (agent_id), as a background chat. webhooks are called when a job of the task ends; header values are never returned. cron, cron_parameters and multimedia_sources are refused: schedule a saved agent with a trigger. Needs the mcp_jobs feature.
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param request body schema.Task true "Task definition"
// @Success 201 {object} map[string]string "id"
// @Failure 400 {object} map[string]string "error"
// @Router /api/agent/tasks [post]
func (h *Handlers) CreateTask(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	var task schema.Task
	if err := c.Bind(&task); err != nil {
		return jobFail(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	in, err := taskIn(task)
	if err != nil {
		return failJob(c, err)
	}
	if h.refuseTaskModel(c, in.Model) {
		return nil
	}
	t, err := host.CreateTask(c.Request().Context(), userID(c), in)
	if err != nil {
		return failJob(c, err)
	}
	return c.JSON(http.StatusCreated, map[string]string{"id": t.ID})
}

// UpdateTask replaces an agent task.
// @Summary Update an agent task.
// @Description Replaces every field. A webhook header sent back as "[hidden]" keeps its stored value.
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param id path string true "Task ID"
// @Param request body schema.Task true "Updated task definition"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "error"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{id} [put]
func (h *Handlers) UpdateTask(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	var task schema.Task
	if err := c.Bind(&task); err != nil {
		return jobFail(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	in, err := taskIn(task)
	if err != nil {
		return failJob(c, err)
	}
	if h.refuseTaskModel(c, in.Model) {
		return nil
	}
	if _, err := host.UpdateTask(c.Request().Context(), userID(c), c.Param("id"), in); err != nil {
		return failJob(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Task updated"})
}

// DeleteTask deletes an agent task.
// @Summary Delete an agent task.
// @Description The jobs of the task stay until their retention passes.
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Task ID"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{id} [delete]
func (h *Handlers) DeleteTask(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	if err := host.DeleteTask(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failJob(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Task deleted"})
}

// wantsAllUsers is true when an administrator asks for every user's items.
func wantsAllUsers(c echo.Context) bool {
	u := auth.GetUser(c)
	return c.QueryParam("all_users") == "true" && (u == nil || u.Role == auth.RoleAdmin)
}

// ListTasks lists agent tasks.
// @Summary List agent tasks.
// @Description With all_users=true an administrator also gets the tasks of the other users, read-only, in user_groups keyed by owner.
// @Tags agent-jobs
// @Produce json
// @Param all_users query string false "Set to 'true' for the administrator's cross-user listing"
// @Success 200 {object} []schema.Task "tasks"
// @Router /api/agent/tasks [get]
func (h *Handlers) ListTasks(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	me := userID(c)
	mine, err := host.ListTasks(ctx, me)
	if err != nil {
		return failJob(c, err)
	}
	tasks := make([]schema.Task, 0, len(mine))
	for _, t := range mine {
		tasks = append(tasks, taskOut(t))
	}
	if !wantsAllUsers(c) {
		return c.JSON(http.StatusOK, tasks)
	}
	all, err := host.AdminTasks(ctx)
	if err != nil {
		return failJob(c, err)
	}
	groups := map[string]any{}
	byOwner := map[string][]schema.Task{}
	for _, t := range all {
		if t.Owner != h.svc.Principal(me) {
			byOwner[t.Owner] = append(byOwner[t.Owner], taskOut(t))
		}
	}
	for owner, list := range byOwner {
		groups[owner] = map[string]any{"tasks": list}
	}
	if len(groups) == 0 {
		return c.JSON(http.StatusOK, tasks)
	}
	return c.JSON(http.StatusOK, map[string]any{"tasks": tasks, "user_groups": groups})
}

// GetTask returns one agent task.
// @Summary Get an agent task.
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Task ID"
// @Success 200 {object} schema.Task "task"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{id} [get]
func (h *Handlers) GetTask(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	t, err := host.GetTask(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failJob(c, err)
	}
	return c.JSON(http.StatusOK, taskOut(t))
}

func attachments(req schema.JobExecutionRequest) []studiosvc.Attachment {
	var out []studiosvc.Attachment
	for kind, list := range map[string][]string{"image": req.Images, "video": req.Videos, "audio": req.Audios, "file": req.Files} {
		for _, d := range list {
			out = append(out, studiosvc.Attachment{Kind: kind, Data: d})
		}
	}
	return out
}

func jobResponse(c echo.Context, i studiosvc.JobInfo) error {
	return c.JSON(http.StatusCreated, schema.JobExecutionResponse{
		JobID: i.ID, Status: "pending", URL: c.Scheme() + "://" + c.Request().Host + "/api/agent/jobs/" + i.ID,
	})
}

// ExecuteJob starts a job for a task.
// @Summary Execute an agent job.
// @Description Starts a job for the task as a background chat of the caller and answers at once; poll the job for its status and result. parameters fill the prompt template. images, videos, audios and files are base64 data URIs (an address is not fetched).
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param request body schema.JobExecutionRequest true "Job execution request"
// @Success 201 {object} schema.JobExecutionResponse "job"
// @Failure 400 {object} map[string]string "error"
// @Router /api/agent/jobs/execute [post]
func (h *Handlers) ExecuteJob(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	var req schema.JobExecutionRequest
	if err := c.Bind(&req); err != nil {
		return jobFail(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	info, err := host.Execute(c.Request().Context(), userID(c), req.TaskID,
		studiosvc.RunRequest{Params: req.Parameters, Attachments: attachments(req), TriggeredBy: "api"})
	if err != nil {
		return failJob(c, err)
	}
	return jobResponse(c, info)
}

// ExecuteTaskByName starts a job for the task with this name.
// @Summary Execute an agent task by name.
// @Description The body is an object of parameters for the prompt template.
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param name path string true "Task name"
// @Param parameters body object false "Optional template parameters"
// @Success 201 {object} schema.JobExecutionResponse "job"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{name}/execute [post]
func (h *Handlers) ExecuteTaskByName(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	params := map[string]string{}
	if c.Request().ContentLength != 0 {
		body := map[string]any{}
		if err := c.Bind(&body); err != nil {
			return jobFail(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		for k, v := range body {
			if s, isStr := v.(string); isStr {
				params[k] = s
			} else {
				params[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	task, err := host.FindTask(ctx, userID(c), c.Param("name"))
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return jobFail(c, http.StatusNotFound, "Task not found: "+c.Param("name"))
		}
		return failJob(c, err)
	}
	info, err := host.Execute(ctx, userID(c), task.ID, studiosvc.RunRequest{Params: params, TriggeredBy: "api"})
	if err != nil {
		return failJob(c, err)
	}
	return jobResponse(c, info)
}

// GetJob returns one job.
// @Summary Get an agent job.
// @Description status is pending, running, completed, failed or cancelled; result is the last answer of the chat; traces are the reasoning and tool results. An administrator can read any user's job.
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} schema.Job "job"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/jobs/{id} [get]
func (h *Handlers) GetJob(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	info, err := host.GetJob(ctx, userID(c), c.Param("id"))
	if errors.Is(err, jobs.ErrNotFound) && wantsAdminRead(c) {
		info, err = host.AdminJob(ctx, c.Param("id"))
	}
	if err != nil {
		return failJob(c, err)
	}
	out := jobOut(info)
	traces, err := host.Traces(ctx, info.Owner, info.ID)
	if err != nil {
		return failJob(c, err)
	}
	for _, t := range traces {
		out.Traces = append(out.Traces, schema.JobTrace{Type: t.Type, Content: t.Content, Timestamp: t.At})
	}
	return c.JSON(http.StatusOK, out)
}

// wantsAdminRead is true for an administrator, who may read any user's job.
func wantsAdminRead(c echo.Context) bool {
	u := auth.GetUser(c)
	return u != nil && u.Role == auth.RoleAdmin
}

// ListJobs lists agent jobs.
// @Summary List agent jobs.
// @Description Newest first. With all_users=true an administrator also gets the jobs of the other users, read-only, in user_groups keyed by owner.
// @Tags agent-jobs
// @Produce json
// @Param task_id query string false "Filter by task ID"
// @Param status query string false "Filter by status (pending, running, completed, failed, cancelled)"
// @Param limit query integer false "Max number of jobs to return"
// @Param all_users query string false "Set to 'true' for the administrator's cross-user listing"
// @Success 200 {object} []schema.Job "jobs"
// @Router /api/agent/jobs [get]
func (h *Handlers) ListJobs(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	ctx := c.Request().Context()
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	q := studiosvc.JobQuery{TaskID: c.QueryParam("task_id"), Status: studiosvc.JobStatus(c.QueryParam("status")), Limit: limit}
	me := userID(c)
	mine, err := host.ListJobs(ctx, me, q)
	if err != nil {
		return failJob(c, err)
	}
	list := make([]schema.Job, 0, len(mine))
	for _, j := range mine {
		list = append(list, jobOut(j))
	}
	if !wantsAllUsers(c) {
		return c.JSON(http.StatusOK, list)
	}
	all, err := host.AdminJobs(ctx, q)
	if err != nil {
		return failJob(c, err)
	}
	byOwner := map[string][]schema.Job{}
	for _, j := range all {
		if j.Owner != h.svc.Principal(me) {
			byOwner[j.Owner] = append(byOwner[j.Owner], jobOut(j))
		}
	}
	if len(byOwner) == 0 {
		return c.JSON(http.StatusOK, list)
	}
	groups := map[string]any{}
	for owner, l := range byOwner {
		groups[owner] = map[string]any{"jobs": l}
	}
	return c.JSON(http.StatusOK, map[string]any{"jobs": list, "user_groups": groups})
}

// CancelJob stops a running job.
// @Summary Cancel an agent job.
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "error"
// @Failure 409 {object} map[string]string "error"
// @Router /api/agent/jobs/{id}/cancel [post]
func (h *Handlers) CancelJob(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	if err := host.CancelJob(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failJob(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Job cancelled"})
}

// RerunJob starts a new job with the task and parameters of an earlier one.
// @Summary Run an agent job again.
// @Description Starts a new job with the same task and parameters. Attachments are not kept, so they are not sent again.
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 201 {object} schema.JobExecutionResponse "job"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/jobs/{id}/rerun [post]
func (h *Handlers) RerunJob(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	info, err := host.Rerun(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failJob(c, err)
	}
	return jobResponse(c, info)
}

// DeleteJob deletes a job and its chat.
// @Summary Delete an agent job.
// @Description A running job is stopped first.
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/jobs/{id} [delete]
func (h *Handlers) DeleteJob(c echo.Context) error {
	host, ok := h.jobHost(c)
	if !ok {
		return nil
	}
	if err := host.DeleteJob(c.Request().Context(), userID(c), c.Param("id")); err != nil {
		return failJob(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "Job deleted"})
}
