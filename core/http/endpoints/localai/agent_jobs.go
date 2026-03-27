package localai

import (
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

func UpdateTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var task schema.Task
		if err := c.Bind(&task); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body: " + err.Error()})
		}

		if err := getJobService(app, c).UpdateTask(id, task); err != nil {
			if err.Error() == "task not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Task updated"})
	}
}

func DeleteTaskEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := getJobService(app, c).DeleteTask(id); err != nil {
			if err.Error() == "task not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Task deleted"})
	}
}

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

func CancelJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := getJobService(app, c).CancelJob(id); err != nil {
			if err.Error() == "job not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job cancelled"})
	}
}

func DeleteJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := getJobService(app, c).DeleteJob(id); err != nil {
			if err.Error() == "job not found: "+id {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job deleted"})
	}
}

func ExecuteTaskByNameEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		name := c.Param("name")
		var params map[string]string

		if c.Request().ContentLength > 0 {
			if err := c.Bind(&params); err != nil {
				body := make(map[string]interface{})
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
