package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/jobs"
)

func RegisterJobRoutes(e *echo.Echo){
	e.GET("/backends/jobs",listJobs)
}

func listJobs(c echo.Context) error{
	store:=jobs.GetStore()
	runningJobs:=store.GetAllJobs()
	return c.JSON(http.StatusOK,runningJobs)
}