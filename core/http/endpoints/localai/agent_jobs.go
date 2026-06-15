package localai

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/agentpool"
)

// getJobService returns the job service for the current user.
// Falls back to the global service when no user is authenticated.
func getJobService(app *application.Application, c echo.Context) *agentpool.AgentJobService {
	userID := getUserID(c)
	if userID == "" {
		return app.AgentJobService()
	}
	svc := app.AgentPoolService()
	if svc == nil {
		return app.AgentJobService()
	}
	jobSvc, err := svc.JobServiceForUser(userID)
	if err != nil {
		return app.AgentJobService()
	}
	return jobSvc
}

// CreateTaskEndpoint creates a new agent task definition.
// @Summary Create a new agent task
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param request body schema.Task true "Task definition"
// @Success 201 {object} map[string]string "id"
// @Failure 400 {object} map[string]string "error"
// @Router /api/agent/tasks [post]
func CreateTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var task schema.Task
		if err := c.Bind(&task); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body: " + err.Error()})
		}

		id, err := getJobService(app, c).CreateTask(task)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusCreated, map[string]string{"id": id})
	}
}

// UpdateTaskEndpoint updates an existing agent task.
// @Summary Update an agent task
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param id path string true "Task ID"
// @Param request body schema.Task true "Updated task definition"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "error"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{id} [put]
func UpdateTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var task schema.Task
		if err := c.Bind(&task); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body: " + err.Error()})
		}

		if err := getJobService(app, c).UpdateTask(id, task); err != nil {
			if errors.Is(err, agentpool.ErrTaskNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Task updated"})
	}
}

// DeleteTaskEndpoint deletes an agent task.
// @Summary Delete an agent task
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Task ID"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{id} [delete]
func DeleteTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := getJobService(app, c).DeleteTask(id); err != nil {
			if errors.Is(err, agentpool.ErrTaskNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Task deleted"})
	}
}

// ListTasksEndpoint lists all agent tasks for the current user.
// @Summary List agent tasks
// @Tags agent-jobs
// @Produce json
// @Param all_users query string false "Set to 'true' for admin cross-user listing"
// @Success 200 {object} []schema.Task "tasks"
// @Router /api/agent/tasks [get]
func ListTasksEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		jobSvc := getJobService(app, c)
		tasks := jobSvc.ListTasks()

		// Admin cross-user aggregation
		if wantsAllUsers(c) {
			svc := app.AgentPoolService()
			if svc != nil {
				usm := svc.UserServicesManager()
				if usm != nil {
					userID := getUserID(c)
					userIDs, _ := usm.ListAllUserIDs()
					userGroups := map[string]any{}
					for _, uid := range userIDs {
						if uid == userID {
							continue
						}
						userJobSvc, err := svc.JobServiceForUser(uid)
						if err != nil {
							continue
						}
						userTasks := userJobSvc.ListTasks()
						if len(userTasks) == 0 {
							continue
						}
						userGroups[uid] = map[string]any{"tasks": userTasks}
					}
					if len(userGroups) > 0 {
						return c.JSON(http.StatusOK, map[string]any{
							"tasks":       tasks,
							"user_groups": userGroups,
						})
					}
				}
			}
		}

		return c.JSON(http.StatusOK, tasks)
	}
}

// GetTaskEndpoint returns a single agent task by ID.
// @Summary Get an agent task
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Task ID"
// @Success 200 {object} schema.Task "task"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{id} [get]
func GetTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		task, err := getJobService(app, c).GetTask(id)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, task)
	}
}

// ExecuteJobEndpoint creates and runs a new job for a task.
// @Summary Execute an agent job
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param request body schema.JobExecutionRequest true "Job execution request"
// @Success 201 {object} schema.JobExecutionResponse "job created"
// @Failure 400 {object} map[string]string "error"
// @Router /api/agent/jobs/execute [post]
func ExecuteJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.JobExecutionRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body: " + err.Error()})
		}

		if req.Parameters == nil {
			req.Parameters = make(map[string]string)
		}

		var multimedia *schema.MultimediaAttachment
		if len(req.Images) > 0 || len(req.Videos) > 0 || len(req.Audios) > 0 || len(req.Files) > 0 {
			multimedia = &schema.MultimediaAttachment{
				Images: req.Images,
				Videos: req.Videos,
				Audios: req.Audios,
				Files:  req.Files,
			}
		}

		jobID, err := getJobService(app, c).ExecuteJob(req.TaskID, req.Parameters, "api", multimedia)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		baseURL := c.Scheme() + "://" + c.Request().Host
		return c.JSON(http.StatusCreated, schema.JobExecutionResponse{
			JobID:  jobID,
			Status: "pending",
			URL:    baseURL + "/api/agent/jobs/" + jobID,
		})
	}
}

// GetJobEndpoint returns a single job by ID.
// @Summary Get an agent job
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} schema.Job "job"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/jobs/{id} [get]
func GetJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		job, err := getJobService(app, c).GetJob(id)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, job)
	}
}

// ListJobsEndpoint lists jobs, optionally filtered by task or status.
// @Summary List agent jobs
// @Tags agent-jobs
// @Produce json
// @Param task_id query string false "Filter by task ID"
// @Param status query string false "Filter by status (pending, running, completed, failed, cancelled)"
// @Param limit query integer false "Max number of jobs to return"
// @Param all_users query string false "Set to 'true' for admin cross-user listing"
// @Success 200 {object} []schema.Job "jobs"
// @Router /api/agent/jobs [get]
func ListJobsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var taskID *string
		var status *schema.JobStatus
		limit := 0

		if taskIDParam := c.QueryParam("task_id"); taskIDParam != "" {
			taskID = &taskIDParam
		}

		if statusParam := c.QueryParam("status"); statusParam != "" {
			s := schema.JobStatus(statusParam)
			status = &s
		}

		if limitParam := c.QueryParam("limit"); limitParam != "" {
			if l, err := strconv.Atoi(limitParam); err == nil {
				limit = l
			}
		}

		jobSvc := getJobService(app, c)
		jobs := jobSvc.ListJobs(taskID, status, limit)

		// Admin cross-user aggregation
		if wantsAllUsers(c) {
			svc := app.AgentPoolService()
			if svc != nil {
				usm := svc.UserServicesManager()
				if usm != nil {
					userID := getUserID(c)
					userIDs, _ := usm.ListAllUserIDs()
					userGroups := map[string]any{}
					for _, uid := range userIDs {
						if uid == userID {
							continue
						}
						userJobSvc, err := svc.JobServiceForUser(uid)
						if err != nil {
							continue
						}
						userJobs := userJobSvc.ListJobs(taskID, status, limit)
						if len(userJobs) == 0 {
							continue
						}
						userGroups[uid] = map[string]any{"jobs": userJobs}
					}
					if len(userGroups) > 0 {
						return c.JSON(http.StatusOK, map[string]any{
							"jobs":        jobs,
							"user_groups": userGroups,
						})
					}
				}
			}
		}

		return c.JSON(http.StatusOK, jobs)
	}
}

// CancelJobEndpoint cancels a running job.
// @Summary Cancel an agent job
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "error"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/jobs/{id}/cancel [post]
func CancelJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := getJobService(app, c).CancelJob(id); err != nil {
			if errors.Is(err, agentpool.ErrJobNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job cancelled"})
	}
}

// DeleteJobEndpoint deletes a job by ID.
// @Summary Delete an agent job
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/jobs/{id} [delete]
func DeleteJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := getJobService(app, c).DeleteJob(id); err != nil {
			if errors.Is(err, agentpool.ErrJobNotFound) {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job deleted"})
	}
}

// ExecuteTaskByNameEndpoint looks up a task by name and executes it.
// @Summary Execute an agent task by name
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param name path string true "Task name"
// @Param parameters body object false "Optional template parameters"
// @Success 201 {object} schema.JobExecutionResponse "job created"
// @Failure 400 {object} map[string]string "error"
// @Failure 404 {object} map[string]string "error"
// @Router /api/agent/tasks/{name}/execute [post]
func ExecuteTaskByNameEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		var params map[string]string

		if c.Request().ContentLength > 0 {
			if err := c.Bind(&params); err != nil {
				body := make(map[string]any)
				if err := c.Bind(&body); err == nil {
					params = make(map[string]string)
					for k, v := range body {
						if str, ok := v.(string); ok {
							params[k] = str
						} else {
							params[k] = fmt.Sprintf("%v", v)
						}
					}
				} else {
					params = make(map[string]string)
				}
			}
		} else {
			params = make(map[string]string)
		}

		jobSvc := getJobService(app, c)
		tasks := jobSvc.ListTasks()
		var task *schema.Task
		for _, t := range tasks {
			if t.Name == name {
				task = &t
				break
			}
		}

		if task == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Task not found: " + name})
		}

		jobID, err := jobSvc.ExecuteJob(task.ID, params, "api", nil)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		baseURL := c.Scheme() + "://" + c.Request().Host
		return c.JSON(http.StatusCreated, schema.JobExecutionResponse{
			JobID:  jobID,
			Status: "pending",
			URL:    baseURL + "/api/agent/jobs/" + jobID,
		})
	}
}
