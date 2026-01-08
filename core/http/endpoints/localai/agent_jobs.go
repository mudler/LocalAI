package localai

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/schema"
)

// CreateTaskEndpoint creates a new agent task
// @Summary Create a new agent task
// @Description Create a new reusable agent task with prompt template and configuration
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param task body schema.Task true "Task definition"
// @Success 201 {object} map[string]string "Task created"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/agent/tasks [post]
func CreateTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var task schema.Task
		if err := c.Bind(&task); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body: " + err.Error()})
		}

		id, err := app.AgentJobService().CreateTask(task)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusCreated, map[string]string{"id": id})
	}
}

// UpdateTaskEndpoint updates an existing task
// @Summary Update an agent task
// @Description Update an existing agent task
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param id path string true "Task ID"
// @Param task body schema.Task true "Updated task definition"
// @Success 200 {object} map[string]string "Task updated"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 404 {object} map[string]string "Task not found"
// @Router /api/agent/tasks/{id} [put]
func UpdateTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var task schema.Task
		if err := c.Bind(&task); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body: " + err.Error()})
		}

		if err := app.AgentJobService().UpdateTask(id, task); err != nil {
			if err.Error() == "task not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Task updated"})
	}
}

// DeleteTaskEndpoint deletes a task
// @Summary Delete an agent task
// @Description Delete an agent task by ID
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Task ID"
// @Success 200 {object} map[string]string "Task deleted"
// @Failure 404 {object} map[string]string "Task not found"
// @Router /api/agent/tasks/{id} [delete]
func DeleteTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := app.AgentJobService().DeleteTask(id); err != nil {
			if err.Error() == "task not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Task deleted"})
	}
}

// ListTasksEndpoint lists all tasks
// @Summary List all agent tasks
// @Description Get a list of all agent tasks
// @Tags agent-jobs
// @Produce json
// @Success 200 {array} schema.Task "List of tasks"
// @Router /api/agent/tasks [get]
func ListTasksEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		tasks := app.AgentJobService().ListTasks()
		return c.JSON(http.StatusOK, tasks)
	}
}

// GetTaskEndpoint gets a task by ID
// @Summary Get an agent task
// @Description Get an agent task by ID
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Task ID"
// @Success 200 {object} schema.Task "Task details"
// @Failure 404 {object} map[string]string "Task not found"
// @Router /api/agent/tasks/{id} [get]
func GetTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		task, err := app.AgentJobService().GetTask(id)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, task)
	}
}

// ExecuteJobEndpoint executes a job
// @Summary Execute an agent job
// @Description Create and execute a new agent job
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param request body schema.JobExecutionRequest true "Job execution request"
// @Success 201 {object} schema.JobExecutionResponse "Job created"
// @Failure 400 {object} map[string]string "Invalid request"
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

		// Build multimedia struct from request
		var multimedia *schema.MultimediaAttachment
		if len(req.Images) > 0 || len(req.Videos) > 0 || len(req.Audios) > 0 || len(req.Files) > 0 {
			multimedia = &schema.MultimediaAttachment{
				Images: req.Images,
				Videos: req.Videos,
				Audios: req.Audios,
				Files:  req.Files,
			}
		}

		jobID, err := app.AgentJobService().ExecuteJob(req.TaskID, req.Parameters, "api", multimedia)
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

// GetJobEndpoint gets a job by ID
// @Summary Get an agent job
// @Description Get an agent job by ID
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} schema.Job "Job details"
// @Failure 404 {object} map[string]string "Job not found"
// @Router /api/agent/jobs/{id} [get]
func GetJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		job, err := app.AgentJobService().GetJob(id)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, job)
	}
}

// ListJobsEndpoint lists jobs with optional filtering
// @Summary List agent jobs
// @Description Get a list of agent jobs, optionally filtered by task_id and status
// @Tags agent-jobs
// @Produce json
// @Param task_id query string false "Filter by task ID"
// @Param status query string false "Filter by status (pending, running, completed, failed, cancelled)"
// @Param limit query int false "Limit number of results"
// @Success 200 {array} schema.Job "List of jobs"
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

		jobs := app.AgentJobService().ListJobs(taskID, status, limit)
		return c.JSON(http.StatusOK, jobs)
	}
}

// CancelJobEndpoint cancels a running job
// @Summary Cancel an agent job
// @Description Cancel a running or pending agent job
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "Job cancelled"
// @Failure 400 {object} map[string]string "Job cannot be cancelled"
// @Failure 404 {object} map[string]string "Job not found"
// @Router /api/agent/jobs/{id}/cancel [post]
func CancelJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := app.AgentJobService().CancelJob(id); err != nil {
			if err.Error() == "job not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job cancelled"})
	}
}

// DeleteJobEndpoint deletes a job
// @Summary Delete an agent job
// @Description Delete an agent job by ID
// @Tags agent-jobs
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "Job deleted"
// @Failure 404 {object} map[string]string "Job not found"
// @Router /api/agent/jobs/{id} [delete]
func DeleteJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := app.AgentJobService().DeleteJob(id); err != nil {
			if err.Error() == "job not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job deleted"})
	}
}

// ExecuteTaskByNameEndpoint executes a task by name
// @Summary Execute a task by name
// @Description Execute an agent task by its name (convenience endpoint). Parameters can be provided in the request body as a JSON object with string values.
// @Tags agent-jobs
// @Accept json
// @Produce json
// @Param name path string true "Task name"
// @Param request body map[string]string false "Template parameters (JSON object with string values)"
// @Success 201 {object} schema.JobExecutionResponse "Job created"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 404 {object} map[string]string "Task not found"
// @Router /api/agent/tasks/{name}/execute [post]
func ExecuteTaskByNameEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		var params map[string]string

		// Try to bind parameters from request body
		// If body is empty or invalid, use empty params
		if c.Request().ContentLength > 0 {
			if err := c.Bind(&params); err != nil {
				// If binding fails, try to read as raw JSON
				body := make(map[string]interface{})
				if err := c.Bind(&body); err == nil {
					// Convert interface{} values to strings
					params = make(map[string]string)
					for k, v := range body {
						if str, ok := v.(string); ok {
							params[k] = str
						} else {
							// Convert non-string values to string
							params[k] = fmt.Sprintf("%v", v)
						}
					}
				} else {
					// If all binding fails, use empty params
					params = make(map[string]string)
				}
			}
		} else {
			// No body provided, use empty params
			params = make(map[string]string)
		}

		// Find task by name
		tasks := app.AgentJobService().ListTasks()
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

		jobID, err := app.AgentJobService().ExecuteJob(task.ID, params, "api", nil)
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
